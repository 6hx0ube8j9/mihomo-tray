package app

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/webui"
)

const (
	pollActiveInterval = 3 * time.Second
	pollIdleInterval   = 10 * time.Second
	pollIdleThreshold  = 30 * time.Second
)

type Application struct {
	Cfg    *config.Manager
	State  *state.RuntimeState
	Kernel *core.KernelManager
	API    *core.APIClient
	WebUI  *webui.Manager
	ui     UIPort

	Deployer   *Deployer
	ProxyGuard *ProxyGuard
	Probe      *KernelProbe
	ProfileHub *ProfileHub
	Tuner      *Tuner
	HostBridge *HostBridge
	Panel      *Panel
	Presenter  *Presenter

	kernelEventCh   chan domain.KernelEvent
	tunEventCh      chan struct{}
	proxyStatusCh   chan sys.ProxyStatus
	apiPollCh       chan struct{}
	UICommandCh     chan domain.UICommand
	webuiEventCh    chan webui.Event
	UIStateNotifyCh chan struct{}

	isAutoUpdating atomic.Bool
}

func NewApplication(cm *config.Manager, st *state.RuntimeState) *Application {
	api := core.NewAPIClient(domain.IPCNamedPipe)
	kernel := core.NewKernelManager(cm, st, api)

	app := &Application{
		Cfg:             cm,
		State:           st,
		Kernel:          kernel,
		API:             api,
		WebUI:           webui.NewManager(),
		kernelEventCh:   make(chan domain.KernelEvent, 10),
		tunEventCh:      make(chan struct{}, 1),
		proxyStatusCh:   make(chan sys.ProxyStatus, 5),
		apiPollCh:       make(chan struct{}, 1),
		UICommandCh:     make(chan domain.UICommand, 10),
		webuiEventCh:    make(chan webui.Event, 1),
		UIStateNotifyCh: make(chan struct{}, 1),
	}

	app.ProxyGuard = NewProxyGuard(cm, st, func(title, msg string) {
		if app.ui != nil {
			app.ui.ShowNotification(title, msg)
		}
	})

	app.Deployer = NewDeployer(cm, st, kernel, api)
	app.ProfileHub = NewProfileHub(cm, st, app.Deployer, app.ProxyGuard.Sync)

	app.Presenter = NewPresenter(cm, st, app.UIStateNotifyCh)
	app.Probe = NewKernelProbe(cm, st, api, app.ProxyGuard)

	app.HostBridge = NewHostBridge(cm, st, func() {
		if app.Presenter != nil {
			app.Presenter.ForcePushUIState()
		}
	})

	app.Tuner = NewTuner(cm, st, api, app.ForceSyncAPI)
	app.Panel = NewPanel(cm, st, app.WebUI, app.webuiEventCh)

	return app
}

func (a *Application) SetUIPort(ui UIPort) {
	a.ui = ui
}

func (a *Application) Bootstrap(ctx context.Context) {
	slog.Debug("初始化核心服务")

	a.ProfileHub.EnsureActiveValid(ctx)
	a.HostBridge.CheckAndReconcilePrivileges(true)

	if err := a.Deployer.SyncRuntimeConfig(); err != nil {
		slog.Warn("启动时底稿配置校验失败，执行安全回退", "err", err)
		a.ProfileHub.FallbackToEmpty(ctx, "配置语法校验失败")
	}

	if a.Cfg.GetConfig().Config.Tun.Enable {
		a.State.SetTunRequestedTime(time.Now())
	}

	a.ProxyGuard.Sync()
	a.Presenter.PushUIState()

	slog.Debug("启动事件监听")
	a.Kernel.SetPreStartHook(func() { _ = a.Deployer.SyncRuntimeConfig() })
	go a.Kernel.RunDaemon(ctx, a.kernelEventCh)
	go sys.WatchNetworkInterfaces(ctx, a.tunEventCh)
	go sys.WatchProxyRegistry(ctx, a.proxyStatusCh)
	go a.eventLoop(ctx)
}

