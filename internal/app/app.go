package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"time"

	"mihomo-tray/internal/core"
	"mihomo-tray/internal/fsm"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/ui"
)

const (
	IconStop = iota
	IconError
	IconTun
	IconProxy
	IconDefault
)

type Application struct {
	Cfg    *fsm.Manager
	Kernel *core.KernelManager
	API    *core.APIClient

	kernelEventCh chan core.KernelEvent
	tunEventCh    chan struct{}
	proxyEventCh  chan bool
	apiPollCh     chan struct{}

	UIStateCh   chan ui.UIState
	UICommandCh chan ui.UICommand
	webuiEventCh chan ui.Event

	lastUIState ui.UIState
}

func NewApplication(cm *fsm.Manager) *Application {
	return &Application{
		Cfg:           cm,
		Kernel:        core.NewKernelManager(cm),
		API:           core.NewAPIClient(cm),
		kernelEventCh: make(chan core.KernelEvent, 1),
		tunEventCh:    make(chan struct{}, 1),
		proxyEventCh:  make(chan bool, 1),
		apiPollCh:     make(chan struct{}, 1),
		UIStateCh:     make(chan ui.UIState, 1),
		UICommandCh:   make(chan ui.UICommand, 10),
		webuiEventCh:  make(chan ui.Event, 1),
	}
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

	a.Cfg.SyncWithYAML()
	log.Println("[INFO] 配置文件同步 (SyncWithYAML) 完成")
	a.Cfg.State.MuteAPIWatcher(5 * time.Second)

	if a.Cfg.Get("proxy") == "true" {
		port := a.Cfg.Get("port")
		log.Printf("[INFO] 检测到 Proxy 选项开启，正在启用系统代理 (端口: %s)...", port)
		if err := sys.EnableSystemProxy(port); err != nil {
			log.Printf("[ERROR] 启用系统代理失败: %v", err)
		} else {
			log.Println("[INFO] 系统代理启用成功")
		}
	}

	a.pushUIState()

	log.Println("[INFO] 启动核心守护协程 (Daemon)...")
	go a.Kernel.RunDaemon(ctx, a.kernelEventCh)
	go sys.WatchNetworkInterfaces(ctx, a.tunEventCh)
	go a.watchProxyAdapter(ctx)
	go a.eventLoop(ctx)
}

func (a *Application) SafeShutdown(cancel context.CancelFunc) {
	log.Println("[INFO] 正在触发安全退出机制 (SafeShutdown)...")
	a.Cfg.State.ForceExitPhase()

	log.Println("[INFO] 停止 TUN 网卡模式...")
	a.gracefulStopTUN()

	if cancel != nil {
		cancel()
	}

	if a.Cfg.Get("proxy") == "true" {
		log.Println("[INFO] 正在关闭系统代理...")
		if err := sys.DisableSystemProxy(); err != nil {
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
		if a.Cfg.State.GetPhase() == fsm.PhaseRunning && !a.Cfg.State.IsAPIWatcherMuted() {
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
				a.Cfg.State.SetPhase(fsm.PhaseRunning)
				a.Cfg.State.MuteAPIWatcher(5 * time.Second)
				if a.Cfg.Get("tun") == "true" {
					a.Cfg.State.SetTunRequestedTime(time.Now())
				}

				go func() {
					defer a.Cfg.State.SetRestarting(false)
					log.Println("[INFO] 开始轮询内核 API 校验可连接性...")
					for i := 0; i < 20; i++ {
						pollCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
						_, err := a.API.DoRequest(pollCtx, "GET", "/configs", nil)
						cancel()
						if err == nil {
							log.Printf("[INFO] 内核 API 连接成功 (重试第 %d 次)，同步配置...", i+1)
							a.syncAllConfig(ctx)

							if a.pollKernelAPI(ctx) {
								a.pushUIState()
							}

							select {
							case a.apiPollCh <- struct{}{}:
							default:
							}
							return
						}
						time.Sleep(250 * time.Millisecond)
					}
					log.Println("[ERROR] 内核 API 校验超时 (尝试 20 次未连通)")
				}()
			} else if event == core.EventKernelExit {
				log.Println("[WARN] 内核异常退出 (EventKernelExit)，重置阶段为 Initializing")
				a.Cfg.State.SetPhase(fsm.PhaseInitializing)
			}
			a.pushUIState()

		case <-a.tunEventCh:
			log.Println("[DEBUG] 网络接口变更通知 (tunEventCh)")
			a.handleTunChange(ctx)

		case isProxyActive := <-a.proxyEventCh:
			log.Printf("[INFO] 系统代理状态变更通知: active=%v", isProxyActive)
			if !isProxyActive {
				a.Cfg.Set("proxy", "false")
			} else {
				_ = sys.EnableSystemProxy(a.Cfg.Get("port"))
			}
			a.pushUIState()

		case <-ticker.C:
			tryPollAPI()
			a.pushUIState()

		case <-a.apiPollCh:
			tryPollAPI()
		}
	}
}

