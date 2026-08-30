package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

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
	log.Println("[INFO] 正在启动应用 Bootstrap...")

	a.Cfg.EnsureDefault()

	osTaskExists := sys.CheckAutoStartStatus()
	cfgMemoryStatus := a.Cfg.Get("autostart") == "true"
	log.Printf("[INFO] 自启动状态检查: 系统任务存在=%v, 配置项记录=%v", osTaskExists, cfgMemoryStatus)

	if osTaskExists {
		if !sys.IsTaskPathValid(a.Cfg.ExePath()) {
			log.Println("[WARN] 检测到开机自启动任务路径无效，正在尝试修复...")
			if cfgMemoryStatus {
				sys.ToggleAutoStart(a.Cfg.ExePath(), a.Cfg.BaseDir(), true)
				osTaskExists = true
				log.Println("[INFO] 开机自启动任务已更正为当前程序路径")
			} else {
				sys.ToggleAutoStart(a.Cfg.ExePath(), a.Cfg.BaseDir(), false)
				osTaskExists = false
				log.Println("[INFO] 清除无效的开机自启动任务")
			}
		}
	}

	if osTaskExists != cfgMemoryStatus {
		log.Printf("[INFO] 更新内存配置项 autostart=%v", osTaskExists)
		a.Cfg.Set("autostart", strconv.FormatBool(osTaskExists))
	}

	if modified, err := a.Cfg.PrepareYAMLForBoot(); err != nil {
		log.Printf("[ERROR] 启动前准备 YAML 配置失败: %v", err)
	} else if modified {
		log.Println("[INFO] 成功校准 config.yaml 预置状态并完成配置同步")
	} else {
		log.Println("[DEBUG] YAML 配置与内存目标状态一致，未触发重写")
	}

	a.State.MuteAPIWatcher(TunInitGracePeriod)
	if a.Cfg.Get("tun") == "true" {
		a.State.SetTunRequestedTime(time.Now())
	}

	a.syncSystemProxy()
	a.pushUIState()

	log.Println("[INFO] 启动核心守护协程 (Daemon)...")
	go a.Kernel.RunDaemon(ctx, a.kernelEventCh)
	go sys.WatchNetworkInterfaces(ctx, a.tunEventCh)
	go sys.WatchProxyRegistry(ctx, a.proxyStatusCh)
	go a.eventLoop(ctx)
}

func (a *Application) SafeShutdown(cancel context.CancelFunc) {
	log.Println("[INFO] 正在触发安全退出机制 (SafeShutdown)...")

	a.State.ForceExitPhase()

	if cancel != nil {
		cancel()
	}

	log.Println("[INFO] 正在优雅关停内核进程...")
	a.Kernel.KillCurrent()

	if a.Cfg.Get("proxy") == "true" {
		log.Println("[INFO] 正在关闭系统代理...")
		if err := sys.SetSystemProxy(false, ""); err != nil {
			log.Printf("[ERROR] 关闭系统代理失败: %v", err)
		}
	}

	log.Println("[INFO] 关闭内核管理接口...")
	a.Kernel.Close()

	log.Println("[INFO] ==================== 应用已安全关闭 ====================")
}

