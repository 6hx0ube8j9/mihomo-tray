package app

import (
	"sync"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
)

type Presenter struct {
	cfg      *config.Manager
	st       *state.RuntimeState
	probe    *KernelProbe
	notifyCh chan struct{}

	lastUIState domain.UIState
	mu          sync.RWMutex
}

func NewPresenter(cfg *config.Manager, st *state.RuntimeState, probe *KernelProbe, notifyCh chan struct{}) *Presenter {
	return &Presenter{
		cfg:      cfg,
		st:       st,
		probe:    probe,
		notifyCh: notifyCh,
	}
}

func (p *Presenter) CalculateUIState() domain.UIState {
	cfg := p.cfg.GetConfig()

	s := domain.UIState{
		IsBusy:           p.st.IsActionBusy(),
		IsTun:            cfg.Config.Tun.Enable,
		IsProxy:          p.cfg.IsSystemProxyEnabled(),
		Mode:             cfg.Config.Mode,
		AutoStart:        p.cfg.IsAutostartEnabled(),
		RunAsAdmin:       cfg.General.RunAsAdmin,
		UseSystemBrowser: p.cfg.IsSystemBrowserEnabled(),
		RemoteWebUI:      p.cfg.IsRemoteWebUIEnabled(),
		AllowLan:         p.cfg.GetEffectiveAllowLan(),
		IsAdmin:          sys.IsAdmin(),

		MixedPort: p.cfg.GetEffectiveMixedPort(),
		SocksPort: p.cfg.GetEffectivePort(cfg.Config.SocksPort, domain.DefaultSocksPort),
		HttpPort:  p.cfg.GetEffectivePort(cfg.Config.Port, domain.DefaultPort),
	}

	activePath := p.cfg.GetActivePath()
	profiles := p.cfg.GetProfiles()
	s.CanAddProfile = len(profiles) < domain.MaxProfileCount

	s.ProfileItems = make([]domain.UIProfileItem, 0, len(profiles))
	for _, prof := range profiles {
		item := domain.UIProfileItem{
			Name:     domain.TruncateMiddle(prof.Name),
			Path:     prof.Path,
			IsActive: prof.Path == activePath && activePath != "",
			IsRemote: prof.URL != "",
			Interval: prof.Interval,
		}

		if item.IsRemote {
			item.LastUpdate = prof.FormatLastUpdateText()
		}
		s.ProfileItems = append(s.ProfileItems, item)
	}

	if p.st.IsExiting() {
		s.IconState = domain.IconStop
		return s
	}

	if !p.st.IsActionBusy() {
		if p.st.IsRestarting() || p.st.GetPhase() != domain.PhaseRunning {
			s.IconState = domain.IconStop
			return s
		}
	}

	if !s.IsTun {
		if s.IsProxy {
			s.IconState = domain.IconProxy
		} else {
			s.IconState = domain.IconDefault
		}
		return s
	}

	if p.st.IsTunAlive() || p.probe.IsTunInGracePeriod() {
		s.IconState = domain.IconTun
	} else {
		s.IconState = domain.IconError
	}
	return s
}

func (p *Presenter) PushUIState() {
	if p.st.IsExiting() || p.st.IsActionBusy() {
		return
	}

	p.mu.Lock()
	newState := p.CalculateUIState()
	changed := p.hasStateChanged(&newState, &p.lastUIState)

	if changed {
		p.lastUIState = newState
	}
	p.mu.Unlock()

	if changed {
		select {
		case p.notifyCh <- struct{}{}:
		default:
		}
	}
}

func (p *Presenter) ForcePushUIState() {
	if p.st.IsExiting() {
		return
	}

	p.mu.Lock()
	p.lastUIState = p.CalculateUIState()
	p.mu.Unlock()

	select {
	case p.notifyCh <- struct{}{}:
	default:
	}
}

func (p *Presenter) GetSnapshot() domain.UIState {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stateSnapshot := p.lastUIState
	items := make([]domain.UIProfileItem, len(p.lastUIState.ProfileItems))
	copy(items, p.lastUIState.ProfileItems)
	stateSnapshot.ProfileItems = items
	return stateSnapshot
}

func (p *Presenter) hasStateChanged(curr, last *domain.UIState) bool {
	if curr.IconState != last.IconState || curr.IsBusy != last.IsBusy ||
		curr.IsTun != last.IsTun || curr.IsProxy != last.IsProxy ||
		curr.Mode != last.Mode || curr.AllowLan != last.AllowLan ||
		curr.AutoStart != last.AutoStart || curr.RunAsAdmin != last.RunAsAdmin ||
		curr.UseSystemBrowser != last.UseSystemBrowser || curr.RemoteWebUI != last.RemoteWebUI ||
		curr.IsAdmin != last.IsAdmin || curr.MixedPort != last.MixedPort ||
		curr.SocksPort != last.SocksPort || curr.HttpPort != last.HttpPort ||
		len(curr.ProfileItems) != len(last.ProfileItems) {
		return true
	}

	for i := range curr.ProfileItems {
		if curr.ProfileItems[i].Path != last.ProfileItems[i].Path ||
			curr.ProfileItems[i].IsActive != last.ProfileItems[i].IsActive ||
			curr.ProfileItems[i].Interval != last.ProfileItems[i].Interval ||
			curr.ProfileItems[i].LastUpdate != last.ProfileItems[i].LastUpdate {
			return true
		}
	}
	return false
}
