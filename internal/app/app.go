package app

import (
	"context"
	"errors"
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
	themeEventCh    chan bool
	apiPollCh       chan struct{}
	UICommandCh     chan domain.UICommand
	UIStateNotifyCh chan struct{}

	shutdownOnce sync.Once
}

func NewApplication(cm *config.Manager, st *state.RuntimeState) *Application {
	api := core.NewAPIClient(domain.IPCNamedPipe)
	kernel := core.NewKernelManager(cm.BaseDir(), st.IsExiting, api)

	app := &Application{
		Cfg:             cm,
		State:           st,
		Kernel:          kernel,
		API:             api,
		WebUI:           webui.NewManager(),
		kernelEventCh:   make(chan domain.KernelEvent, 10),
		tunEventCh:      make(chan struct{}, 1),
		proxyStatusCh:   make(chan sys.ProxyStatus, 5),
		themeEventCh:    make(chan bool, 1),
		apiPollCh:       make(chan struct{}, 1),
		UICommandCh:     make(chan domain.UICommand, 10),
		UIStateNotifyCh: make(chan struct{}, 1),
	}

	isDark := sys.IsDarkMode()
	st.SetSystemTheme(domain.ThemeFromBool(isDark))

	app.ProxyGuard = NewProxyGuard(cm, st, func(title, msg string) {
		if app.ui != nil && !app.State.IsExiting() {
			app.ui.ShowNotification(title, msg)
		}
		app.Presenter.PushUIState()
	})

	app.KernelDeployer = NewKernelDeployer(cm, st, kernel, api)
	app.Platform = NewPlatform(cm.ExePath(), cm.BaseDir())

	app.ProfileHub = NewProfileHub(cm, func() string {
		if app.KernelDeployer.IsKernelRunning() {
			port, _ := app.Cfg.GetConfig().EffectiveSystemProxyPortStr()
			return port
		}
		return ""
	})

	app.Presenter = NewPresenter(cm, st, app.Platform, app.UIStateNotifyCh)
	app.ProfileUpdater = NewProfileUpdater(cm, st, app.ProfileHub, app.Presenter.PushUIState, app.silentReloadActiveProfile)
	app.Probe = NewKernelProbe(cm, st, api, app.Platform)
	app.KernelTuner = NewKernelTuner(cm, st, api, app.Platform, app.ForceSyncAPI)
	app.WebUIService = NewWebUIService(cm, st, app.WebUI)

	return app
}

func (a *Application) SetUIPort(ui UIPort) {
	a.ui = ui
}

func (a *Application) Bootstrap(ctx context.Context) {
	core.CleanupTempCheckFiles(a.Cfg.BaseDir())
	a.KernelTuner.ReconcilePrivilege()

	if err := a.ProfileHub.EnsureActiveValid(ctx); err != nil {
		slog.Error("启动配置失效，已回退空配置", "err", err)
		a.FallbackToEmpty(ctx, "活跃配置失效")
	} else if err := a.KernelDeployer.PrepareBootConfig(); err != nil {
		slog.Error("启动配置解析失败，已回退空配置", "err", err)
		a.FallbackToEmpty(ctx, "配置解析失败")
	}

	if a.Cfg.GetConfig().Config.Tun.Enable {
		a.State.SetTunRequestedTime(time.Now())
	}

	a.ProxyGuard.Sync()
	a.Presenter.PushUIState()

	go a.Kernel.RunDaemon(ctx, a.kernelEventCh)
	go sys.WatchNetworkInterfaces(ctx, a.tunEventCh)
	go sys.WatchSystemProxy(ctx, a.proxyStatusCh)
	go sys.WatchSystemTheme(ctx, a.themeEventCh)
	go a.eventLoop(ctx)
}

func (a *Application) ExitForRestart() {
	slog.Info("提权交接，退出当前实例")
	a.State.ForceExitPhase()

	if a.ui == nil {
		a.SafeShutdown(nil)
		os.Exit(0)
		return
	}

	a.ui.Exit()
}

func (a *Application) SafeShutdown(cancel context.CancelFunc) {
	a.shutdownOnce.Do(func() {
		a.State.ForceExitPhase()
		a.WebUI.Cleanup()
		a.ProxyGuard.SafeShutdownCleanup()

		if cancel != nil {
			cancel()
		}

		a.KernelDeployer.StopValidation()
		a.Kernel.Close()
		slog.Info("应用已退出")
	})
}

