package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	APIMuteShortPeriod = 5 * time.Second
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

	lastUIState     ui.UIState
	proxyRetryCount int

	actualTunDevice string
	tunDevMutex     sync.RWMutex
	lastProxyModify time.Time
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
			slog.Warn("发现无效的自启任务路径，正在尝试修复")
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

	a.State.MuteAPIWatcher(TunInitGracePeriod)
	if a.Cfg.Get("tun") == "true" {
		a.State.SetTunRequestedTime(time.Now())
	}

	a.syncSystemProxy()
	a.pushUIState()

	slog.Debug("启动网卡监听与进程守护协程")
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
		if a.State.GetPhase() == state.PhaseRunning && !a.State.IsAPIWatcherMuted() {
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
				slog.Error("WebUI 面板启动失败或崩溃")
			}
		case <-ctx.Done():
			slog.Debug("退出主事件循环")
			return

		case cmd := <-a.UICommandCh:
			slog.Debug("接收 UI 指令", "Action", cmd.Action, "Payload", cmd.Payload)
			a.handleUICommand(ctx, cmd)

		case event := <-a.kernelEventCh:
			if event == core.EventKernelReady {
				slog.Info("内核进程就绪", "阶段", "Running")

				if a.State.GetPhase() != state.PhaseInitializing {
					a.State.MuteAPIWatcher(APIMuteShortPeriod)
				}
				a.State.SetPhase(state.PhaseRunning)

				if a.Cfg.Get("tun") == "true" {
					a.State.SetTunRequestedTime(time.Now())
				}

				a.syncSystemProxy()

				go func() {
					defer a.State.SetRestarting(false)
					slog.Debug("正在连接内核 API...")
					
					for i := 0; i < 60; i++ {
						pollCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
						_, err := a.API.DoRequest(pollCtx, "GET", "/configs", nil)
						cancel()
						if err == nil {
							slog.Info("内核 API 连接成功", "耗时(ms)", (i+1)*250)
							a.pushUIState()
							select {
							case a.apiPollCh <- struct{}{}:
							default:
							}
							return
						}
						time.Sleep(250 * time.Millisecond)
					}
					slog.Error("内核 API 连接超时，放弃重试", "重试次数", 60)
					a.Kernel.HaltDaemon()
					a.State.SetPhase(state.PhaseInitializing)
					a.pushUIState()
				}()
			} else if event == core.EventKernelExit {
				if a.State.IsRestarting() {
					slog.Info("内核已停止，等待重启指令")
				} else {
					slog.Warn("内核意外退出，重置运行状态")
				}
				a.State.SetPhase(state.PhaseInitializing)
			}
			a.pushUIState()

		case <-a.tunEventCh:
			slog.Debug("检测到网卡状态变更，开始检查")
			a.handleTunChange(ctx)
			tryPollAPI() 

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
			slog.Warn("拒绝打开 WebUI：内核未运行")
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
		slog.Info("切换系统代理", "目标状态", enable)
		a.Cfg.Set("proxy", strconv.FormatBool(enable))
		a.lastProxyModify = time.Now()
		a.syncSystemProxy()
	case "ToggleTun":
		enable := cmd.Payload == "true"
		slog.Info("切换虚拟网卡(TUN)", "目标状态", enable)
		a.Cfg.Set("tun", strconv.FormatBool(enable))
		if enable {
			a.State.SetTunRequestedTime(time.Now())
			a.setActualTunDevice(a.Cfg.Get("tun_device"))
		}
		
		a.State.MuteAPIWatcher(APIMuteShortPeriod)
		
		go func() {
			tunPayload := map[string]interface{}{"enable": enable}
			if dev := a.Cfg.Get("tun_device"); dev != "" {
				tunPayload["device"] = dev
			}
			if err := a.API.SyncConfigToKernel(ctx, map[string]interface{}{"tun": tunPayload}); err == nil {
				time.Sleep(150 * time.Millisecond)
				select {
				case a.apiPollCh <- struct{}{}:
				default:
				}
			} else {
				slog.Error("API 切换 TUN 失败", "err", err)
			}
		}()

	case "SwitchMode":
		slog.Info("切换路由模式", "目标模式", cmd.Payload)
		a.Cfg.Set("mode", cmd.Payload) 

		a.State.MuteAPIWatcher(3 * time.Second)
		
		go func() {
			if err := a.API.SyncConfigToKernel(ctx, map[string]interface{}{"mode": cmd.Payload}); err == nil {
				select {
				case a.apiPollCh <- struct{}{}:
				default:
				}
			} else {
				slog.Error("API 模式切换失败", "err", err)
			}
		}()

	case "ToggleAutoStart":
		enable := cmd.Payload == "true"
		slog.Info("切换开机启动", "目标状态", enable)
		a.Cfg.Set("autostart", cmd.Payload)
		sys.ToggleAutoStart(a.Cfg.ExePath(), a.Cfg.BaseDir(), enable)
	case "OpenBaseDir":
		baseDir := a.Cfg.BaseDir()
		slog.Info("打开工作目录")
		_ = sys.ExecuteSystemCommand(baseDir)
	case "ReloadConfig":
		slog.Info("开始重载配置")
		a.ReloadConfig(ctx)
	case "RestartKernel":
		slog.Warn("开始重启内核")
		a.RestartKernel()
	case "OpenConfigFile":
		configPath := filepath.Join(a.Cfg.BaseDir(), "config.yaml")
		slog.Info("打开配置文件")
		_ = sys.ExecuteSystemCommand(configPath)
	}
	
	a.pushUIState()
}

