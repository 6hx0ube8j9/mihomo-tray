package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"log/slog"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/core"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/ui"
)

const (
	TunInitGracePeriod = 20 * time.Second
	TunLostAlarmDelay  = 6 * time.Second
)

const (
	IconStop = iota
	IconError
	IconTun
	IconProxy
	IconDefault
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
	webuiEventCh chan ui.Event

	lastUIState    ui.UIState
	uiStateMutex   sync.Mutex
	proxyRepairing atomic.Bool

	actualTunDevice string
	tunDevMutex     sync.RWMutex
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
		webuiEventCh:  make(chan ui.Event, 1),
	}
}

func (a *Application) getActualTunDevice() string {
	a.tunDevMutex.RLock()
	defer a.tunDevMutex.RUnlock()
	if a.actualTunDevice == "" {
		return a.Cfg.Get("tun_device")
	}
	return a.actualTunDevice
}

func (a *Application) setActualTunDevice(dev string) {
	a.tunDevMutex.Lock()
	defer a.tunDevMutex.Unlock()
	a.actualTunDevice = dev
}

func (a *Application) isTunInGracePeriod() bool {
	return time.Since(a.State.GetTunStartTime()) < TunInitGracePeriod ||
		time.Since(a.State.GetTunRequestedTime()) < TunInitGracePeriod ||
		time.Since(a.State.GetTunLostTime()) < TunLostAlarmDelay
}

func (a *Application) Bootstrap(ctx context.Context) {
	slog.Debug("开始初始化后台核心服务")
	osTaskExists := sys.CheckAutoStartStatus()
	cfgMemoryStatus := a.Cfg.Get("autostart") == "true"
	slog.Debug("检查开机自启状态", "系统任务", osTaskExists, "配置预期", cfgMemoryStatus)

	if osTaskExists {
		if !sys.IsTaskPathValid(a.Cfg.ExePath()) {
			slog.Warn("检测到系统自启任务路径不匹配，准备自动修复")
			if cfgMemoryStatus {
				sys.ToggleAutoStart(a.Cfg.ExePath(), a.Cfg.BaseDir(), true)
				osTaskExists = true
				slog.Info("自启任务路径已更新至当前位置")
			} else {
				sys.ToggleAutoStart(a.Cfg.ExePath(), a.Cfg.BaseDir(), false)
				osTaskExists = false
				slog.Info("已清除系统残留的无效自启任务")
			}
		}
	}

	if osTaskExists != cfgMemoryStatus {
		slog.Debug("同步自启状态至本地配置", "Status", osTaskExists)
		a.Cfg.Set("autostart", strconv.FormatBool(osTaskExists))
	}

	if modified, err := a.Cfg.PrepareYAMLForBoot(); err != nil {
		slog.Error("处理内核 YAML 配置失败", "err", err)
	} else if modified {
		slog.Info("已自动修正并同步内核 YAML 配置")
	} else {
		slog.Debug("内核 YAML 配置一致，跳过重写")
	}

	if a.Cfg.Get("tun") == "true" {
		a.State.SetTunRequestedTime(time.Now())
	}

	a.syncSystemProxy()
	a.pushUIState()

	slog.Debug("启动网卡监听与进程守护服务")
	go a.Kernel.RunDaemon(ctx, a.kernelEventCh)
	go sys.WatchNetworkInterfaces(ctx, a.tunEventCh)
	go sys.WatchProxyRegistry(ctx, a.proxyStatusCh)
	go a.eventLoop(ctx)
}

func (a *Application) SafeShutdown(cancel context.CancelFunc) {
	slog.Info("开始执行安全退出流程")
	a.State.ForceExitPhase()

	if cancel != nil {
		cancel()
	}

	slog.Debug("发送内核停止指令")
	a.Kernel.KillCurrent()

	if a.Cfg.Get("proxy") == "true" {
		slog.Info("正在关闭系统全局代理")
		if err := sys.SetSystemProxy(false, ""); err != nil {
			slog.Error("关闭系统代理失败", "err", err)
		}
	}

	slog.Debug("释放内核资源")
	a.Kernel.Close()
	slog.Info("后台核心服务已完全停止")
}

