package app

import (
	"context"
	"log/slog"
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
	pollActiveInterval = 3 * time.Second  // 活跃状态轮询间隔
	pollIdleInterval   = 8 * time.Second  // 待机静置轮询间隔
	pollIdleThreshold  = 30 * time.Second // 静置判定超时时长
)

type Application struct {
	Cfg    *config.Manager
	State  *state.RuntimeState
	Kernel *core.KernelManager
	API    *core.APIClient
	WebUI  *webui.Manager
	ui     UIPort

	kernelEventCh chan domain.KernelEvent
	tunEventCh    chan struct{}
	proxyStatusCh chan sys.ProxyStatus
	apiPollCh     chan struct{}

	UICommandCh  chan domain.UICommand
	webuiEventCh chan webui.Event

	UIStateNotifyCh chan struct{}

	lastUIState  domain.UIState
	uiStateMutex sync.RWMutex
	runtimeValidated bool
}

func NewApplication(cm *config.Manager, st *state.RuntimeState) *Application {
	return &Application{
		Cfg:             cm,
		State:           st,
		Kernel:          core.NewKernelManager(cm, st),
		API:             core.NewAPIClient(st),
		WebUI:           webui.NewManager(),
		kernelEventCh:   make(chan domain.KernelEvent, 10),
		tunEventCh:      make(chan struct{}, 1),
		proxyStatusCh:   make(chan sys.ProxyStatus, 5),
		apiPollCh:       make(chan struct{}, 1),
		UICommandCh:     make(chan domain.UICommand, 10),
		webuiEventCh:    make(chan webui.Event, 1),
		UIStateNotifyCh: make(chan struct{}, 1),
	}
}

func (a *Application) SetUIPort(ui UIPort) {
	a.ui = ui
}

func (a *Application) Bootstrap(ctx context.Context) {
	slog.Debug("正在初始化核心服务")

	activePath := a.Cfg.GetActivePath()

	if activePath != "" {
		if err := a.Cfg.ValidatePhysicalFile(activePath); err != nil {
			if p, ok := a.Cfg.GetProfileByPath(activePath); ok && p.URL != "" {
				slog.Info("活跃配置文件不存在，尝试自动拉取远程订阅", "path", activePath)

				fetchErr := a.fetchAndCommitRemoteProfile(context.Background(), activePath, p.URL, &p)
				if fetchErr != nil {
					slog.Warn("自动拉取订阅失败，取消配置激活", "err", fetchErr)
					a.Cfg.SetActiveProfile("")
					activePath = ""
				} else {
					slog.Info("自动拉取订阅成功，配置已恢复")
				}
			} else {
				slog.Warn("活跃配置文件不存在，取消配置激活")
				a.Cfg.SetActiveProfile("")
				activePath = ""
			}
		}
	}

	a.CheckAndReconcilePrivileges(true)
	a.SyncRuntimeConfig()

	if a.Cfg.GetConfig().Config.Tun.Enable {
		a.State.SetTunRequestedTime(time.Now())
	}

	a.syncSystemProxy()
	a.pushUIState()

	slog.Debug("启动系统事件监听服务")
	a.Kernel.SetPreStartHook(a.SyncRuntimeConfig)
	go a.Kernel.RunDaemon(ctx, a.kernelEventCh)
	go sys.WatchNetworkInterfaces(ctx, a.tunEventCh)
	go sys.WatchProxyRegistry(ctx, a.proxyStatusCh)
	go a.eventLoop(ctx)
}

func (a *Application) SafeShutdown(cancel context.CancelFunc) {
	slog.Info("开始执行退出流程")
	a.State.ForceExitPhase()

	slog.Debug("清理 Web 面板资源")
	a.WebUI.Cleanup()

	if cancel != nil {
		cancel()
	}

	slog.Debug("停止内核进程")
	a.Kernel.KillCurrent()

	if *a.Cfg.GetConfig().General.SystemProxy {
		slog.Debug("关闭系统代理")
		if err := sys.SetSystemProxy(false, ""); err != nil {
			slog.Warn("关闭系统代理失败 (已忽略)", "err", err)
		}
	}

	slog.Debug("释放内核资源")
	a.Kernel.Close()
	slog.Info("后台服务已完全停止")
}