func (a *Application) handleUICommand(ctx context.Context, cmd ui.UICommand) {
	switch cmd.Action {
	case "OpenWebUI":
		if a.Cfg.State.GetPhase() != fsm.PhaseRunning {
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
		if enable {
			_ = sys.EnableSystemProxy(a.Cfg.Get("port"))
		} else {
			_ = sys.DisableSystemProxy()
		}
	case "ToggleTun":
		enable := cmd.Payload == "true"
		log.Printf("[INFO] 切换 TUN 模式开关: %v", enable)
		a.Cfg.Set("tun", strconv.FormatBool(enable))
		if enable {
			a.Cfg.State.SetTunRequestedTime(time.Now())
		}
		a.Cfg.State.MuteAPIWatcher(3 * time.Second)
		go a.API.SyncConfigToKernel(ctx, map[string]interface{}{"tun": map[string]bool{"enable": enable}})
	case "SwitchMode":
		log.Printf("[INFO] 切换运行模式: %s", cmd.Payload)
		a.Cfg.Set("mode", cmd.Payload)
		a.Cfg.State.MuteAPIWatcher(2 * time.Second)
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

	if a.Cfg.State.GetPhase() != fsm.PhaseRunning || a.Cfg.State.IsRestarting() {
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

	if a.Cfg.State.IsReloading() {
		if !a.Cfg.State.IsTunAlive() {
			s.IconState = IconError
		} else {
			s.IconState = IconTun
		}
		return s
	}

	if a.Cfg.State.IsTunAlive() ||
		time.Since(a.Cfg.State.GetTunStartTime()) < 5*time.Second ||
		time.Since(a.Cfg.State.GetTunRequestedTime()) < 5*time.Second ||
		time.Since(a.Cfg.State.GetTunLostTime()) < 6*time.Second {
		s.IconState = IconTun
	} else {
		s.IconState = IconError
	}

	return s
}

func (a *Application) pushUIState() {
	if a.Cfg.State.IsExiting() {
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
	a.Cfg.State.SetReloading(true)
	a.Cfg.State.SetRestarting(false)
	a.Cfg.State.MuteAPIWatcher(5 * time.Second)

	oldPort := a.Cfg.Get("port")
	isProxyOn := a.Cfg.Get("proxy") == "true"

	go func() {
		defer a.Cfg.State.SetReloading(false)
		a.Cfg.SyncWithYAML()
		portChanged := oldPort != "" && oldPort != a.Cfg.Get("port")

		if portChanged && isProxyOn {
			log.Printf("[INFO] 检查到端口变更 (%s -> %s)，临时关闭系统代理", oldPort, a.Cfg.Get("port"))
			a.Cfg.Set("proxy", "false")
			_ = sys.DisableSystemProxy()
			a.pushUIState()
		}

		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := a.API.DoRequest(reqCtx, "PUT", "/configs?force=true", map[string]interface{}{"path": "", "payload": ""})
		cancel()

		if err != nil {
			log.Printf("[ERROR] 内核加载新配置失败: %v", err)
			if portChanged && isProxyOn {
				log.Println("[WARN] 回滚系统代理状态...")
				_ = sys.EnableSystemProxy(a.Cfg.Get("port"))
				a.Cfg.Set("proxy", "true")
				a.pushUIState()
			}
			return
		}

		log.Println("[INFO] 内核热重载成功，开始同步各模块配置...")
		a.syncAllConfig(ctx)

		if portChanged && isProxyOn {
			time.Sleep(500 * time.Millisecond)
			log.Printf("[INFO] 重新应用系统代理到新端口 %s", a.Cfg.Get("port"))
			_ = sys.EnableSystemProxy(a.Cfg.Get("port"))
			a.Cfg.Set("proxy", "true")
		}

		a.pushUIState()
		select {
		case a.apiPollCh <- struct{}{}:
		default:
		}
	}()
}

func (a *Application) RestartKernel() {
	log.Println("[WARN] 执行内核重启操作 (RestartKernel)...")
	a.Cfg.State.SetRestarting(true)
	a.Cfg.State.SetReloading(false)
	a.Cfg.State.MuteAPIWatcher(5 * time.Second)
	a.Cfg.SyncWithYAML()

	a.gracefulStopTUN()

	log.Println("[INFO] 强制终止当前内核进程...")
	a.Kernel.KillCurrent()
	a.pushUIState()
}

func (a *Application) handleTunChange(ctx context.Context) {
	if a.Cfg.State.IsExiting() {
		return
	}
	tunDev := a.Cfg.Get("tun_device")
	alive := sys.IsTunActive(tunDev)

	if a.Cfg.State.IsTunAlive() != alive {
		log.Printf("[INFO] TUN 虚拟网卡状态发生变更: 设备=%s, 活跃状态=%v", tunDev, alive)
		if !alive {
			a.Cfg.State.SetTunLostTime(time.Now())
		}
		if !alive && !a.Cfg.State.IsAPIWatcherMuted() {
			go func() {
				log.Println("[DEBUG] 检测到 TUN 断开，开始尝试快速轮询确认...")
				for i := 0; i < 3; i++ {
					pollCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
					success := a.pollKernelAPI(pollCtx)
					cancel()
					if success {
						log.Println("[DEBUG] TUN 断开检查中重连成功")
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
				a.Cfg.State.SetTunAlive(alive)
				select {
				case a.apiPollCh <- struct{}{}:
				default:
				}
			}()
		} else {
			a.Cfg.State.SetTunAlive(alive)
			a.pushUIState()
		}
	}
}

func (a *Application) watchProxyAdapter(ctx context.Context) {
	log.Println("[INFO] 系统代理监听协程已启动")
	sys.WatchProxyRegistry(ctx,
		func() bool { return a.Cfg.Get("proxy") == "true" },
		func() string { return a.Cfg.Get("port") },
		func() {
			log.Println("[WARN] 注册表通知: 系统代理在外部被关闭")
			select {
			case a.proxyEventCh <- false:
			case <-ctx.Done():
			}
		},
		func() {
			log.Println("[INFO] 注册表通知: 系统代理在外部被开启")
			select {
			case a.proxyEventCh <- true:
			case <-ctx.Done():
			}
		},
	)
}

func (a *Application) syncAllConfig(ctx context.Context) {
	if a.Cfg.State.GetPhase() != fsm.PhaseRunning {
		return
	}
	payload := map[string]interface{}{
		"tun":  map[string]bool{"enable": a.Cfg.Get("tun") == "true"},
		"mode": a.Cfg.Get("mode"),
	}
	log.Printf("[DEBUG] 向内核同步运行参数: tun.enable=%v, mode=%s", payload["tun"], payload["mode"])
	if err := a.API.SyncConfigToKernel(ctx, payload); err != nil {
		log.Printf("[ERROR] 同步参数到内核失败: %v", err)
	}
}

func (a *Application) pollKernelAPI(ctx context.Context) bool {
	queryCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()

	body, err := a.API.DoRequest(queryCtx, "GET", "/configs", nil)
	if err != nil {
		return false
	}

	var resp struct {
		Mode string `json:"mode"`
		Tun  struct {
			Enable bool `json:"enable"`
		} `json:"tun"`
	}
	if json.Unmarshal(body, &resp) == nil {
		changed := false
		if resp.Mode != "" && resp.Mode != a.Cfg.Get("mode") {
			log.Printf("[INFO] 内核返回 Mode 变更: %s -> %s", a.Cfg.Get("mode"), resp.Mode)
			a.Cfg.Set("mode", resp.Mode)
			changed = true
		}
		if resp.Tun.Enable != (a.Cfg.Get("tun") == "true") {
			log.Printf("[INFO] 内核返回 Tun.Enable 变更: %v -> %v", a.Cfg.Get("tun") == "true", resp.Tun.Enable)
			a.Cfg.Set("tun", fmt.Sprintf("%t", resp.Tun.Enable))
			changed = true
		}
		return changed
	}
	return false
}

func (a *Application) gracefulStopTUN() {
	if a.Cfg.Get("tun") != "true" {
		return
	}

	log.Println("[INFO] 开始平滑关闭 TUN 网卡...")
	stopCtx, cancel := context.WithTimeout(context.Background(), 1000*time.Millisecond)
	defer cancel()

	payload := map[string]interface{}{
		"tun": map[string]bool{"enable": false},
	}
	_ = a.API.SyncConfigToKernel(stopCtx, payload)

	tunDevice := a.Cfg.Get("tun_device")
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	timeout := time.After(2000 * time.Millisecond)

	for {
		select {
		case <-ticker.C:
			if !sys.IsTunActive(tunDevice) {
				log.Println("[INFO] TUN 网卡成功安全解绑关闭")
				return
			}
		case <-timeout:
			log.Println("[WARN] 等待 TUN 网卡关闭超时，强制继续操作")
			return
		}
	}
}