func (a *Application) eventLoop(ctx context.Context) {
	slog.Debug("进入主事件循环")
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	tryPollAPI := func() {
		if a.State.GetPhase() == state.PhaseRunning && !a.State.IsConfigSyncing() && !a.State.IsReloading() {
			if a.pollKernelAPI(ctx) {
				slog.Debug("内核 API 状态发生变更，已同步刷新 UI")
				a.pushUIState()
			}
		}
	}

	for {
		select {
		case event := <-a.webuiEventCh:
			if event == ui.EventError {
				slog.Error("WebUI 面板启动失败或异常崩溃")
			}
		case <-ctx.Done():
			slog.Debug("退出主事件循环")
			return

		case cmd := <-a.UICommandCh:
			slog.Debug("接收 UI 指令", "Action", cmd.Action, "Payload", cmd.Payload)
			a.handleUICommand(ctx, cmd)

		case event := <-a.kernelEventCh:
			if event == core.EventKernelReady {
				slog.Info("内核进程已拉起，等待 API 服务就绪...")
				
				if a.Cfg.Get("tun") == "true" {
					a.State.SetTunRequestedTime(time.Now())
				}
				a.syncSystemProxy()

				go func() {
					defer a.State.SetRestarting(false)
					for i := 0; i < 60; i++ {
						pollCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
						_, err := a.API.DoRequest(pollCtx, "GET", "/configs", nil)
						cancel()
						
						if err == nil {
							slog.Info("内核 API 端口已响应，等待底层网络栈收敛...", "耗时(ms)", (i+1)*250)
							time.Sleep(500 * time.Millisecond)
							
							slog.Info("内核启动完成，正式进入运行阶段")
							a.State.SetPhase(state.PhaseRunning)
							
							select {
							case a.apiPollCh <- struct{}{}:
							default:
							}
							return
						}
						time.Sleep(250 * time.Millisecond)
					}
					slog.Error("内核 API 连接超时，终止重试请求", "重试次数", 60)
					a.Kernel.HaltDaemon()
					a.State.SetPhase(state.PhaseInitializing)
					a.pushUIState()
				}()
			} else if event == core.EventKernelExit {
				if a.State.IsRestarting() {
					slog.Info("内核已停止，等待重启指令")
				} else {
					slog.Warn("内核异常退出，重置运行状态")
				}
				a.State.SetPhase(state.PhaseInitializing)
			}
			a.pushUIState()

		case <-a.tunEventCh:
			slog.Debug("检测到网卡状态变更，执行校验逻辑")
			a.handleTunChange(ctx)

		case status := <-a.proxyStatusCh:
			a.handleProxyStatusChange(status)

		case <-ticker.C:
			tryPollAPI()
			a.pushUIState()

		case <-a.apiPollCh:
			tryPollAPI()
			a.pushUIState()
		}
	}
}

