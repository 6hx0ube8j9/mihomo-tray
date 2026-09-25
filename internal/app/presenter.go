package app

import (
	"mihomo-tray/internal/config"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

func (a *Application) calculateUIState() domain.UIState {
	cfg := a.Cfg.GetConfig()
	
	s := domain.UIState{
		IsTun:            cfg.Config.Tun.Enable,
		IsProxy:          *cfg.General.SystemProxy,
		Mode:             cfg.Config.Mode,
		AutoStart:        *cfg.General.Autostart,
		RunAsAdmin:       cfg.General.RunAsAdmin,
		UseSystemBrowser: *cfg.General.SystemBrowser,
		RemoteWebUI:      *cfg.General.RemoteWebUI,
		AllowLan:         *cfg.Config.AllowLan,
		IsAdmin:          sys.IsAdmin(),
	}

	activePath := a.Cfg.GetActivePath()
	profiles := a.Cfg.GetProfiles()
	s.CanAddProfile = len(profiles) < domain.MaxProfileCount

	seenPaths := make(map[string]bool)

	for _, p := range profiles {
		if seenPaths[p.Path] {
			continue
		}
		seenPaths[p.Path] = true

		item := domain.UIProfileItem{
			Name:       config.TruncateMiddle(p.Name),
			Path:       p.Path,
			IsActive:   p.Path == activePath && activePath != "",
			IsRemote:   p.URL != "",
			Interval:   p.Interval,
		}

		if item.IsRemote {
			item.LastUpdate = p.FormatLastUpdateText()
		}
			
		s.ProfileItems = append(s.ProfileItems, item)
	}

	if a.State.IsExiting() || a.State.IsRestarting() || a.State.GetPhase() != domain.PhaseRunning {
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

	if a.State.IsTunAlive() || a.isTunInGracePeriod() {
		s.IconState = domain.IconTun
	} else {
		s.IconState = domain.IconError
	}
	return s
}

func (a *Application) pushUIState() {
	if a.State.IsExiting() {
		return
	}

	a.uiStateMutex.Lock()
	defer a.uiStateMutex.Unlock()

	newState := a.calculateUIState()
	changed := false

	if newState.IconState != a.lastUIState.IconState ||
		newState.IsTun != a.lastUIState.IsTun ||
		newState.IsProxy != a.lastUIState.IsProxy ||
		newState.Mode != a.lastUIState.Mode ||
		newState.AllowLan != a.lastUIState.AllowLan ||
		newState.AutoStart != a.lastUIState.AutoStart ||
		newState.RunAsAdmin != a.lastUIState.RunAsAdmin ||
		newState.UseSystemBrowser != a.lastUIState.UseSystemBrowser ||
		newState.RemoteWebUI != a.lastUIState.RemoteWebUI ||
		newState.IsAdmin != a.lastUIState.IsAdmin ||
		len(newState.ProfileItems) != len(a.lastUIState.ProfileItems) {
		changed = true
	} else {
		for i := range newState.ProfileItems {
			if newState.ProfileItems[i].Path != a.lastUIState.ProfileItems[i].Path ||
				newState.ProfileItems[i].IsActive != a.lastUIState.ProfileItems[i].IsActive ||
				newState.ProfileItems[i].Interval != a.lastUIState.ProfileItems[i].Interval ||
				newState.ProfileItems[i].LastUpdate != a.lastUIState.ProfileItems[i].LastUpdate {
				changed = true
				break
			}
		}
	}

	if changed {
		a.lastUIState = newState
		select {
		case a.UIStateCh <- newState:
		default:
			<-a.UIStateCh
			a.UIStateCh <- newState
		}
	}
}

func (a *Application) ForcePushUIState() {
	if a.State.IsExiting() {
		return
	}

	a.uiStateMutex.Lock()
	defer a.uiStateMutex.Unlock()

	newState := a.calculateUIState()
	a.lastUIState = newState

	select {
	case a.UIStateCh <- newState:
	default:
		<-a.UIStateCh
		a.UIStateCh <- newState
	}
}