func (a *Application) syncSystemProxy() {
	enable := a.Cfg.Get("proxy") == "true"
	port := a.Cfg.Get("port")

	if enable {
		slog.Info("系统代理已开启", "端口", port)
	} else {
		slog.Info("系统代理已关闭")
	}

	if err := sys.SetSystemProxy(enable, port); err != nil {
		slog.Error("修改 Windows 系统代理失败", "err", err)
	}
}

func (a *Application) handleProxyStatusChange(status sys.ProxyStatus) {
	if a.State.IsExiting() {
		return
	}

	if time.Since(a.lastProxyModify) < 1500*time.Millisecond {
		return
	}
	
	expectedProxy := a.Cfg.Get("proxy") == "true"
	expectedPort := a.Cfg.Get("port")
	expectedServer := "127.0.0.1:" + expectedPort

	if expectedProxy {
		if status.Enabled {
			if status.Server != "" && !strings.EqualFold(status.Server, expectedServer) {
				slog.Warn("系统代理被外部程序修改，已自动关闭本地代理", "当前接管方", status.Server)
				a.proxyRetryCount = 0
				a.Cfg.Set("proxy", "false")
				a.pushUIState()
				return
			}
			a.proxyRetryCount = 0 
			return
		}

		a.proxyRetryCount++
		if a.proxyRetryCount <= 3 {
			slog.Warn("系统代理被外部程序关闭，尝试自动修复", "重试次数", a.proxyRetryCount)
			
			a.lastProxyModify = time.Now()
			
			go func() {
				time.Sleep(200 * time.Millisecond)
				a.syncSystemProxy()
				
				time.Sleep(1600 * time.Millisecond)
				if cur, err := sys.GetProxyStatus(); err == nil {
					if !cur.Enabled {
						select {
						case a.proxyStatusCh <- cur:
						default:
						}
					}
				}
			}()
		} else {
			slog.Warn("代理自动修复失败，已同步为关闭状态")
			a.proxyRetryCount = 0
			a.Cfg.Set("proxy", "false")
			a.pushUIState()
		}
		return
	}

	if status.Enabled {
		slog.Debug("静默模式下检测到外部开启代理，已忽略", "Server", status.Server)
	}
	a.proxyRetryCount = 0
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
	newState := a.calculateUIState()

	if newState != a.lastUIState {
		slog.Debug("刷新 UI 状态", 
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
	slog.Info("开始重载内核配置")
	a.State.SetReloading(true)
	a.State.SetRestarting(false)
	a.State.MuteAPIWatcher(APIMuteShortPeriod)

	go func() {
		defer a.State.SetReloading(false)

		if _, err := a.Cfg.PrepareYAMLForBoot(); err != nil {
			slog.Error("配置重载前 YAML 检查失败", "err", err)
		}

		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := a.API.DoRequest(reqCtx, "PUT", "/configs?force=true", map[string]interface{}{"path": "", "payload": ""})
		cancel()

		if err != nil {
			slog.Error("重载内核配置失败", "err", err)
			return
		}

		slog.Info("内核配置重载成功，正在同步参数")
		a.syncAllConfig(ctx)
		a.syncSystemProxy()
		
		select {
		case a.apiPollCh <- struct{}{}:
		default:
		}
	}()
}

func (a *Application) RestartKernel() {
	slog.Info("正在结束并重启内核进程")
	a.State.SetRestarting(true)
	a.State.SetReloading(false)
	a.Kernel.HaltDaemon()

	if _, err := a.Cfg.PrepareYAMLForBoot(); err != nil {
		slog.Error("重启前 YAML 检查失败", "err", err)
	}

	a.Kernel.WakeDaemon()

	a.State.MuteAPIWatcher(APIMuteShortPeriod)
	a.pushUIState()
}

func (a *Application) handleTunChange(ctx context.Context) {
	if a.State.IsExiting() {
		return
	}
	tunDev := a.getActualTunDevice()
	alive := sys.IsTunActive(tunDev)

	if a.State.IsTunAlive() != alive {
		slog.Info("虚拟网卡(TUN) 状态变更", "设备", tunDev, "活跃状态", alive)
		if !alive {
			a.State.SetTunLostTime(time.Now())
		}
		if !alive && !a.State.IsAPIWatcherMuted() {
			go func() {
				slog.Debug("TUN 意外断开，尝试快速恢复")
				for i := 0; i < 3; i++ {
					pollCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
					success := a.pollKernelAPI(pollCtx)
					cancel()
					if success {
						slog.Debug("确认 TUN 已恢复响应")
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
				realAlive := sys.IsTunActive(a.getActualTunDevice())
				a.State.SetTunAlive(realAlive)
				select {
				case a.apiPollCh <- struct{}{}:
				default:
				}
			}()
		} else {
			a.State.SetTunAlive(alive)
			a.pushUIState()
		}
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
	
	slog.Debug("同步托盘参数至内核 API", 
		"Tun", tunPayload["enable"], 
		"Dev", a.Cfg.Get("tun_device"), 
		"Mode", payload["mode"])
		
	if err := a.API.SyncConfigToKernel(ctx, payload); err != nil {
		slog.Error("API 参数同步失败", "err", err)
	}
}

func (a *Application) pollKernelAPI(ctx context.Context) bool {
	if a.State.IsAPIWatcherMuted() {
		return false
	}

	queryCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
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
			slog.Info("检测到运行模式(Mode)发生改变，执行同步", "Old", a.Cfg.Get("mode"), "New", resp.Mode)
			a.Cfg.Set("mode", resp.Mode)
			changed = true
		}

		wantTun := a.Cfg.Get("tun") == "true"

		if resp.Tun.Enable && a.State.GetTunStartTime().IsZero() {
			a.State.SetTunStartTime(time.Now())
		} else if !resp.Tun.Enable && !a.State.GetTunStartTime().IsZero() {
			a.State.SetTunStartTime(time.Time{})
		}

		if resp.Tun.Enable != wantTun {
			if wantTun && !resp.Tun.Enable {
				if !a.isTunInGracePeriod() && !a.State.IsTunAlive() {
					slog.Warn("TUN 启动超时失败，已回退本地配置")
					a.Cfg.Set("tun", "false")
					changed = true
				}
			} else {
				slog.Info("检测到 TUN 状态与本地不一致，执行同步", "Expected", wantTun, "Actual", resp.Tun.Enable)
				a.Cfg.Set("tun", fmt.Sprintf("%t", resp.Tun.Enable))
				changed = true
			}
		}

		currentActual := a.getActualTunDevice()
		if resp.Tun.Device != "" && resp.Tun.Device != currentActual {
			slog.Debug("TUN 网卡硬件标识符发生更新", "NewDev", resp.Tun.Device)
			a.setActualTunDevice(resp.Tun.Device)
			changed = true
		}

		if changed {
			realAlive := sys.IsTunActive(a.getActualTunDevice())
			if a.State.IsTunAlive() != realAlive {
				slog.Debug("同步 TUN 虚拟网卡实际活跃状态", "Alive", realAlive)
				a.State.SetTunAlive(realAlive)
			}
		}

		return changed
	}
	return false
}
