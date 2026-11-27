package app

import (
	"slices"
	"sync"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

type Presenter struct {
	cfg      *config.Manager
	st       *state.RuntimeState
	platform *Platform
	notifyCh chan struct{}

	lastUIState domain.UIState
	mu          sync.RWMutex
}

func NewPresenter(cfg *config.Manager, st *state.RuntimeState, platform *Platform, notifyCh chan struct{}) *Presenter {
	return &Presenter{
		cfg:         cfg,
		st:          st,
		platform:    platform,
		notifyCh:    notifyCh,
		lastUIState: domain.UIState{IconState: domain.IconStop},
	}
}

func (p *Presenter) calculateUIStateLocked() domain.UIState {
	cfg := p.cfg.GetConfig()
	isBusy, pendingPath := p.st.ActionVisualState()

	s := domain.UIState{
		IsBusy:             isBusy,
		IsUpdatingProfiles: p.st.IsProfileBatchUpdating(),
		IsTun:              cfg.Config.Tun.Enable,
		IsProxy:            cfg.IsSystemProxyEnabled(),
		Mode:               cfg.Config.Mode,
		AutoStart:          cfg.IsAutostartEnabled(),
		RunAsAdmin:         cfg.General.RunAsAdmin,
		WebOpenMode:        cfg.EffectiveWebOpenMode(),
		RemoteWebUI:        cfg.IsRemoteWebUIEnabled(),
		AllowLan:           cfg.EffectiveAllowLan(),
		IsAdmin:            p.platform.IsAdmin(),
		SystemTheme:        p.st.GetSystemTheme(),

		MixedPort: cfg.EffectiveMixedPort(),
		SocksPort: cfg.EffectiveSocksPort(),
		HttpPort:  cfg.EffectivePort(),
	}

	displayActive := cfg.Profiles.Active
	if pendingPath != "" {
		displayActive = pendingPath
	}

	syncStatuses := p.st.SnapshotProfileSyncStatuses()
	profiles := cfg.Profiles.Items
	s.ProfileItems = make([]domain.UIProfileItem, 0, len(profiles))
	for _, prof := range profiles {
		syncStatus := syncStatuses[prof.Path]
		item := domain.UIProfileItem{
			Name:       domain.TruncateMiddle(prof.Name),
			Path:       prof.Path,
			IsActive:   prof.Path == displayActive && displayActive != "",
			IsPending:  pendingPath != "" && prof.Path == pendingPath,
			IsRemote:   prof.IsRemote(),
			IsUpdating: syncStatus == state.ProfileSyncUpdating,
			AutoUpdate: prof.IsAutoUpdateEnabled(),
			Interval:   prof.Interval,
		}

		if item.IsRemote {
			switch {
			case item.IsUpdating:
				item.LastUpdate = "更新中..."
			case prof.UpdateFailed:
				item.LastUpdate = "更新失败"
			default:
				item.LastUpdate = prof.FormatLastUpdateText()
			}
		}
		s.ProfileItems = append(s.ProfileItems, item)
	}

	if p.st.IsExiting() {
		s.IconState = domain.IconStop
		return s
	}

	if isBusy {
		s.IconState = p.lastUIState.IconState
		return s
	}

	if p.st.GetPhase() != domain.PhaseRunning {
		s.IconState = domain.IconStop
		return s
	}

	if !s.IsTun {
		if s.IsProxy {
			s.IconState = domain.IconProxy
		} else {
			s.IconState = domain.IconDefault
		}
		return s
	}

	if p.st.IsTunAlive() || p.st.IsTunStarting() || p.st.IsTunDropDebouncing() {
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
	newState := p.calculateUIStateLocked()
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
	p.lastUIState = p.calculateUIStateLocked()
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
		curr.IsUpdatingProfiles != last.IsUpdatingProfiles ||
		curr.IsTun != last.IsTun || curr.IsProxy != last.IsProxy ||
		curr.Mode != last.Mode || curr.AllowLan != last.AllowLan ||
		curr.AutoStart != last.AutoStart || curr.RunAsAdmin != last.RunAsAdmin ||
		curr.WebOpenMode != last.WebOpenMode ||
		curr.RemoteWebUI != last.RemoteWebUI ||
		curr.IsAdmin != last.IsAdmin ||
		curr.SystemTheme != last.SystemTheme ||
		curr.MixedPort != last.MixedPort || curr.SocksPort != last.SocksPort ||
		curr.HttpPort != last.HttpPort {
		return true
	}

	return !slices.Equal(curr.ProfileItems, last.ProfileItems)
}