func (a *Application) handleUICommand(ctx context.Context, cmd ui.UICommand) {
	switch cmd.Action {
	case "ForceSyncAPI":
		if a.State.GetPhase() == state.PhaseRunning {
			if a.pollKernelAPI(ctx) {
				a.pushUIState()
			}
		}
	case "OpenWebUI":
		if a.State.GetPhase() != state.PhaseRunning {
			slog.Warn("操作拒绝：内核未处于运行状态无法打开 WebUI")
			break
		}
		cfg := ui.Config{
			APIAddr:   a.Cfg.Get("external-controller"),
			Secret:    a.Cfg.Get("secret"),
			ProxyPort: a.Cfg.Get("port"),
			BaseDir:   a.Cfg.BaseDir(),
			UIName:    a.Cfg.Get("external-ui-name"),
		}
		slog.Info("启动独立 WebUI 面板", "API", cfg.APIAddr)
		go ui.Launch(cfg, a.webuiEventCh)
	case "ExitApp":
		slog.Info("收到退出指令")
		ui.Cleanup()
	case "ToggleProxy":
		enable := cmd.Payload == "true"
		slog.Info("切换系统代理状态", "目标状态", enable)
		a.Cfg.Set("proxy", strconv.FormatBool(enable))
		a.syncSystemProxy()
		
	case "ToggleTun":
		enable := cmd.Payload == "true"
		slog.Info("切换虚拟网卡(TUN)状态", "目标状态", enable)
		a.Cfg.Set("tun", strconv.FormatBool(enable))
		if enable {
			a.State.SetTunRequestedTime(time.Now())
			a.setActualTunDevice(a.Cfg.Get("tun_device"))
		}
		
		a.State.SetConfigSyncing(true)
		
		go func() {
			defer a.State.SetConfigSyncing(false)

			tunPayload := map[string]interface{}{"enable": enable}
			if dev := a.Cfg.Get("tun_device"); dev != "" {
				tunPayload["device"] = dev
			}

			reqCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			if err := a.API.SyncConfigToKernel(reqCtx, map[string]interface{}{"tun": tunPayload}); err != nil {
				slog.Error("通过 API 切换 TUN 模式失败", "err", err)
				a.Cfg.Set("tun", strconv.FormatBool(!enable))
			}
			
			select {
			case a.apiPollCh <- struct{}{}:
			default:
			}
		}()

	case "SwitchMode":
		slog.Info("切换路由模式", "目标模式", cmd.Payload)
		a.Cfg.Set("mode", cmd.Payload)
		
		a.State.SetConfigSyncing(true)
		
		go func() {
			defer a.State.SetConfigSyncing(false)
			
			reqCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			if err := a.API.SyncConfigToKernel(reqCtx, map[string]interface{}{"mode": cmd.Payload}); err != nil {
				slog.Error("通过 API 切换路由模式失败", "err", err)
			}
			
			select {
			case a.apiPollCh <- struct{}{}:
			default:
			}
		}()
	case "ToggleAutoStart":
		enable := cmd.Payload == "true"
		slog.Info("切换开机自启配置", "目标状态", enable)
		a.Cfg.Set("autostart", cmd.Payload)
		sys.ToggleAutoStart(a.Cfg.ExePath(), a.Cfg.BaseDir(), enable)
	case "OpenBaseDir":
		baseDir := a.Cfg.BaseDir()
		slog.Info("打开应用程序目录")
		_ = sys.ExecuteSystemCommand(baseDir)
	case "ReloadConfig":
		slog.Info("开始重载配置文件")
		a.ReloadConfig(ctx)
	case "RestartKernel":
		slog.Warn("准备重启内核进程")
		a.RestartKernel()
	case "OpenConfigFile":
		configPath := filepath.Join(a.Cfg.BaseDir(), "config.yaml")
		slog.Info("打开内核配置文件")
		_ = sys.ExecuteSystemCommand(configPath)
	}
	a.pushUIState()
}

func (a *Application) syncSystemProxy() {
	enable := a.Cfg.Get("proxy") == "true"
	port := a.Cfg.Get("port")
	if enable {
		slog.Info("系统代理配置已启用", "端口", port)
	} else {
		slog.Info("系统代理配置已关闭")
	}
	if err := sys.SetSystemProxy(enable, port); err != nil {
		slog.Error("操作系统代理设置变更失败", "err", err)
	}
}

