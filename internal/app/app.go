package app

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
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

	kernelEventCh chan domain.KernelEvent
	tunEventCh    chan struct{}
	proxyStatusCh chan sys.ProxyStatus
	apiPollCh     chan struct{}

	UIStateCh    chan domain.UIState
	UICommandCh  chan domain.UICommand
	webuiEventCh chan webui.Event

	lastUIState  domain.UIState
	uiStateMutex sync.Mutex

	ShowProfileManager     func(items []domain.UIProfileItem)
	ShowSubscriptionEditor func(title, defaultName, defaultUrl string, defaultInterval int) (string, string, int, bool)
}

func NewApplication(cm *config.Manager, st *state.RuntimeState) *Application {
	return &Application{
		Cfg:           cm,
		State:         st,
		Kernel:        core.NewKernelManager(cm, st),
		API:           core.NewAPIClient(cm, st),
		kernelEventCh: make(chan domain.KernelEvent, 10),
		tunEventCh:    make(chan struct{}, 1),
		proxyStatusCh: make(chan sys.ProxyStatus, 5),
		apiPollCh:     make(chan struct{}, 1),
		UIStateCh:     make(chan domain.UIState, 1),
		UICommandCh:   make(chan domain.UICommand, 10),
		webuiEventCh:  make(chan webui.Event, 1),
	}
}

func (a *Application) Bootstrap(ctx context.Context) {
	slog.Debug("初始化核心服务")

	currentAutostart := a.Cfg.Get("autostart")
	finalAutostart := ResolveAutostart(currentAutostart, a.Cfg.ExePath(), a.Cfg.BaseDir())

	if currentAutostart != finalAutostart {
		slog.Debug("自启状态不符，执行修正", "old", currentAutostart, "new", finalAutostart)
		a.Cfg.UpdateBatch(map[string]string{"autostart": finalAutostart})
		a.Cfg.FlushInitialState()
	}

	activePath := a.Cfg.GetActivePath()

	if activePath != "" {
		if err := a.Cfg.ValidatePhysicalFile(activePath); err != nil {
			if p, ok := a.Cfg.GetProfileByPath(activePath); ok && p.URL != "" {
				slog.Info("订阅丢失，尝试静默拉取", "path", activePath)
				validator := func(tmpPath string) error {
					exePath := core.GetKernelPath(a.Cfg.BaseDir())
					return core.ValidateConfig(exePath, a.Cfg.BaseDir(), tmpPath)
				}

				success, fetchErr := a.Cfg.UpgradeSubscription(activePath, "", validator)

				if !success {
					slog.Warn("静默拉取失败，进入空转", "err", fetchErr)
					a.Cfg.SetActiveProfile("")
					activePath = ""
				} else {
					slog.Info("静默拉取成功，底稿恢复")
				}
			} else {
				slog.Warn("活跃配置丢失，进入空转")
				a.Cfg.SetActiveProfile("")
				activePath = ""
			}
		}
	}

	a.SyncRuntimeConfig()

	runtimeAbs := filepath.Join(a.Cfg.BaseDir(), core.RuntimeConfigName)
	apiAddr, apiSecret := a.Cfg.ResolveKernelEndpoint(runtimeAbs)
	a.API.SetEndpoint(apiAddr, apiSecret)

	if a.Cfg.Get("tun") == "true" {
		a.State.SetTunRequestedTime(time.Now())
	}

	a.syncSystemProxy()
	a.pushUIState()

	slog.Debug("启动系统事件监听")
	a.Kernel.SetPreStartHook(a.SyncRuntimeConfig)
	go a.Kernel.RunDaemon(ctx, a.kernelEventCh)
	go sys.WatchNetworkInterfaces(ctx, a.tunEventCh)
	go sys.WatchProxyRegistry(ctx, a.proxyStatusCh)
	go a.eventLoop(ctx)
}

