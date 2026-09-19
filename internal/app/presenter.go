package app

import (
	"fmt"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

const (
	IconStop = iota
	IconError
	IconTun
	IconProxy
	IconDefault
)

func (a *Application) calculateUIState() domain.UIState {
	s := domain.UIState{
		IsTun:      a.Cfg.Get("tun") == "true",
		IsProxy:    a.Cfg.Get("proxy") == "true",
		Mode:       a.Cfg.Get("mode"),
		AutoStart:  a.Cfg.Get("autostart") == "true",
		RunAsAdmin: a.Cfg.Get("run_as_admin") == "true",
		IsAdmin:    sys.IsAdmin(),
	}

	activePath := a.Cfg.GetActivePath()
	profiles := a.Cfg.GetProfiles()
	s.CanAddProfile = len(profiles) < 5

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
		s.IconState = IconStop
		return s
	}

	if !s.IsTun {
		if s.IsProxy {
			s.IconState = IconProxy
		} else {
			s.IconState = IconDefault
		}
		return s
	}

	if a.State.IsTunAlive() || a.isTunInGracePeriod() {
		s.IconState = IconTun
	} else {
		s.IconState = IconError
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
