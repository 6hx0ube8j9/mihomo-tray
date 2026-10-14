package app

import (
	"context"
	"log/slog"
	"time"

	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/webui"
)

type UIEnginePort interface {
	PushState(state domain.UIState)
}

type Application struct {
	Cfg    *state.ConfigManager
	State  *state.RuntimeState
	Kernel *core.KernelManager
	API    *core.APIClient
	WebUI  *webui.WebUIController
	uiPort UIEnginePort

	UICommandCh     chan domain.UICommand
	UIStateNotifyCh chan struct{}
	webuiEventCh    chan webui.Event
	kernelEventCh   chan domain.KernelEvent
	tunEventCh      chan struct{}
	proxyStatusCh   chan domain.ProxyStatus
	apiPollCh       chan struct{}
}

func NewApplication(cfg interface{}, st interface{}) *Application {
	cfgMgr := cfg.(*state.ConfigManager) 
	runtimeState := st.(*state.RuntimeState)

	a := &Application{
		Cfg:   cfgMgr,
		State: runtimeState,

		UICommandCh:     make(chan domain.UICommand, 10),
		UIStateNotifyCh: make(chan struct{}, 1),
		webuiEventCh:    make(chan webui.Event, 5),
		kernelEventCh:   make(chan domain.KernelEvent, 5),
		tunEventCh:      make(chan struct{}, 1),
		proxyStatusCh:   make(chan domain.ProxyStatus, 1),
		apiPollCh:       make(chan struct{}, 1),
	}

	a.Kernel = core.NewKernelManager(a.kernelEventCh)
	a.API = core.NewAPIClient(cfgMgr, a.tunEventCh, a.proxyStatusCh)
	a.WebUI = webui.NewWebUIController(a.webuiEventCh, cfgMgr.BaseDir())

	return a
}

func (a *Application) SetUIPort(port UIEnginePort) {
	a.uiPort = port
}

func (a *Application) GetUIStateSnapshot() domain.UIState {
	return a.State.BuildUIState(a.Cfg)
}

func (a *Application) Bootstrap(ctx context.Context) {
	slog.Info("开始引导后台业务...")

	if err := a.WebUI.Start(); err != nil {
		slog.Error("WebUI 服务启动失败", "err", err)
	}

	a.executePhysicalRestart(a.Cfg.GetConfig())
	a.eventLoop(ctx)
}

func (a *Application) SafeShutdown(cancel context.CancelFunc) {
	slog.Info("正在执行安全清理流程...")

	if a.WebUI != nil {
		a.WebUI.Stop()
	}

	a.restoreSystemProxy()

	if a.Kernel != nil {
		a.Kernel.Stop() 
	}

	slog.Info("后台业务清理完毕")
}

func (a *Application) pushUIState() {
	if a.uiPort != nil {
		a.uiPort.PushState(a.GetUIStateSnapshot())
	}
}

func (a *Application) ForcePushUIState() {
	select {
	case a.UIStateNotifyCh <- struct{}{}:
	default:
	}
}
