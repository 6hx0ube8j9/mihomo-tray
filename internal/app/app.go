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
	log.Println("[INFO] 启动应用...")

	a.Cfg.EnsureDefault()

	osTaskExists := sys.CheckAutoStartStatus()
	cfgMemoryStatus := a.Cfg.Get("autostart") == "true"
	log.Printf("[INFO] 自启检查: 任务=%v, 配置=%v", osTaskExists, cfgMemoryStatus)

	if osTaskExists {
		if !sys.IsTaskPathValid(a.Cfg.ExePath()) {
			log.Println("[WARN] 修复无效自启任务...")
			if cfgMemoryStatus {
				sys.ToggleAutoStart(a.Cfg.ExePath(), a.Cfg.BaseDir(), true)
				osTaskExists = true
				log.Println("[INFO] 自启任务路径已更新")
			} else {
				sys.ToggleAutoStart(a.Cfg.ExePath(), a.Cfg.BaseDir(), false)
				osTaskExists = false
				log.Println("[INFO] 已清除无效自启任务")
			}
		}
	}

	if osTaskExists != cfgMemoryStatus {
		log.Printf("[INFO] 更新自启配置: %v", osTaskExists)
		a.Cfg.Set("autostart", strconv.FormatBool(osTaskExists))
	}

	if modified, err := a.Cfg.PrepareYAMLForBoot(); err != nil {
		log.Printf("[ERROR] YAML 预处理失败: %v", err)
	} else if modified {
		log.Println("[INFO] YAML 配置校准同步完成")
	} else {
		log.Println("[DEBUG] YAML 配置一致，无需重写")
	}

	a.State.MuteAPIWatcher(TunInitGracePeriod)
	if a.Cfg.Get("tun") == "true" {
		a.State.SetTunRequestedTime(time.Now())
	}

	a.syncSystemProxy()
	a.pushUIState()

	log.Println("[INFO] 启动守护协程...")
	go a.Kernel.RunDaemon(ctx, a.kernelEventCh)
	go sys.WatchNetworkInterfaces(ctx, a.tunEventCh)
	go sys.WatchProxyRegistry(ctx, a.proxyStatusCh)
	go a.eventLoop(ctx)
}

func (a *Application) SafeShutdown(cancel context.CancelFunc) {
	log.Println("[INFO] 执行安全退出...")

	a.State.ForceExitPhase()

	if cancel != nil {
		cancel()
	}

	log.Println("[INFO] 关停内核...")
	a.Kernel.KillCurrent()

	if a.Cfg.Get("proxy") == "true" {
		log.Println("[INFO] 关闭系统代理...")
		if err := sys.SetSystemProxy(false, ""); err != nil {
			log.Printf("[ERROR] 关闭代理失败: %v", err)
		}
	}

	log.Println("[INFO] 关闭内核接口...")
	a.Kernel.Close()

	log.Println("[INFO] === 应用已安全关闭 ===")
}

