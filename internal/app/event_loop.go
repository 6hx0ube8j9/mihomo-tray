package app

import (
	"context"
	"log/slog"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/webui"
)

const (
	pollIdleInterval   = 3 * time.Second
	pollActiveInterval = 1 * time.Second
	pollIdleThreshold  = 10 * time.Minute
)

func (a *Application) eventLoop(ctx context.Context) {
	slog.Debug("启动主事件循环")

	currentInterval := pollActiveInterval
	ticker := time.NewTicker(currentInterval)
	defer ticker.Stop()

	subTicker := time.NewTicker(10 * time.Minute)
	defer subTicker.Stop()

	lastUserAction := time.Now()

	tryPollAPI := func() {
		if a.State.GetPhase() == domain.PhaseRunning && !a.State.IsConfigSyncing() && !a.State.IsReloading() {
			if a.pollKernelAPI(ctx) {
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

			if wasIdle && targetInterval == pollActiveInterval {
				tryPollAPI()
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			slog.Debug("退出主事件循环")
			return

		case event := <-a.webuiEventCh:
			if event == webui.EventError {
				slog.Warn("Web 面板服务异常")
			}

		case cmd := <-a.UICommandCh:
			slog.Debug("处理界面指令", "action", cmd.Action)
			lastUserAction = time.Now()
			adjustPollInterval()
			a.handleUICommand(ctx, cmd)

		case event := <-a.kernelEventCh:
			a.handleKernelEvent(ctx, event)

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
			a.processAutoUpdates(ctx)
		}
	}
}

func (a *Application) handleKernelEvent(ctx context.Context, event domain.KernelEvent) {
	if event == domain.EventKernelReady {
		slog.Info("内核及 API 已就绪")

		if a.Cfg.GetConfig().Config.Tun.Enable {
			a.State.SetTunRequestedTime(time.Now())
		}
		a.syncSystemProxy()
		a.State.SetPhase(domain.PhaseRunning)

		currentGen := a.State.AdvanceProbeGen()
		go func(gen uint64) {
			defer a.ForcePushUIState()
			if a.State.IsExiting() || ctx.Err() != nil || a.State.GetProbeGen() != gen {
				return
			}
			a.ForceSyncAPI()
		}(currentGen)

	} else if event == domain.EventKernelExit {
		a.State.AdvanceProbeGen()
		if a.State.IsRestarting() {
			slog.Info("内核已停止，等待重启")
		} else {
			slog.Warn("内核进程异常退出")
		}
		a.State.SetPhase(domain.PhaseInitializing)
	}
	
	a.pushUIState()
}

func (a *Application) handleTunChange(ctx context.Context) {
	if a.State.GetPhase() != domain.PhaseRunning {
		return
	}
	slog.Debug("侦测到内核 TUN 状态变化，强制同步")
	a.ForceSyncAPI()
}

func (a *Application) handleProxyStatusChange(ctx context.Context, status domain.ProxyStatus) {
	a.State.UpdateProxyState(status.Mode, status.SocksPort, status.HttpPort)
	if status.Mode == "rule" {
		a.State.SetIconState(domain.IconProxy)
	} else if status.Mode == "global" {
		a.State.SetIconState(domain.IconProxy)
	} else if status.Mode == "direct" {
		a.State.SetIconState(domain.IconDefault)
	}
	a.pushUIState()
}

func (a *Application) processAutoUpdates(ctx context.Context) {
	go func() {
		for _, p := range a.Cfg.GetProfiles() {
			if a.State.IsExiting() || ctx.Err() != nil {
				return
			}
			
			if p.IsUpdateDue() {
				slog.Debug("自动更新订阅", "name", p.Name)
				a.UpdateRemoteProfile(ctx, p.Path, false)

				time.Sleep(3 * time.Second)
			}
		}
	}()
}
