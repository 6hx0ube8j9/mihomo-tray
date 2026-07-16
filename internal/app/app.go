package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"mihomo-tray/internal/core"
	"mihomo-tray/internal/fsm"
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

type Application struct {
	Cfg    *fsm.Manager
	Kernel *core.KernelManager
	API    *core.APIClient

	kernelEventCh chan core.KernelEvent
	tunEventCh    chan struct{}
	proxyEventCh  chan bool
	apiPollCh     chan struct{}

	UIStateCh    chan ui.UIState
	UICommandCh  chan ui.UICommand
	webuiEventCh chan ui.Event

	lastUIState ui.UIState
}

func NewApplication(cm *fsm.Manager) *Application {
	return &Application{
		Cfg:           cm,
		Kernel:        core.NewKernelManager(cm),
		API:           core.NewAPIClient(cm),
		kernelEventCh: make(chan core.KernelEvent, 1),
		tunEventCh:    make(chan struct{}, 1),
		proxyEventCh:  make(chan bool, 1),
		apiPollCh:     make(chan struct{}, 1),
		UIStateCh:     make(chan ui.UIState, 1),
		UICommandCh:   make(chan ui.UICommand, 10),
		webuiEventCh:  make(chan ui.Event, 1),
	}
}

func (a *Application) Bootstrap(ctx context.Context) {
	a.Cfg.EnsureDefault()
	
	osTaskExists := sys.CheckAutoStartStatus()
	cfgMemoryStatus := a.Cfg.Get("autostart") == "true"

	if osTaskExists {
		if !sys.IsTaskPathValid(a.Cfg.ExePath()) {
			if cfgMemoryStatus {
				sys.ToggleAutoStart(a.Cfg.ExePath(), a.Cfg.BaseDir(), true)
				osTaskExists = true
			} else {
				sys.ToggleAutoStart(a.Cfg.ExePath(), a.Cfg.BaseDir(), false)
				osTaskExists = false
			}
		}
	}

	if osTaskExists != cfgMemoryStatus {
		a.Cfg.Set("autostart", strconv.FormatBool(osTaskExists))
	}

	a.Cfg.SyncWithYAML()
	a.Cfg.State.MuteAPIWatcher(5 * time.Second)

	if a.Cfg.Get("proxy") == "true" {
		_ = sys.EnableSystemProxy(a.Cfg.Get("port"))
	}

	a.pushUIState()

	go a.Kernel.RunDaemon(ctx, a.kernelEventCh)
	go sys.WatchNetworkInterfaces(ctx, a.tunEventCh)
	go a.watchProxyAdapter(ctx)
	go a.eventLoop(ctx)
}

func (a *Application) SafeShutdown(cancel context.CancelFunc) {
	a.Cfg.State.ForceExitPhase()
	if cancel != nil {
		cancel()
	}
	
	if a.Cfg.Get("proxy") == "true" {
		_ = sys.DisableSystemProxy()
	}	
	a.Kernel.Close()
}

func (a *Application) eventLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case event := <-a.webuiEventCh:
			if event == ui.EventError {
			}
		case <-ctx.Done():
			return

		case cmd := <-a.UICommandCh:
			a.handleUICommand(ctx, cmd)

		case event := <-a.kernelEventCh:
			if event == core.EventKernelReady {
				a.Cfg.State.SetPhase(fsm.PhaseRunning)
				a.Cfg.State.MuteAPIWatcher(5 * time.Second)
				if a.Cfg.Get("tun") == "true" {
					a.Cfg.State.SetTunRequestedTime(time.Now())
				}

                go func() {
					defer a.Cfg.State.SetRestarting(false)
					for i := 0; i < 20; i++ {
						pollCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
						_, err := a.API.DoRequest(pollCtx, "GET", "/configs", nil)
						cancel()
						if err == nil {
							a.syncAllConfig(ctx)
						
							if a.pollKernelAPI(ctx) {
								a.pushUIState()
							}
							
							select {
							case a.apiPollCh <- struct{}{}:
							default:
							}
							return
						}
						time.Sleep(250 * time.Millisecond)
					}
				}()
			} else if event == core.EventKernelExit {
				a.Cfg.State.SetPhase(fsm.PhaseInitializing)
			}
			a.pushUIState()

		case <-a.tunEventCh:
			a.handleTunChange(ctx)

		case isProxyActive := <-a.proxyEventCh:
			if !isProxyActive {
				a.Cfg.Set("proxy", "false")
			} else {
				_ = sys.EnableSystemProxy(a.Cfg.Get("port"))
			}
			a.pushUIState()

		case <-ticker.C:
			if a.Cfg.State.GetPhase() == fsm.PhaseRunning && !a.Cfg.State.IsAPIWatcherMuted() {
				if a.pollKernelAPI(ctx) {
					a.pushUIState()
				}
			}
			a.pushUIState()

		case <-a.apiPollCh:
			if a.Cfg.State.GetPhase() == fsm.PhaseRunning && !a.Cfg.State.IsAPIWatcherMuted() {
				if a.pollKernelAPI(ctx) {
					a.pushUIState()
				}
			}
		}
	}
}