func (a *Application) eventLoop(ctx context.Context) {
	log.Println("[INFO] 事件循环已开启")
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	tryPollAPI := func() {
		if a.State.GetPhase() == state.PhaseRunning && !a.State.IsAPIWatcherMuted() {
			if a.pollKernelAPI(ctx) {
				log.Println("[DEBUG] API 状态更新，推送 UI")
				a.pushUIState()
			}
		}
	}

	for {
		select {
		case event := <-a.webuiEventCh:
			log.Printf("[WARN] WebUI 事件: %v", event)
			if event == ui.EventError {
				log.Println("[ERROR] WebUI 错误")
			}
		case <-ctx.Done():
			log.Println("[INFO] 退出事件循环")
			return

		case cmd := <-a.UICommandCh:
			log.Printf("[INFO] UI 指令: %s (参数: %s)", cmd.Action, cmd.Payload)
			a.handleUICommand(ctx, cmd)

		case event := <-a.kernelEventCh:
			log.Printf("[INFO] 内核事件: %v", event)
			if event == core.EventKernelReady {
				log.Println("[INFO] 内核就绪，进入 Running 阶段")

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
					log.Println("[INFO] 轮询内核 API...")
					
					for i := 0; i < 60; i++ {
						pollCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
						_, err := a.API.DoRequest(pollCtx, "GET", "/configs", nil)
						cancel()
						if err == nil {
							log.Printf("[INFO] 内核 API 连通 (重试 %d)", i+1)
							a.pushUIState()
							select {
							case a.apiPollCh <- struct{}{}:
							default:
							}
							return
						}
						time.Sleep(250 * time.Millisecond)
					}
					log.Println("[ERROR] 内核 API 连接超时 (尝试 60 次)")
					a.Kernel.HaltDaemon()
					a.State.SetPhase(state.PhaseInitializing)
					a.pushUIState()
				}()
			} else if event == core.EventKernelExit {
				if a.State.IsRestarting() {
					log.Println("[INFO] 内核已停止，等待重启...")
				} else {
					log.Println("[WARN] 内核异常退出，退回 Initializing 阶段")
				}
				a.State.SetPhase(state.PhaseInitializing)
			}
			a.pushUIState()

		case <-a.tunEventCh:
			log.Println("[DEBUG] TUN 接口变更")
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
		log.Printf("[INFO] 系统代理: 开启 (端口 %s)", port)
	} else {
		log.Println("[INFO] 系统代理: 关闭")
	}

	if err := sys.SetSystemProxy(enable, port); err != nil {
		log.Printf("[ERROR] 设置系统代理失败: %v", err)
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
				log.Printf("[WARN] 代理被外部修改 (期望 %s, 实际 %s)，关闭本地代理", expectedServer, status.Server)
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
			log.Printf("[WARN] 外部关闭代理，尝试恢复 (%d/3)", a.proxyRetryCount)
			go func() {
				time.Sleep(200 * time.Millisecond)
				a.syncSystemProxy()
			}()
		} else {
			log.Println("[WARN] 代理恢复失败，同步关闭本地状态")
			a.proxyRetryCount = 0
			a.Cfg.Set("proxy", "false")
			a.pushUIState()
		}
		return
	}

	if status.Enabled {
		log.Printf("[INFO] 外部工具开启代理 (%s)，忽略", status.Server)
	}
	a.proxyRetryCount = 0
}