func (a *Application) handleProxyStatusChange(status sys.ProxyStatus) {
	if a.State.IsExiting() {
		return
	}

	expectedProxy := a.Cfg.Get("proxy") == "true"
	expectedPort := a.Cfg.Get("port")
	expectedServer := "127.0.0.1:" + expectedPort

	if expectedProxy {
		if status.Enabled {
			if status.Server != "" && !strings.EqualFold(status.Server, expectedServer) {
				slog.Warn("系统代理被外部程序修改，已自动关闭本地配置状态", "当前接管方", status.Server)
				a.Cfg.Set("proxy", "false")
				a.pushUIState()
			}
			return
		}

		if !a.proxyRepairing.CompareAndSwap(false, true) {
			slog.Debug("系统代理正在自动恢复中，忽略并发的注册表变更事件")
			return
		}

		go func() {
			defer a.proxyRepairing.Store(false)

			for i := 1; i <= 10; i++ {
				if a.Cfg.Get("proxy") != "true" {
					slog.Info("用户已手动关闭系统代理，终止自动恢复流程")
					return
				}

				slog.Warn("系统代理被外部程序关闭，尝试自动恢复", "当前尝试次数", i)
				
				a.syncSystemProxy()
				
				time.Sleep(1000 * time.Millisecond)

				cur, err := sys.GetProxyStatus()
				if err == nil && cur.Enabled && strings.EqualFold(cur.Server, expectedServer) {
					slog.Info("系统代理自动恢复成功")
					return
				}
			}

			slog.Warn("系统代理自动恢复连续 10 次失败，终止重试并回退为关闭状态")
			a.Cfg.Set("proxy", "false")
			a.pushUIState()
		}()
		return
	}

	if status.Enabled {
		slog.Debug("静默模式下检测到外部程序开启代理，系统保持观察状态", "Server", status.Server)
	}
}

