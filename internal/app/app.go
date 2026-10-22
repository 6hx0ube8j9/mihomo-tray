package app

import (
	"context"
	"log/slog"
	"os"
	"sync"
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

	ProxyGuard     *ProxyGuard
	Probe          *KernelProbe
	ProfileHub     *ProfileHub
	ProfileUpdater *ProfileUpdater
	KernelDeployer *KernelDeployer
	KernelTuner    *KernelTuner
	Platform       *Platform
	WebUIService   *WebUIService
	Presenter      *Presenter

	kernelEventCh   chan domain.KernelEvent
	tunEventCh      chan struct{}
	proxyStatusCh   chan sys.ProxyStatus
	apiPollCh       chan struct{}
	UICommandCh     chan domain.UICommand
	webuiEventCh    chan webui.Event
	UIStateNotifyCh chan struct{}

	shutdownOnce sync.Once
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

	app.KernelDeployer = NewKernelDeployer(cm, st, kernel, api)

	app.ProfileHub = NewProfileHub(cm, st, func() string {
		if app.Deployer.IsKernelRunning() {
			return app.Cfg.GetEffectiveLocalProxyPortStr()
		}
		return ""
	})

	app.Presenter = NewPresenter(cm, st, app.UIStateNotifyCh)
	app.ProfileUpdater = NewProfileUpdater(cm, st, app.ProfileHub, app.Presenter.PushUIState)
	app.Probe = NewKernelProbe(cm, st, api)

	app.Platform = NewPlatform(cm.ExePath(), cm.BaseDir())

	app.KernelTuner = NewKernelTuner(cm, st, api, app.ForceSyncAPI)
	app.WebUIService = NewWebUIService(cm, st, app.WebUI, app.webuiEventCh)

	return app
}

func (a *Application) SetUIPort(ui UIPort) {
	a.ui = ui
}

func (a *Application) Bootstrap(ctx context.Context) {
	slog.Debug("初始化核心服务")

	if !a.ProfileHub.EnsureActiveValid(ctx) {
		a.FallbackToEmpty(ctx, "活跃配置物理文件无效且无法恢复")
	}

	a.KernelTuner.ReconcilePrivilege()

	if err := a.Deployer.SyncRuntimeConfig(); err != nil {
		slog.Warn("启动时底稿配置校验失败，执行安全回退", "err", err)
		a.FallbackToEmpty(ctx, "配置语法校验失败")
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

func (a *Application) ExitForRestart() {
	slog.Info("准备交接至管理员实例，执行前置快速清理")

	a.SafeShutdown(nil)

	if a.ui != nil {
		a.ui.Exit()
	}

	time.Sleep(50 * time.Millisecond)
	os.Exit(0)
}

func (a *Application) SafeShutdown(cancel context.CancelFunc) {
	a.shutdownOnce.Do(func() {
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
	})
}

func (a *Application) FallbackToEmpty(ctx context.Context, reason string) {
	slog.Warn("配置失效，系统安全回退至空配置", "reason", reason)

	a.Cfg.Update(func(c *domain.TrayConfig) {
		c.Profiles.Active = ""
		bProxy := false
		c.General.SystemProxy = &bProxy
		c.Config.Tun.Enable = false
	})
	a.State.ClearTunStarting()
	a.State.ClearTunLost()
	a.State.SetTunAlive(false)

	if a.Deployer.IsKernelRunning() {
		_ = a.Deployer.DeployAndApply(ctx, "", "回退空配置")
	} else {
		_ = a.Deployer.SyncRuntimeConfig()
	}

	a.ProxyGuard.Sync()
	a.Presenter.PushUIState()
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
		res := a.Probe.PollStatus(ctx)

		if res.TunRollback {
			slog.Warn("执行 TUN 模式安全回滚", "reason", res.RollbackReason)
			a.Cfg.Update(func(c *domain.TrayConfig) {
				c.Config.Tun.Enable = false
			})
			a.State.ClearTunStarting()
			a.State.ClearTunLost()

			if a.ui != nil && !a.State.IsExiting() {
				a.ui.ShowNotification("TUN 模式已关闭", res.RollbackReason)
			}
		}

		if res.Diff.HasChanges() {
			a.Cfg.Update(func(c *domain.TrayConfig) {
				res.Diff.Apply(c)
			})
		}

		if res.PortChanged && a.Cfg.IsSystemProxyEnabled() {
			a.ProxyGuard.Sync()
		}

		if res.Changed || res.TunRollback {
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
			a.Probe.HandleTunChange(a.ForceSyncAPI)

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
			a.ProfileUpdater.Trigger(ctx)
		}
	}
}

func (a *Application) ForceSyncAPI() {
	select {
	case a.apiPollCh <- struct{}{}:
	default:
	}
}