func (a *Application) FallbackToEmpty(ctx context.Context, reason string) {
	slog.Info("已回退空配置", "reason", reason)

	a.Cfg.Update(func(c *domain.TrayConfig) {
		c.Profiles.Active = ""
		bProxy := false
		c.General.SystemProxy = &bProxy
		c.Config.Tun.Enable = false
	})
	a.State.ClearTunStarting()
	a.State.ClearTunLost()
	a.State.SetTunAlive(false)

	if a.KernelDeployer.IsKernelRunning() {
		_ = a.KernelDeployer.DeployAndApply(ctx, "", "回退空配置", nil)
	} else {
		_ = a.KernelDeployer.WriteMinimalConfig()
	}

	a.ProxyGuard.Sync()
	a.Presenter.PushUIState()
}

func (a *Application) silentReloadActiveProfile(ctx context.Context, targetRelPath string) {
	if a.State.IsExiting() || ctx.Err() != nil {
		return
	}

	if !a.State.TryBeginAction(state.ActionReload) {
		slog.Info("跳过后台重载，系统繁忙", "path", targetRelPath)
		return
	}

	a.Presenter.ForcePushUIState()
	defer func() {
		a.State.EndAction()
		a.Presenter.ForcePushUIState()
		if r := recover(); r != nil {
			slog.Error("后台重载异常", "path", targetRelPath, "err", r)
		}
	}()

	if a.Cfg.GetActivePath() != targetRelPath {
		return
	}

	if err := a.KernelDeployer.ReloadConfig(ctx); err != nil {
		if errors.Is(err, errKernelNotReady) {
			slog.Info("内核未就绪，跳过后台重载", "path", targetRelPath)
			return
		}
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return
		}
		slog.Error("后台重载失败", "path", targetRelPath, "err", err)
		return
	}

	a.ProxyGuard.Sync()
	a.WebUIService.ReopenIfActive(a.ForceSyncAPI)
}

func (a *Application) eventLoop(ctx context.Context) {
	currentInterval := pollActiveInterval
	ticker := time.NewTicker(currentInterval)
	defer ticker.Stop()

	subTicker := time.NewTicker(10 * time.Minute)
	defer subTicker.Stop()

	lastUserAction := time.Now()

	tryPollAPI := func() {
		res := a.Probe.PollStatus(ctx)

		if res.TunRollback {
			slog.Error("TUN 已回滚关闭", "reason", res.RollbackReason)
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

		if res.PortChanged && a.Cfg.GetConfig().IsSystemProxyEnabled() {
			a.ProxyGuard.Sync()
		}

		if res.Changed || res.TunRollback {
			a.Presenter.PushUIState()
		}
	}

	adjustPollInterval := func() bool {
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
				return true
			}
		}
		return false
	}

	for {
		select {
		case isDark := <-a.themeEventCh:
			a.State.SetSystemTheme(domain.ThemeFromBool(isDark))
			a.Presenter.PushUIState()
			a.WebUI.UpdateTheme(isDark)

		case <-ctx.Done():
			return

		case cmd := <-a.UICommandCh:
			lastUserAction = time.Now()
			adjustPollInterval()
			a.handleUICommand(ctx, cmd)

		case event := <-a.kernelEventCh:
			switch event {
			case domain.EventKernelReady:
				slog.Info("内核已就绪")
				a.State.SetPhase(domain.PhaseRunning)
				if a.Cfg.GetConfig().Config.Tun.Enable {
					a.State.SetTunRequestedTime(time.Now())
				}
				a.ProxyGuard.Sync()

				a.ForceSyncAPI()
				a.Presenter.ForcePushUIState()
				continue

			case domain.EventKernelStopped:
				slog.Info("内核已停止")
				a.State.SetPhase(domain.PhaseInitializing)
				a.ProxyGuard.Sync()

			case domain.EventKernelCrashed:
				slog.Warn("内核异常退出")
				a.State.SetPhase(domain.PhaseInitializing)
				a.ProxyGuard.Sync()

			case domain.EventKernelHalted:
				slog.Warn("内核已暂停")
				a.State.SetPhase(domain.PhaseInitializing)
				a.ProxyGuard.Sync()
			}
			a.Presenter.PushUIState()

		case <-a.tunEventCh:
			if !a.State.IsExiting() && !a.State.IsActionBusy() {
				a.ForceSyncAPI()
			}

		case status := <-a.proxyStatusCh:
			a.ProxyGuard.HandleStatusChange(ctx, status)
			a.Presenter.PushUIState()

		case <-ticker.C:
			if !adjustPollInterval() {
				tryPollAPI()
			}

		case <-a.apiPollCh:
			lastUserAction = time.Now()
			if !adjustPollInterval() {
				tryPollAPI()
			}

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