func (a *Application) calculateUIState() ui.UIState {
	s := ui.UIState{
		IsTun:     a.Cfg.Get("tun") == "true",
		IsProxy:   a.Cfg.Get("proxy") == "true",
		Mode:      a.Cfg.Get("mode"),
		AutoStart: a.Cfg.Get("autostart") == "true",
	}

	if a.State.IsExiting() || a.State.IsRestarting() || a.State.GetPhase() != state.PhaseRunning {
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

	if a.State.IsTunAlive() || a.isTunInGracePeriod() {
		s.IconState = IconTun
	} else {
		s.IconState = IconError
	}
	return s
}

func (a *Application) pushUIState() {
	if a.State.IsExiting() {
		return
	}
	
	a.uiStateMutex.Lock()
	defer a.uiStateMutex.Unlock()

	newState := a.calculateUIState()
	if newState != a.lastUIState {
		slog.Debug("刷新 UI 界面状态",
			"Icon", newState.IconState,
			"Tun", newState.IsTun,
			"Proxy", newState.IsProxy,
			"Mode", newState.Mode)

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
	slog.Info("启动内核配置重载流程")
	a.State.SetReloading(true)
	a.State.SetRestarting(false)

	go func() {
		defer a.State.SetReloading(false)
		
		if _, err := a.Cfg.PrepareYAMLForBoot(); err != nil {
			slog.Error("配置重载前置 YAML 检查失败", "err", err)
		}
		
		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := a.API.DoRequest(reqCtx, "PUT", "/configs?force=true", map[string]interface{}{"path": "", "payload": ""})
		cancel()

		if err != nil {
			slog.Error("内核配置重载请求执行失败", "err", err)
			return
		}
		
		time.Sleep(200 * time.Millisecond)

		slog.Info("内核配置重载成功，准备同步应用级参数")
		a.syncAllConfig(ctx)
		a.syncSystemProxy()
		
		select {
		case a.apiPollCh <- struct{}{}:
		default:
		}
	}()
}

func (a *Application) RestartKernel() {
	slog.Info("正在执行内核进程结束与重启")
	a.State.SetRestarting(true)
	a.State.SetReloading(false)
	a.Kernel.HaltDaemon()	
	if _, err := a.Cfg.PrepareYAMLForBoot(); err != nil {
		slog.Error("进程重启前置 YAML 检查失败", "err", err)
	}	
	a.Kernel.WakeDaemon()
	a.pushUIState()
}

func (a *Application) handleTunChange(ctx context.Context) {
	if a.State.IsExiting() || a.State.IsConfigSyncing() {
		return
	}
	
	tunDev := a.getActualTunDevice()
	alive := sys.IsTunActive(tunDev)

	if a.State.IsTunAlive() != alive {
		slog.Info("系统虚拟网卡(TUN)可用性发生变更", "网卡设备", tunDev, "活跃状态", alive)
		a.State.SetTunAlive(alive)
		if !alive {
			a.State.SetTunLostTime(time.Now())
		}
		
		go func() {
			for i := 0; i < 5; i++ {
				pollCtx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
				success := a.pollKernelAPI(pollCtx)
				cancel()
				if success {
					select {
					case a.apiPollCh <- struct{}{}:
					default:
					}
					return
				}
				time.Sleep(300 * time.Millisecond)
			}
		}()
		a.pushUIState()
	}
}

func (a *Application) syncAllConfig(ctx context.Context) {
	if a.State.GetPhase() != state.PhaseRunning {
		return
	}
	tunPayload := map[string]interface{}{
		"enable": a.Cfg.Get("tun") == "true",
	}
	if dev := a.Cfg.Get("tun_device"); dev != "" {
		tunPayload["device"] = dev
	}
	payload := map[string]interface{}{
		"tun":  tunPayload,
		"mode": a.Cfg.Get("mode"),
	}
	if err := a.API.SyncConfigToKernel(ctx, payload); err != nil {
		slog.Error("核心 API 参数批量同步失败", "err", err)
	}
}

func (a *Application) pollKernelAPI(ctx context.Context) bool {
	if a.State.IsExiting() || a.State.IsReloading() || a.State.IsConfigSyncing() {
		return false
	}
	
	queryCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	body, err := a.API.DoRequest(queryCtx, "GET", "/configs", nil)
	if err != nil {
		return false
	}

	var resp struct {
		Mode string `json:"mode"`
		Tun  struct {
			Enable bool   `json:"enable"`
			Device string `json:"device"`
		} `json:"tun"`
	}

	if json.Unmarshal(body, &resp) == nil {
		changed := false

		if resp.Mode != "" && resp.Mode != a.Cfg.Get("mode") {
			slog.Info("探测到内核路由模式变更，执行本地同步", "旧模式", a.Cfg.Get("mode"), "新模式", resp.Mode)
			a.Cfg.Set("mode", resp.Mode)
			changed = true
		}

		wantTun := a.Cfg.Get("tun") == "true"

		if resp.Tun.Enable != wantTun {
			slog.Info("探测到 TUN 配置发生外部变更，执行本地同步", "本地预期", wantTun, "内核实际", resp.Tun.Enable)
			a.Cfg.Set("tun", fmt.Sprintf("%t", resp.Tun.Enable))
			changed = true
			wantTun = resp.Tun.Enable
		}

		if wantTun {
			if !a.State.IsTunAlive() && !a.isTunInGracePeriod() {
				slog.Warn("TUN 核心已开启，但底层虚拟网卡未能按时初始化或已丢失，请检查驱动或权限")
			} else if a.State.GetTunStartTime().IsZero() {
				a.State.SetTunStartTime(time.Now())
			}
		} else {
			if !a.State.GetTunStartTime().IsZero() {
				a.State.SetTunStartTime(time.Time{})
			}
		}

		currentActual := a.getActualTunDevice()
		if resp.Tun.Device != "" && resp.Tun.Device != currentActual {
			a.setActualTunDevice(resp.Tun.Device)
			changed = true
		}

		if changed {
			realAlive := sys.IsTunActive(a.getActualTunDevice())
			if a.State.IsTunAlive() != realAlive {
				a.State.SetTunAlive(realAlive)
			}
		}
		return changed
	}
	return false
}