func (a *Application) SafeShutdown(cancel context.CancelFunc) {
	slog.Info("执行安全退出序列")
	a.State.ForceExitPhase()

	if cancel != nil {
		cancel()
	}

	slog.Debug("发送内核停止指令")
	a.Kernel.KillCurrent()

	if a.Cfg.Get("proxy") == "true" {
		slog.Info("关闭系统代理")
		if err := sys.SetSystemProxy(false, ""); err != nil {
			slog.Error("关闭系统代理失败", "err", err)
		}
	}

	slog.Debug("释放内核资源")
	a.Kernel.Close()
	slog.Info("后台服务已停止")
}

func (a *Application) eventLoop(ctx context.Context) {
	slog.Debug("进入主事件循环")
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	subTicker := time.NewTicker(10 * time.Minute)
	defer subTicker.Stop()

	tryPollAPI := func() {
		if a.State.GetPhase() == domain.PhaseRunning && !a.State.IsConfigSyncing() && !a.State.IsReloading() {
			if a.pollKernelAPI(ctx) {
				slog.Debug("内核 API 状态变更")
				a.pushUIState()
			}
		}
	}

	for {
		select {
		case event := <-a.webuiEventCh:
			if event == webui.EventError {
				slog.Error("WebUI 运行异常")
			}
		case <-ctx.Done():
			slog.Debug("退出主事件循环")
			return

		case cmd := <-a.UICommandCh:
			slog.Debug("收到 UI 指令", "action", cmd.Action, "payload", cmd.Payload)
			a.handleUICommand(ctx, cmd)

		case event := <-a.kernelEventCh:
			if event == domain.EventKernelReady {
				slog.Info("内核进程已启动")

				if a.Cfg.Get("tun") == "true" {
					a.State.SetTunRequestedTime(time.Now())
				}
				a.syncSystemProxy()

				currentGen := a.State.AdvanceProbeGen()

				go func(gen uint64) {
					defer a.State.SetRestarting(false)

					for i := 0; i < 600; i++ {
						if a.State.IsExiting() || ctx.Err() != nil {
							return
						}

						if a.State.GetProbeGen() != gen {
							return
						}

						if a.State.GetPhase() == domain.PhaseRunning {
							return
						}

						pollCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
						_, err := a.API.DoRequest(pollCtx, "GET", "/version", nil)
						cancel()

						if err == nil {
							slog.Info("内核 API 已就绪")
							a.State.SetPhase(domain.PhaseRunning)
							select {
							case a.apiPollCh <- struct{}{}:
							default:
							}
							return
						}

						select {
						case <-ctx.Done():
							return
						case <-time.After(1 * time.Second):
						}
					}

					if a.State.GetProbeGen() == gen && !a.State.IsExiting() {
						slog.Error("内核无响应，守护进程挂起")
						a.Kernel.HaltDaemon()
						a.State.SetPhase(domain.PhaseInitializing)
						a.pushUIState()
					}
				}(currentGen)

			} else if event == domain.EventKernelExit {
				a.State.AdvanceProbeGen()

				if a.State.IsRestarting() {
					slog.Info("内核已停止，等待重启")
				} else {
					slog.Warn("内核异常退出")
				}
				a.State.SetPhase(domain.PhaseInitializing)
			}
			a.pushUIState()

		case <-a.tunEventCh:
			a.handleTunChange(ctx)

		case status := <-a.proxyStatusCh:
			a.handleProxyStatusChange(ctx, status)

		case <-ticker.C:
			tryPollAPI()
			a.pushUIState()

		case <-a.apiPollCh:
			tryPollAPI()
			a.pushUIState()

		case <-subTicker.C:
			for _, p := range a.Cfg.GetProfiles() {
				if p.NeedUpdate() {
					slog.Debug("触发自动更新任务", "name", p.Name)
					go a.executeRemoteUpdate(ctx, p.Path, false, false)
				}
			}
		}
	}
}