func (a *Application) SafeShutdown(cancel context.CancelFunc) {
	slog.Info("执行退出流程")
	a.State.ForceExitPhase()
	a.WebUI.Cleanup()

	slog.Debug("退出清理：安全比对并注销系统代理")
	a.ProxyGuard.SafeShutdownCleanup()

	if cancel != nil {
		cancel()
	}

	a.Kernel.KillCurrent()
	a.Kernel.Close()
	slog.Info("应用已安全退出")
}

func (a *Application) eventLoop(ctx context.Context) {
	slog.Debug("启动主事件循环")

	currentInterval := pollActiveInterval
	ticker := time.NewTicker(currentInterval)
	defer ticker.Stop()

	subTicker := time.NewTicker(10 * time.Minute)
	defer subTicker.Stop()

	lastUserAction := time.Now()

	tryPollAPI := func() {
		if a.Probe.PollStatus(ctx) {
			a.Presenter.PushUIState()
		}
	}

	adjustPollInterval := func() {
		isUserActive := time.Since(lastUserAction) < pollIdleThreshold
		isWebActive := a.WebUI.IsActive()

		targetInterval := pollIdleInterval
		if isUserActive || isWebActive {
			targetInterval = pollActiveInterval
		}

		if targetInterval != currentInterval {
			wasIdle := (currentInterval == pollIdleInterval)
			currentInterval = targetInterval
			ticker.Reset(currentInterval)

			if wasIdle && targetInterval == pollActiveInterval {
				tryPollAPI()
			}
		}
	}

	for {
		select {
		case event := <-a.webuiEventCh:
			if event == webui.EventError {
				slog.Warn("Web 面板服务异常")
			}

		case <-ctx.Done():
			slog.Debug("退出主事件循环")
			return

		case cmd := <-a.UICommandCh:
			slog.Debug("处理界面指令", "action", cmd.Action)
			lastUserAction = time.Now()
			adjustPollInterval()
			a.handleUICommand(ctx, cmd)

		case event := <-a.kernelEventCh:
			if event == domain.EventKernelReady {
				slog.Info("内核及 API 已就绪")
				if a.Cfg.GetConfig().Config.Tun.Enable {
					a.State.SetTunRequestedTime(time.Now())
				}
				a.ProxyGuard.Sync()
				a.State.SetPhase(domain.PhaseRunning)

				currentGen := a.State.AdvanceProbeGen()
				go func(gen uint64) {
					defer a.Presenter.ForcePushUIState()
					if a.State.IsExiting() || ctx.Err() != nil || a.State.GetProbeGen() != gen {
						return
					}
					a.ForceSyncAPI()
				}(currentGen)

			} else if event == domain.EventKernelExit {
				a.State.AdvanceProbeGen()
				if a.Kernel.IsKilledByUs() {
					slog.Info("内核已停止，等待重启")
				} else {
					slog.Warn("内核进程异常退出")
				}
				a.State.SetPhase(domain.PhaseInitializing)
			}
			a.Presenter.PushUIState()

		case <-a.tunEventCh:
			a.Probe.HandleTunChange(ctx, a.ForceSyncAPI, a.Presenter.PushUIState)

		case status := <-a.proxyStatusCh:
			a.ProxyGuard.HandleStatusChange(ctx, status)
			a.Presenter.PushUIState()

		case <-ticker.C:
			adjustPollInterval()
			tryPollAPI()

		case <-a.apiPollCh:
			lastUserAction = time.Now()
			adjustPollInterval()
			tryPollAPI()

		case <-subTicker.C:
			a.processAutoUpdates(ctx)
		}
	}
}

func (a *Application) ForceSyncAPI() {
	select {
	case a.apiPollCh <- struct{}{}:
	default:
	}
}

func (a *Application) processAutoUpdates(ctx context.Context) {
	if !a.isAutoUpdating.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer a.isAutoUpdating.Store(false)
		hasUpdate := false

		for _, p := range a.Cfg.GetProfiles() {
			if a.State.IsExiting() || ctx.Err() != nil {
				return
			}
			if p.IsUpdateDue() {
				_ = a.ProfileHub.UpdateRemoteProfile(ctx, p.Path, false)
				hasUpdate = true
				select {
				case <-ctx.Done():
					return
				case <-time.After(3 * time.Second):
				}
			}
		}

		if hasUpdate {
			a.Presenter.PushUIState()
		}
	}()
}
