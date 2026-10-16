package app

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/webui"
)

type Application struct {
	Cfg    *config.Manager
	State  *state.RuntimeState
	Kernel *core.KernelManager
	API    *core.APIClient
	WebUI  *webui.Manager
	ui     UIPort

	Deployer *Deployer
	Proxy    *ProxyGuard
	Probe    *KernelProbe
	Profiles *ProfileService

	kernelEventCh   chan domain.KernelEvent
	tunEventCh      chan struct{}
	proxyStatusCh   chan sys.ProxyStatus
	apiPollCh       chan struct{}
	UICommandCh     chan domain.UICommand
	webuiEventCh    chan webui.Event
	UIStateNotifyCh chan struct{}

	lastUIState    domain.UIState
	uiStateMutex   sync.RWMutex
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

	app.Deployer = NewDeployer(cm, st, kernel, api)
	app.Proxy = NewProxyGuard(cm, st, func(title, msg string) {
		if app.ui != nil {
			app.ui.ShowNotification(title, msg)
		}
	})
	app.Probe = NewKernelProbe(cm, st, api, app.Proxy)
	app.Profiles = NewProfileService(cm, st, app.Deployer, app.Proxy)

	return app
}

func (a *Application) Bootstrap(ctx context.Context) {
	slog.Debug("初始化核心服务")

	a.ensureActiveProfileValid(ctx)

	a.CheckAndReconcilePrivileges(true)
	if err := a.Deployer.SyncRuntimeConfig(); err != nil {
		a.Profiles.FallbackToEmpty(ctx, "运行时配置加载失败")
	}

	if a.Cfg.GetConfig().Config.Tun.Enable {
		a.State.SetTunRequestedTime(time.Now())
	}

	a.Proxy.Sync()
	a.pushUIState()

	// 3. 启动监听服务与守护协程
	slog.Debug("启动事件监听")
	a.Kernel.SetPreStartHook(func() { _ = a.Deployer.SyncRuntimeConfig() })
	go a.Kernel.RunDaemon(ctx, a.kernelEventCh)
	go sys.WatchNetworkInterfaces(ctx, a.tunEventCh)
	go sys.WatchProxyRegistry(ctx, a.proxyStatusCh)
	go a.eventLoop(ctx)
}

func (a *Application) ensureActiveProfileValid(ctx context.Context) {
	activePath := a.Cfg.GetActivePath()
	if activePath == "" {
		return
	}

	if err := a.Cfg.ValidatePhysicalFile(activePath); err == nil {
		return
	}

	if p, ok := a.Cfg.GetProfileByPath(activePath); ok && p.URL != "" {
		slog.Info("当前配置物理文件丢失，尝试拉取恢复", "path", activePath)
		fCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := a.Profiles.fetchAndCommit(fCtx, activePath, p.URL, &p)
		cancel()

		if err == nil {
			slog.Info("配置已成功恢复")
			return
		}
	}

	a.Profiles.FallbackToEmpty(ctx, "物理文件丢失且无法自动恢复")
}

func (a *Application) SafeShutdown(cancel context.CancelFunc) {
	slog.Info("执行退出流程")
	a.State.ForceExitPhase()
	a.WebUI.Cleanup()

	if cancel != nil {
		cancel()
	}

	a.Kernel.KillCurrent()

	if a.Cfg.IsSystemProxyEnabled() {
		portStr, _ := a.Cfg.GetEffectiveSystemProxyPortStr()
		if err := sys.DisableProxy(portStr); err != nil {
			slog.Warn("退出注销系统代理受阻", "err", err)
		}
	}

	a.Kernel.Close()
	slog.Info("应用已安全退出")
}