func (a *Application) handleUICommand(ctx context.Context, cmd ui.UICommand) {
	switch cmd.Action {
	case "OpenWebUI":
		if a.State.GetPhase() != state.PhaseRunning {
			log.Println("[WARN] 内核未运行，拒绝打开 WebUI")
			break
		}
		cfg := ui.Config{
			APIAddr:   a.Cfg.Get("external-controller"),
			Secret:    a.Cfg.Get("secret"),
			ProxyPort: a.Cfg.Get("port"),
			BaseDir:   a.Cfg.BaseDir(),
			UIName:    a.Cfg.Get("external-ui-name"),
		}
		log.Printf("[INFO] 启动 WebUI (%s)...", cfg.APIAddr)
		go ui.Launch(cfg, a.webuiEventCh)
	case "ExitApp":
		log.Println("[INFO] 请求退出程序")
		ui.Cleanup()
	case "ToggleProxy":
		enable := cmd.Payload == "true"
		log.Printf("[INFO] 代理开关: %v", enable)
		a.Cfg.Set("proxy", strconv.FormatBool(enable))
		a.lastProxyModify = time.Now()
		a.syncSystemProxy()
	case "ToggleTun":
		enable := cmd.Payload == "true"
		log.Printf("[INFO] TUN 开关: %v (%s)", enable, a.Cfg.Get("tun_device"))
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
		log.Printf("[INFO] 运行模式: %s", cmd.Payload)
		a.Cfg.Set("mode", cmd.Payload)
		a.State.MuteAPIWatcher(3 * time.Second)
		go a.syncAllConfig(ctx)

	case "ToggleAutoStart":
		enable := cmd.Payload == "true"
		log.Printf("[INFO] 开机自启: %v", enable)
		a.Cfg.Set("autostart", cmd.Payload)
		sys.ToggleAutoStart(a.Cfg.ExePath(), a.Cfg.BaseDir(), enable)
	case "OpenBaseDir":
		baseDir := a.Cfg.BaseDir()
		log.Printf("[INFO] 打开目录: %s", baseDir)
		_ = sys.ExecuteSystemCommand(baseDir)
	case "ReloadConfig":
		log.Println("[INFO] 手动重载配置")
		a.ReloadConfig(ctx)
	case "RestartKernel":
		log.Println("[WARN] 重启内核")
		a.RestartKernel()
	case "OpenConfigFile":
		configPath := filepath.Join(a.Cfg.BaseDir(), "config.yaml")
		log.Printf("[INFO] 打开配置: %s", configPath)
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
		log.Printf("[DEBUG] UI 状态: Icon=%d, Tun=%v, Proxy=%v, Mode=%s",
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
	log.Println("[INFO] 执行重载配置...")
	a.State.SetReloading(true)
	a.State.SetRestarting(false)
	a.State.MuteAPIWatcher(APIMuteShortPeriod)

	go func() {
		defer a.State.SetReloading(false)

		if _, err := a.Cfg.PrepareYAMLForBoot(); err != nil {
			log.Printf("[ERROR] 重载 YAML 失败: %v", err)
		}

		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := a.API.DoRequest(reqCtx, "PUT", "/configs?force=true", map[string]interface{}{"path": "", "payload": ""})
		cancel()

		if err != nil {
			log.Printf("[ERROR] 加载新配置失败: %v", err)
			return
		}

		log.Println("[INFO] 热重载成功，同步配置...")
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
	log.Println("[WARN] 执行内核重启...")
	a.State.SetRestarting(true)
	a.State.SetReloading(false)
	a.Kernel.HaltDaemon()

	if _, err := a.Cfg.PrepareYAMLForBoot(); err != nil {
		log.Printf("[ERROR] 重启前校准 YAML 失败: %v", err)
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
		log.Printf("[INFO] TUN 状态变更: %s, 活跃=%v", tunDev, alive)
		if !alive {
			a.State.SetTunLostTime(time.Now())
		}
		if !alive && !a.State.IsAPIWatcherMuted() {
			go func() {
				log.Println("[DEBUG] TUN 断开，快速确认...")
				for i := 0; i < 3; i++ {
					pollCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
					success := a.pollKernelAPI(pollCtx)
					cancel()
					if success {
						log.Println("[DEBUG] TUN 快速确认 API 连通")
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
	log.Printf("[DEBUG] 同步内核参数: tun=%v, dev=%s, mode=%s",
		tunPayload["enable"], a.Cfg.Get("tun_device"), payload["mode"])
	if err := a.API.SyncConfigToKernel(ctx, payload); err != nil {
		log.Printf("[ERROR] 参数同步失败: %v", err)
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
			log.Printf("[INFO] 内核 Mode 变更: %s -> %s", a.Cfg.Get("mode"), resp.Mode)
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
					log.Printf("[WARN] TUN 启动失败，关闭本地 Tun")
					a.Cfg.Set("tun", "false")

					if a.Cfg.Get("proxy") != "true" {
						log.Printf("[INFO] 容灾: 回退开启系统代理")
						a.Cfg.Set("proxy", "true")
						a.syncSystemProxy()
					}
					changed = true
				} else {
					log.Printf("[DEBUG] 忽略 TUN 初始化状态")
				}
			} else {
				log.Printf("[INFO] 内核 Tun.Enable 变更: %v -> %v", wantTun, resp.Tun.Enable)
				a.Cfg.Set("tun", fmt.Sprintf("%t", resp.Tun.Enable))
				changed = true
			}
		}

		currentActual := a.getActualTunDevice()
		if resp.Tun.Device != "" && resp.Tun.Device != currentActual {
			log.Printf("[INFO] 内核 Tun.Device 变更: %s -> %s", currentActual, resp.Tun.Device)
			a.setActualTunDevice(resp.Tun.Device)
			changed = true
		}

		if changed {
			realAlive := sys.IsTunActive(a.getActualTunDevice())
			if a.State.IsTunAlive() != realAlive {
				log.Printf("[DEBUG] 轮询纠偏 TUN 真实状态: %v", realAlive)
				a.State.SetTunAlive(realAlive)
			}
		}

		return changed
	}
	return false
}
