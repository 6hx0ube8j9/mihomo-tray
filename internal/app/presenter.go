package app

import (
	"fmt"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/ui"
)

const (
	IconStop = iota
	IconError
	IconTun
	IconProxy
	IconDefault
)

func (a *Application) calculateUIState() ui.UIState {
	s := ui.UIState{
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

	for _, p := range profiles {
		item := ui.ProfileItem{
			Name:     config.TruncateMiddle(p.Name),
			Path:     p.Path,
			IsActive: p.Path == activePath && activePath != "",
			IsRemote: p.URL != "",
			Interval: p.Interval,
		}

		if item.IsRemote {
			if p.LastUpdate == 0 {
				item.LastUpdate = "从未更新"
			} else {
				diff := time.Since(time.Unix(p.LastUpdate, 0))
				if diff.Hours() > 24 {
					item.LastUpdate = fmt.Sprintf("%d 天前", int(diff.Hours()/24))
				} else if diff.Hours() > 1 {
					item.LastUpdate = fmt.Sprintf("%d 小时前", int(diff.Hours()))
				} else if diff.Minutes() > 1 {
					item.LastUpdate = fmt.Sprintf("%d 分钟前", int(diff.Minutes()))
				} else {
					item.LastUpdate = "刚刚"
				}
			}
		}
		s.ProfileItems = append(s.ProfileItems, item)
	}

	if a.State.IsExiting() || a.State.IsRestarting() || a.State.GetPhase() != state.PhaseRunning {
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
