package app

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/core"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/ui"
	"mihomo-tray/internal/webui"
)

type Application struct {
	Cfg    *config.Manager
	State  *state.RuntimeState
	Kernel *core.KernelManager
	API    *core.APIClient

	kernelEventCh chan core.KernelEvent
	tunEventCh    chan struct{}
	proxyStatusCh chan sys.ProxyStatus
	apiPollCh     chan struct{}

	UIStateCh    chan ui.UIState
	UICommandCh  chan ui.UICommand
	webuiEventCh chan webui.Event

	lastUIState  ui.UIState
	uiStateMutex sync.Mutex
}

func NewApplication(cm *config.Manager, st *state.RuntimeState) *Application {
	return &Application{
		Cfg:           cm,
		State:         st,
		Kernel:        core.NewKernelManager(cm, st),
		API:           core.NewAPIClient(cm, st),
		kernelEventCh: make(chan core.KernelEvent, 10),
		tunEventCh:    make(chan struct{}, 1),
		proxyStatusCh: make(chan sys.ProxyStatus, 5),
		apiPollCh:     make(chan struct{}, 1),
		UIStateCh:     make(chan ui.UIState, 1),
		UICommandCh:   make(chan ui.UICommand, 10),
		webuiEventCh:  make(chan webui.Event, 1),
	}
}

func (a *Application) Bootstrap(ctx context.Context) {
	slog.Debug("开始初始化后台服务")

	currentAutostart := a.Cfg.Get("autostart")
	finalAutostart := ResolveAutostart(currentAutostart, a.Cfg.ExePath(), a.Cfg.BaseDir())

	if currentAutostart != finalAutostart {
		slog.Debug("自启状态与预期不符，修正托盘配置并落盘", "old", currentAutostart, "new", finalAutostart)
		a.Cfg.UpdateBatch(map[string]string{"autostart": finalAutostart})
		a.Cfg.FlushInitialState()
	}

	activePath := a.Cfg.GetActivePath()

	if activePath != "" {
		if err := a.Cfg.ValidatePhysicalFile(activePath); err != nil {
			if p, ok := a.Cfg.GetProfileByPath(activePath); ok && p.URL != "" {
				slog.Info("开机检测到活跃订阅丢失，正在尝试后台直连静默拉取", "path", activePath)
				validator := func(tmpPath string) error {
					exePath := core.GetKernelPath(a.Cfg.BaseDir())
					return core.ValidateConfig(exePath, a.Cfg.BaseDir(), tmpPath)
				}

				success, fetchErr := a.Cfg.UpgradeSubscription(activePath, "", validator)

				if !success {
					slog.Warn("静默拉取订阅失败，将进入无配置空转状态", "err", fetchErr)
					a.Cfg.SetActiveProfile("")
					activePath = ""
				} else {
					slog.Info("开机静默拉取成功，底稿已恢复")
				}
			} else {
				slog.Warn("开机检测到活跃本地配置丢失或为空，将进入无配置空转状态")
				a.Cfg.SetActiveProfile("")
				activePath = ""
			}
		}
	}

	if _, extracted, err := a.Cfg.PrepareYAMLForPath(activePath); err != nil {
		slog.Error("开机生成核心运行配置失败", "err", err)
	} else {
		if len(extracted) > 0 {
			a.Cfg.UpdateBatch(extracted)
		}
	}

	runtimeAbs := filepath.Join(a.Cfg.BaseDir(), core.RuntimeConfigName)
	apiAddr, apiSecret := a.Cfg.ResolveKernelEndpoint(runtimeAbs)
	a.API.SetEndpoint(apiAddr, apiSecret)

	if a.Cfg.Get("tun") == "true" {
		a.State.SetTunRequestedTime(time.Now())
	}

	a.syncSystemProxy()
	a.pushUIState()

	slog.Debug("启动网卡监听与守护任务")
	go a.Kernel.RunDaemon(ctx, a.kernelEventCh)
	go sys.WatchNetworkInterfaces(ctx, a.tunEventCh)
	go sys.WatchProxyRegistry(ctx, a.proxyStatusCh)
	go a.eventLoop(ctx)
}

func (a *Application) SafeShutdown(cancel context.CancelFunc) {
	slog.Info("开始执行退出流程")
	a.State.ForceExitPhase()

	if cancel != nil {
		cancel()
	}

	slog.Debug("发送内核停止指令")
	a.Kernel.KillCurrent()

	if a.Cfg.Get("proxy") == "true" {
		slog.Info("正在关闭系统代理")
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
		if a.State.GetPhase() == state.PhaseRunning && !a.State.IsConfigSyncing() && !a.State.IsReloading() {
			if a.pollKernelAPI(ctx) {
				slog.Debug("内核 API 状态已变更")
				a.pushUIState()
			}
		}
	}

	for {
		select {
		case event := <-a.webuiEventCh:
			if event == webui.EventError {
				slog.Error("WebUI 启动或运行异常")
			}
		case <-ctx.Done():
			slog.Debug("退出主事件循环")
			return

		case cmd := <-a.UICommandCh:
			slog.Debug("收到 UI 指令", "action", cmd.Action, "payload", cmd.Payload)
			a.handleUICommand(ctx, cmd)

		case event := <-a.kernelEventCh:
			if event == core.EventKernelReady {
				slog.Info("内核进程已启动，若需下载规则集耗时较长，请耐心等待...")

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

						if a.State.GetPhase() == state.PhaseRunning {
							return
						}

						pollCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
						_, err := a.API.DoRequest(pollCtx, "GET", "/version", nil)
						cancel()

						if err == nil {
							slog.Info("内核 API 已就绪，核心网络配置生效")
							a.State.SetPhase(state.PhaseRunning)
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
						slog.Error("内核进程假死，守护进程已自动熄火挂起")
						a.Kernel.HaltDaemon()
						a.State.SetPhase(state.PhaseInitializing)
						a.pushUIState()
					}
				}(currentGen)

			} else if event == core.EventKernelExit {
				a.State.AdvanceProbeGen()

				if a.State.IsRestarting() {
					slog.Info("内核已停止，等待重启指令")
				} else {
					slog.Warn("内核异常退出，重置运行状态")
				}
				a.State.SetPhase(state.PhaseInitializing)
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
					slog.Debug("后台巡检触发自动更新任务", "name", p.Name)
					go a.executeRemoteUpdate(ctx, p.Path, false)
				}
			}
		}
	}
}