func (a *Application) handleUICommand(ctx context.Context, cmd ui.UICommand) {
	switch cmd.Action {
	case "OpenWebUI":
		if a.Cfg.State.GetPhase() != fsm.PhaseRunning {
			break
		}
		cfg := ui.Config{
			APIAddr:   a.Cfg.Get("external-controller"),
			Secret:    a.Cfg.Get("secret"),
			ProxyPort: a.Cfg.Get("port"),
			BaseDir:   a.Cfg.BaseDir(),
		}
		go ui.Launch(cfg, a.webuiEventCh)
	case "ExitApp":
		ui.Cleanup()
	case "ToggleProxy":
		enable := cmd.Payload == "true"
		a.Cfg.Set("proxy", strconv.FormatBool(enable))
		if enable {
			_ = sys.EnableSystemProxy(a.Cfg.Get("port"))
		} else {
			_ = sys.DisableSystemProxy()
		}
	case "ToggleTun":
		enable := cmd.Payload == "true"
		a.Cfg.Set("tun", strconv.FormatBool(enable))
		if enable {
			a.Cfg.State.SetTunRequestedTime(time.Now())
		}
		a.Cfg.State.MuteAPIWatcher(3 * time.Second)
		go a.API.SyncConfigToKernel(ctx, map[string]interface{}{"tun": map[string]bool{"enable": enable}})
	case "SwitchMode":
		a.Cfg.Set("mode", cmd.Payload)
		a.Cfg.State.MuteAPIWatcher(2 * time.Second)
		go a.syncAllConfig(ctx)

	case "ToggleAutoStart":
		enable := cmd.Payload == "true"
		a.Cfg.Set("autostart", cmd.Payload)
		sys.ToggleAutoStart(a.Cfg.ExePath(), a.Cfg.BaseDir(), enable)
	case "OpenBaseDir":
		baseDir := a.Cfg.BaseDir()
		_ = sys.ExecuteSystemCommand(baseDir)
	case "ReloadConfig":
		a.ReloadConfig(ctx)
	case "RestartKernel":
		a.RestartKernel()
	case "OpenConfigFile":
		configPath := filepath.Join(a.Cfg.BaseDir(), "config.yaml")
		_ = sys.ExecuteSystemCommand(configPath)
	}
	
	a.pushUIState()
}