func (a *Application) eventLoop(ctx context.Context) {
	log.Println("[INFO] 主事件循环 (eventLoop) 已开启")
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	tryPollAPI := func() {
		if a.State.GetPhase() == state.PhaseRunning && !a.State.IsAPIWatcherMuted() {
			if a.pollKernelAPI(ctx) {
				log.Println("[DEBUG] 轮询内核 API 触发状态更新，推送 UI State")
				a.pushUIState()
			}
		}
	}

	for {
		select {
		case event := <-a.webuiEventCh:
			log.Printf("[WARN] 收到 WebUI 事件: %v", event)
			if event == ui.EventError {
				log.Println("[ERROR] WebUI 发生错误事件")
			}
		case <-ctx.Done():
			log.Println("[INFO] 上下文 Context 取消，退出事件循环")
			return

		case cmd := <-a.UICommandCh:
			log.Printf("[INFO] 收到 UI 指令: Action=%s, Payload=%s", cmd.Action, cmd.Payload)
			a.handleUICommand(ctx, cmd)

		case event := <-a.kernelEventCh:
			log.Printf("[INFO] 收到内核事件: %v", event)
			if event == core.EventKernelReady {
				log.Println("[INFO] 内核就绪 (EventKernelReady)，设置阶段为 Running")
				
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
					log.Println("[INFO] 开始轮询内核 API 校验可连接性...")
					for i := 0; i < 20; i++ {
						pollCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
						_, err := a.API.DoRequest(pollCtx, "GET", "/configs", nil)
						cancel()
						if err == nil {
							log.Printf("[INFO] 内核 API 连接成功 (重试第 %d 次)", i+1)
							a.pushUIState()
							select {
							case a.apiPollCh <- struct{}{}:
							default:
							}
							return
						}
						time.Sleep(250 * time.Millisecond)
					}
					log.Println("[ERROR] 内核 API 校验超时 (尝试 20 次未连通)")
					a.pushUIState()
				}()
			} else if event == core.EventKernelExit {
				if a.State.IsRestarting() {
					log.Println("[INFO] 内核已停止，等待重新拉起...")
				} else {
					log.Println("[WARN] 内核异常退出 (EventKernelExit)，重置阶段为 Initializing")
				}
				a.State.SetPhase(state.PhaseInitializing)
			}
			a.pushUIState()

		case <-a.tunEventCh:
			log.Println("[DEBUG] 网络接口变更通知 (tunEventCh)")
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

func (a *Application) syncSystemProxy() {
	enable := a.Cfg.Get("proxy") == "true"
	port := a.Cfg.Get("port")

	if enable {
		log.Printf("[INFO] 正在同步系统代理状态: 开启 (端口: %s)", port)
	} else {
		log.Println("[INFO] 正在同步系统代理状态: 关闭")
	}

	if err := sys.SetSystemProxy(enable, port); err != nil {
		log.Printf("[ERROR] 同步系统代理设置失败: %v", err)
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
				log.Printf("[WARN] 检测到系统代理被外部修改: 期望=%s, 实际=%s，自动关闭本地代理标记", expectedServer, status.Server)
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
			log.Printf("[WARN] 系统代理在外部被关闭，异步尝试自动重新应用 (第 %d/3 次)...", a.proxyRetryCount)
			go func() {       
				time.Sleep(200 * time.Millisecond)
				a.syncSystemProxy()
			}()
		} else {
			log.Println("[WARN] 外部关闭代理重试超过 3 次，放弃纠偏并同步本地状态为关闭")
			a.proxyRetryCount = 0
			a.Cfg.Set("proxy", "false")
			a.pushUIState()
		}
		return
	}

	if status.Enabled {
		log.Printf("[INFO] 检测到外部工具开启了系统代理 (Server: %s)，本地保持未开启状态", status.Server)
	}
	a.proxyRetryCount = 0
}

func (a *Application) handleUICommand(ctx context.Context, cmd ui.UICommand) {
	switch cmd.Action {
	case "OpenWebUI":
		if a.State.GetPhase() != state.PhaseRunning {
			log.Println("[WARN] 内核未处于 Running 状态，拒绝打开 WebUI")
			break
		}
		cfg := ui.Config{
			APIAddr:   a.Cfg.Get("external-controller"),
			Secret:    a.Cfg.Get("secret"),
			ProxyPort: a.Cfg.Get("port"),
			BaseDir:   a.Cfg.BaseDir(),
		}
		log.Printf("[INFO] 正在启动 WebUI (地址: %s)...", cfg.APIAddr)
		go ui.Launch(cfg, a.webuiEventCh)
	case "ExitApp":
		log.Println("[INFO] UI 请求退出程序")
		ui.Cleanup()
	case "ToggleProxy":
		enable := cmd.Payload == "true"
		log.Printf("[INFO] 切换系统代理开关: %v", enable)
		a.Cfg.Set("proxy", strconv.FormatBool(enable))
		a.syncSystemProxy()
	case "ToggleTun":
		enable := cmd.Payload == "true"
		log.Printf("[INFO] 切换 TUN 模式开关: %v (设备: %s)", enable, a.Cfg.Get("tun_device"))
		a.Cfg.Set("tun", strconv.FormatBool(enable))
		if enable {
			a.State.SetTunRequestedTime(time.Now())
			a.setActualTunDevice(a.Cfg.Get("tun_device"))
		}
		a.State.MuteAPIWatcher(APIMuteShortPeriod)
		
		tunPayload := map[string]interface{}{
			"enable": enable,
		}
		if dev := a.Cfg.Get("tun_device"); dev != "" {
			tunPayload["device"] = dev
		}
		go a.API.SyncConfigToKernel(ctx, map[string]interface{}{"tun": tunPayload})
		
	case "SwitchMode":
		log.Printf("[INFO] 切换运行模式: %s", cmd.Payload)
		a.Cfg.Set("mode", cmd.Payload)
		a.State.MuteAPIWatcher(3 * time.Second)
		go a.syncAllConfig(ctx)

	case "ToggleAutoStart":
		enable := cmd.Payload == "true"
		log.Printf("[INFO] 设置开机自启: %v", enable)
		a.Cfg.Set("autostart", cmd.Payload)
		sys.ToggleAutoStart(a.Cfg.ExePath(), a.Cfg.BaseDir(), enable)
	case "OpenBaseDir":
		baseDir := a.Cfg.BaseDir()
		log.Printf("[INFO] 打开工作目录: %s", baseDir)
		_ = sys.ExecuteSystemCommand(baseDir)
	case "ReloadConfig":
		log.Println("[INFO] 触发手动重载配置")
		a.ReloadConfig(ctx)
	case "RestartKernel":
		log.Println("[WARN] 触发重启内核")
		a.RestartKernel()
	case "OpenConfigFile":
		configPath := filepath.Join(a.Cfg.BaseDir(), "config.yaml")
		log.Printf("[INFO] 打开配置文件: %s", configPath)
		_ = sys.ExecuteSystemCommand(configPath)
	}

	a.pushUIState()
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
		log.Printf("[DEBUG] UI 图标状态更新: IconState=%d, Tun=%v, Proxy=%v, Mode=%s",
			newState.IconState, newState.IsTun, newState.IsProxy, newState.Mode)
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
	log.Println("[INFO] 开始执行重载配置逻辑 (ReloadConfig)...")
	a.State.SetReloading(true)
	a.State.SetRestarting(false)
	a.State.MuteAPIWatcher(APIMuteShortPeriod)

	go func() {
		defer a.State.SetReloading(false)

		if _, err := a.Cfg.PrepareYAMLForBoot(); err != nil {
			log.Printf("[ERROR] 重载配置时同步 YAML 状态失败: %v", err)
		}

		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := a.API.DoRequest(reqCtx, "PUT", "/configs?force=true", map[string]interface{}{"path": "", "payload": ""})
		cancel()

		if err != nil {
			log.Printf("[ERROR] 内核加载新配置失败: %v", err)
			return
		}

		log.Println("[INFO] 内核热重载成功，开始同步各模块配置...")
		a.syncAllConfig(ctx)

		a.syncSystemProxy()

		a.pushUIState()
		select {
		case a.apiPollCh <- struct{}{}:
		default:
		}
	}()
}

func (a *Application) RestartKernel() {
	log.Println("[WARN] 执行内核重启操作 (RestartKernel)...")
	a.State.SetRestarting(true)
	a.State.SetReloading(false)

	if _, err := a.Cfg.PrepareYAMLForBoot(); err != nil {
		log.Printf("[ERROR] 重启内核前校准 YAML 失败: %v", err)
	}

	log.Println("[INFO] 终止当前内核进程...")
	a.Kernel.KillCurrent()

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
		log.Printf("[INFO] TUN 虚拟网卡状态发生变更: 设备=%s, 活跃状态=%v", tunDev, alive)
		if !alive {
			a.State.SetTunLostTime(time.Now())
		}
		if !alive && !a.State.IsAPIWatcherMuted() {
			go func() {
				log.Println("[DEBUG] 检测到 TUN 断开，开始尝试快速轮询确认...")
				for i := 0; i < 3; i++ {
					pollCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
					success := a.pollKernelAPI(pollCtx)
					cancel()
					if success {
						log.Println("[DEBUG] TUN 断开检查中 API 重连成功")
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
	log.Printf("[DEBUG] 向内核同步运行参数: tun.enable=%v, tun.device=%s, mode=%s",
		tunPayload["enable"], a.Cfg.Get("tun_device"), payload["mode"])
	if err := a.API.SyncConfigToKernel(ctx, payload); err != nil {
		log.Printf("[ERROR] 同步参数到内核失败: %v", err)
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
			log.Printf("[INFO] 内核返回 Mode 变更: %s -> %s", a.Cfg.Get("mode"), resp.Mode)
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
					log.Printf("[WARN] TUN 确认启动失败 (超时且网卡未激活)，同步本地 Tun.Enable: false")
					a.Cfg.Set("tun", "false")
					
					if a.Cfg.Get("proxy") != "true" {
						log.Printf("[INFO] 触发网络容灾机制：自动回退开启系统代理接管流量")
						a.Cfg.Set("proxy", "true")
						a.syncSystemProxy()
					}
					changed = true
				} else {
					log.Printf("[DEBUG] 忽略内核 TUN 初始化过渡期状态 (resp.Enable=false, wantTun=true)")
				}
			} else {
				log.Printf("[INFO] 内核返回 Tun.Enable 变更: %v -> %v", wantTun, resp.Tun.Enable)
				a.Cfg.Set("tun", fmt.Sprintf("%t", resp.Tun.Enable))
				changed = true
			}
		}

		currentActual := a.getActualTunDevice()
		if resp.Tun.Device != "" && resp.Tun.Device != currentActual {
			log.Printf("[INFO] 内核返回 Tun.Device 变更 (运行时变动): %s -> %s", currentActual, resp.Tun.Device)
			a.setActualTunDevice(resp.Tun.Device)
			changed = true
		}

		if changed {
			realAlive := sys.IsTunActive(a.getActualTunDevice())
			if a.State.IsTunAlive() != realAlive {
				log.Printf("[DEBUG] 轮询纠偏：及时同步底层网卡真实存活状态 -> %v", realAlive)
				a.State.SetTunAlive(realAlive)
			}
		}

		return changed
	}
	return false
}