func (a *Application) eventLoop(ctx context.Context) {
	slog.Debug("进入主事件循环")

	currentInterval := pollActiveInterval
	ticker := time.NewTicker(currentInterval)
	defer ticker.Stop()

	subTicker := time.NewTicker(10 * time.Minute)
	defer subTicker.Stop()

	lastUserAction := time.Now()

	tryPollAPI := func() {
		if a.State.GetPhase() == domain.PhaseRunning && !a.State.IsConfigSyncing() && !a.State.IsReloading() {
			if a.pollKernelAPI(ctx) {
				slog.Debug("内核状态发生变更，更新 UI 视图")
				a.pushUIState()
			}
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
			slog.Debug("调整内核状态轮询频率", "interval", currentInterval)

			if wasIdle && targetInterval == pollActiveInterval {
				tryPollAPI()
			}
		}
	}

	for {
		select {
		case event := <-a.webuiEventCh:
			if event == webui.EventError {
				slog.Warn("Web 面板服务运行异常")
			}

		case <-ctx.Done():
			slog.Debug("退出主事件循环")
			return

		case cmd := <-a.UICommandCh:
			slog.Debug("收到界面指令", "action", cmd.Action, "payload", cmd.Payload)
			lastUserAction = time.Now()
			adjustPollInterval()
			a.handleUICommand(ctx, cmd)

		case event := <-a.kernelEventCh:
			if event == domain.EventKernelReady {
				slog.Info("内核进程已启动")

				if a.Cfg.GetConfig().Config.Tun.Enable {
					a.State.SetTunRequestedTime(time.Now())
				}
				a.syncSystemProxy()

				currentGen := a.State.AdvanceProbeGen()

				go func(gen uint64) {
					defer a.ForcePushUIState()

					waitCtx, cancel := context.WithTimeout(ctx, core.KernelReadyTimeout)
					defer cancel()
					err := a.API.WaitForReady(waitCtx)

					if a.State.IsExiting() || ctx.Err() != nil || a.State.GetProbeGen() != gen {
						slog.Debug("内核就绪探测已被后续操作打断，取消本次探测", "gen", gen)
						return
					}

					if err == nil {
						slog.Info("内核 API 已就绪")
						a.State.SetPhase(domain.PhaseRunning)
						a.ForceSyncAPI()
					} else {
						slog.Error("内核 API 就绪超时，停止内核进程", "timeout", core.KernelReadyTimeout, "err", err)
						a.Kernel.HaltDaemon()
						a.State.SetPhase(domain.PhaseInitializing)
					}
				}(currentGen)

			} else if event == domain.EventKernelExit {
				a.State.AdvanceProbeGen()

				if a.State.IsRestarting() {
					slog.Info("内核已停止，正在等待重新启动")
				} else {
					slog.Warn("内核进程异常退出")
				}
				a.State.SetPhase(domain.PhaseInitializing)
			}
			a.pushUIState()

		case <-a.tunEventCh:
			a.handleTunChange(ctx)

		case status := <-a.proxyStatusCh:
			a.handleProxyStatusChange(ctx, status)

		case <-ticker.C:
			adjustPollInterval()
			tryPollAPI()

		case <-a.apiPollCh:
			lastUserAction = time.Now()
			adjustPollInterval()
			tryPollAPI()

		case <-subTicker.C:
			for _, p := range a.Cfg.GetProfiles() {
				if p.IsUpdateDue() {
					slog.Debug("触发后台自动更新订阅", "name", p.Name)
					go a.UpdateRemoteProfile(ctx, p.Path, false)
				}
			}
		}
	}
}