func (a *Application) calculateUIState() ui.UIState {
	s := ui.UIState{
		IsTun:     a.Cfg.Get("tun") == "true",
		IsProxy:   a.Cfg.Get("proxy") == "true",
		Mode:      a.Cfg.Get("mode"),
		AutoStart: a.Cfg.Get("autostart") == "true", 
	}

	if a.Cfg.State.GetPhase() != fsm.PhaseRunning || a.Cfg.State.IsRestarting() {
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

	if a.Cfg.State.IsReloading() {
		if !a.Cfg.State.IsTunAlive() {
			s.IconState = IconError
		} else {
			s.IconState = IconTun
		}
		return s
	}

	if a.Cfg.State.IsTunAlive() ||
		time.Since(a.Cfg.State.GetTunStartTime()) < 5*time.Second ||
		time.Since(a.Cfg.State.GetTunRequestedTime()) < 5*time.Second ||
		time.Since(a.Cfg.State.GetTunLostTime()) < 6*time.Second {
		s.IconState = IconTun
	} else {
		s.IconState = IconError
	}

	return s
}

func (a *Application) pushUIState() {
	if a.Cfg.State.IsExiting() {
		return
	}
	newState := a.calculateUIState()

	if newState != a.lastUIState {
		a.lastUIState = newState
		select {
		case a.UIStateCh <- newState:
		default:
			<-a.UIStateCh
			a.UIStateCh <- newState
		}
	}
}

func (a *Application) ReloadConfig(ctx context.Context) {
	a.Cfg.State.SetReloading(true)
	a.Cfg.State.SetRestarting(false)
	a.Cfg.State.MuteAPIWatcher(5 * time.Second)

	oldPort := a.Cfg.Get("port")
	isProxyOn := a.Cfg.Get("proxy") == "true"

	go func() {
		defer a.Cfg.State.SetReloading(false)
		a.Cfg.SyncWithYAML()
		portChanged := oldPort != "" && oldPort != a.Cfg.Get("port")

		if portChanged && isProxyOn {
			a.Cfg.Set("proxy", "false")
			_ = sys.DisableSystemProxy()
			a.pushUIState()
		}

		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := a.API.DoRequest(reqCtx, "PUT", "/configs?force=true", map[string]interface{}{"path": "", "payload": ""})
		cancel()

		if err != nil {
			if portChanged && isProxyOn {
				_ = sys.EnableSystemProxy(a.Cfg.Get("port"))
				a.Cfg.Set("proxy", "true")
				a.pushUIState()
			}
			return
		}

		a.syncAllConfig(ctx)

		if portChanged && isProxyOn {
			time.Sleep(500 * time.Millisecond)
			_ = sys.EnableSystemProxy(a.Cfg.Get("port"))
			a.Cfg.Set("proxy", "true")
		}

		a.pushUIState()
		select {
		case a.apiPollCh <- struct{}{}:
		default:
		}
	}()
}

func (a *Application) RestartKernel() {
	a.Cfg.State.SetRestarting(true)
	a.Cfg.State.SetReloading(false)
	a.Cfg.State.MuteAPIWatcher(5 * time.Second)
	a.Cfg.SyncWithYAML()

	a.Kernel.KillCurrent()
	a.pushUIState()
}

func (a *Application) handleTunChange(ctx context.Context) {
    if a.Cfg.State.IsExiting() {
        return
    }
    alive := sys.IsTunActive(a.Cfg.Get("tun_device"))

    if a.Cfg.State.IsTunAlive() != alive {
        if !alive {
            a.Cfg.State.SetTunLostTime(time.Now())
        }
        if !alive && !a.Cfg.State.IsAPIWatcherMuted() {
            go func() {
                for i := 0; i < 3; i++ {
                    pollCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
                    success := a.pollKernelAPI(pollCtx)
                    cancel()
                    if success {
                        break
                    }
                    time.Sleep(100 * time.Millisecond)
                }
                a.Cfg.State.SetTunAlive(alive)
                select {
                case a.apiPollCh <- struct{}{}:
                default:
                }
            }()
        } else {
            a.Cfg.State.SetTunAlive(alive)
            a.pushUIState()
        }
    }
}

func (a *Application) watchProxyAdapter(ctx context.Context) {
    sys.WatchProxyRegistry(ctx,
        func() bool { return a.Cfg.Get("proxy") == "true" },
        func() string { return a.Cfg.Get("port") },
        func() {
            select {
            case a.proxyEventCh <- false:
            case <-ctx.Done():
            }
        },
        func() {
            select {
            case a.proxyEventCh <- true:
            case <-ctx.Done():
            }
        },
    )
}

func (a *Application) syncAllConfig(ctx context.Context) {
	if a.Cfg.State.GetPhase() != fsm.PhaseRunning {
		return
	}
	payload := map[string]interface{}{
		"tun":  map[string]bool{"enable": a.Cfg.Get("tun") == "true"},
		"mode": a.Cfg.Get("mode"),
	}
	_ = a.API.SyncConfigToKernel(ctx, payload)
}

func (a *Application) pollKernelAPI(ctx context.Context) bool {
	queryCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()

	body, err := a.API.DoRequest(queryCtx, "GET", "/configs", nil)
	if err != nil {
		return false
	}

	var resp struct {
		Mode string `json:"mode"`
		Tun  struct {
			Enable bool `json:"enable"`
		} `json:"tun"`
	}
	if json.Unmarshal(body, &resp) == nil {
		changed := false
		if resp.Mode != "" && resp.Mode != a.Cfg.Get("mode") {
			a.Cfg.Set("mode", resp.Mode)
			changed = true
		}
		if resp.Tun.Enable != (a.Cfg.Get("tun") == "true") {
			a.Cfg.Set("tun", fmt.Sprintf("%t", resp.Tun.Enable))
			changed = true
		}
		return changed
	}
	return false
}
