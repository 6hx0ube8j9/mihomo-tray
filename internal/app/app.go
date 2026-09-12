package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"strconv"
	"sync"
	"sync/atomic"
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

	probeGen atomic.Uint64
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
	reqTime := a.State.GetTunRequestedTime()
	lostTime := a.State.GetTunLostTime()

	reqActive := !reqTime.IsZero() && time.Since(reqTime) < TunInitGracePeriod
	lostActive := !lostTime.IsZero() && time.Since(lostTime) < TunLostAlarmDelay

	return reqActive || lostActive
}

func (a *Application) reconcileTunState(kernelTunEnabled bool) bool {
	wantTun := a.Cfg.Get("tun") == "true"

	if wantTun && kernelTunEnabled && a.State.IsTunAlive() && a.isTunInGracePeriod() {
		a.State.SetTunRequestedTime(time.Time{})
		slog.Debug("TUN 接口与虚拟网卡均已就绪，提前解除初始化保护")
	}

	if kernelTunEnabled != wantTun {
		if wantTun && !kernelTunEnabled && a.isTunInGracePeriod() {
			if time.Since(a.State.GetTunRequestedTime()) < TunInitGracePeriod {
				slog.Debug("TUN 处于启动保护期，暂缓状态同步")
				return false
			}
		}

		slog.Info("TUN 配置发生外部变更", "expected", wantTun, "actual", kernelTunEnabled)
		a.Cfg.Set("tun", fmt.Sprintf("%t", kernelTunEnabled))
		return true
	}

	return false
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

	activeRelPath := a.Cfg.GetActivePath()
	if modified, err := a.Cfg.PrepareYAMLForPath(activeRelPath); err != nil {
		slog.Error("检查内核配置文件失败", "err", err)
	} else if modified {
		slog.Info("已自动修正并同步内核配置文件")
	}

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
			if event == ui.EventError {
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
				
				currentGen := a.probeGen.Add(1)

				go func(gen uint64) {
					defer a.State.SetRestarting(false)
					
					for i := 0; i < 600; i++ {
						if a.State.IsExiting() || ctx.Err() != nil {
							return
						}

						if a.probeGen.Load() != gen {
							return
						}

						if a.State.GetPhase() == state.PhaseRunning {
							return
						}

						pollCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
						_, err := a.API.DoRequest(pollCtx, "GET", "/configs", nil)
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
					
					if a.probeGen.Load() == gen && !a.State.IsExiting() {
						slog.Error("内核进程假死，守护进程已自动熄火挂起")
						a.Kernel.HaltDaemon() 
						a.State.SetPhase(state.PhaseInitializing)
						a.pushUIState()
					}
				}(currentGen)

			} else if event == core.EventKernelExit {
				a.probeGen.Add(1)
				
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
		}
	}
}

func (a *Application) handleUICommand(ctx context.Context, cmd ui.UICommand) {
	switch cmd.Action {
	case "RequestAddLocalProfile":
		go func() {
			if selectedPath, ok := sys.OpenYAMLFileDialog(); ok {
				a.UICommandCh <- ui.UICommand{Action: "AddLocalProfile", Payload: selectedPath}
			}
		}()
		return

	case "AddLocalProfile":
		if err := a.Cfg.PrepareLocalConfig(cmd.Payload); err != nil {
			slog.Error("导入本地配置失败", "err", err, "path", cmd.Payload)
			sys.ShowElevationPrompt("配置导入失败", err.Error())
			break
		}
		cmd.Payload = a.Cfg.GetActivePath()
		fallthrough

	case "SwitchProfile":
		if a.State.IsProfileSwitching() {
			slog.Warn("配置切换正在进行中，已阻断并发请求")
			break
		}
		a.State.SetProfileSwitching(true)
		
		go func(relPath string) {
			defer a.State.SetProfileSwitching(false)
			
			if relPath != "" {
				a.Cfg.SetActiveProfile(relPath)
			} else {
				relPath = a.Cfg.GetActivePath()
			}

			absPath := a.Cfg.GetActivePathAbs()
			
			if _, err := os.Stat(absPath); err != nil {
				slog.Error("目标物理文件已丢失或无法读取，中止切换", "path", absPath, "err", err)
				sys.ShowElevationPrompt("配置文件失效", "无法切换到该配置，目标物理文件已丢失或无读取权限！")
				return
			}

			if _, err := a.Cfg.PrepareYAMLForPath(relPath); err != nil {
				slog.Error("修补 YAML 核心参数失败", "err", err)
				return
			}

			reqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			slog.Info("正在向内核下发切换指令", "target", absPath)
			
			payload := map[string]interface{}{"path": absPath, "payload": ""}
			if _, err := a.API.DoRequest(reqCtx, "PUT", "/configs?force=true", payload); err != nil {
				slog.Error("内核热重载请求失败", "err", err)
				return
			}

			time.Sleep(200 * time.Millisecond)
			a.syncAllConfig(context.Background())
			a.syncSystemProxy()

			select {
			case a.apiPollCh <- struct{}{}:
			default:
			}
			a.pushUIState()
		}(cmd.Payload)

	case "RemoveProfile":
		slog.Info("移除本地配置", "path", cmd.Payload)
		a.Cfg.RemoveProfile(cmd.Payload)

	case "ToggleAutoStart":
		enable := cmd.Payload == "true"

		if !sys.IsAdmin() {
			slog.Info("普通权限修改开机自启，发起 UAC 提权")
			arg := "--disable-autostart"
			if enable { arg = "--enable-autostart" }
			err := sys.RunAsAdmin(a.Cfg.ExePath(), a.Cfg.BaseDir(), arg, "--restarting")
			
			if sys.IsUserCancelled(err) {
				slog.Info("用户取消提权，保持当前会话")
			} else if err == nil {
				slog.Info("提权请求已下发，当前普通进程退出")
				os.Exit(0)
			}
			a.pushUIState()
			return
		}

		a.Cfg.Set("autostart", strconv.FormatBool(enable))

		if enable {
			sys.ToggleAutoStart(a.Cfg.ExePath(), a.Cfg.BaseDir(), true)
		} else {
			if sys.CheckAutoStartStatus() && !sys.IsTaskPathValid(a.Cfg.ExePath()) {
				slog.Warn("计划任务指向其他程序路径，跳过清理")
			} else {
				sys.ToggleAutoStart(a.Cfg.ExePath(), a.Cfg.BaseDir(), false)
			}
		}

	case "ToggleRunAsAdmin":
		enable := cmd.Payload == "true"
		
		if enable && !sys.IsAdmin() {
			err := sys.RunAsAdmin(a.Cfg.ExePath(), a.Cfg.BaseDir(), "--enable-run-as-admin", "--restarting")
			if err == nil { os.Exit(0) }
			a.pushUIState()
			return
		}
		a.Cfg.Set("run_as_admin", strconv.FormatBool(enable))

	case "ToggleTun":
		enable := cmd.Payload == "true"

		if enable && !sys.IsAdmin() {
			err := sys.RunAsAdmin(a.Cfg.ExePath(), a.Cfg.BaseDir(), "--enable-tun", "--restarting")
			if err == nil { os.Exit(0) }
			a.pushUIState()
			return
		}

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

			reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			if err := a.API.SyncConfigToKernel(reqCtx, map[string]interface{}{"tun": tunPayload}); err != nil {
				a.Cfg.Set("tun", strconv.FormatBool(!enable))
			}
			select { case a.apiPollCh <- struct{}{}: default: }
		}()

	case "ToggleProxy":
		enable := cmd.Payload == "true"
		a.Cfg.Set("proxy", strconv.FormatBool(enable)) 
		a.syncSystemProxy()

	case "SwitchMode":
		a.Cfg.Set("mode", cmd.Payload)
		a.State.SetConfigSyncing(true)
		go func() {
			defer a.State.SetConfigSyncing(false)
			reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			_ = a.API.SyncConfigToKernel(reqCtx, map[string]interface{}{"mode": cmd.Payload})
			select { case a.apiPollCh <- struct{}{}: default: }
		}()

	case "ForceSyncAPI":
		select { case a.apiPollCh <- struct{}{}: default: }
		return

	case "OpenWebUI":
		if a.State.GetPhase() != state.PhaseRunning {
			slog.Warn("内核尚未就绪，无法打开 WebUI")
			break
		}
		cfg := ui.Config{
			APIAddr:   a.Cfg.Get("external-controller"),
			Secret:    a.Cfg.Get("secret"),
			ProxyPort: a.Cfg.Get("port"),
			BaseDir:   a.Cfg.BaseDir(),
			UIName:    a.Cfg.Get("external-ui-name"),
		}
		go ui.Launch(cfg, a.webuiEventCh)

	case "OpenBaseDir":
		_ = sys.ExecuteSystemCommand(a.Cfg.BaseDir())

	case "ReloadConfig":
		a.ReloadConfig(ctx)

	case "RestartKernel":
		a.RestartKernel()

	case "OpenConfigFile":
		_ = sys.ExecuteSystemCommand(a.Cfg.GetActivePathAbs())

	case "ExitApp":
		ui.Cleanup()
	}

	a.pushUIState()
}

func (a *Application) syncSystemProxy() {
	enable := a.Cfg.Get("proxy") == "true"
	port := a.Cfg.Get("port")
	if enable {
		slog.Info("系统代理配置已启用", "port", port)
	} else {
		slog.Info("系统代理配置已关闭")
	}
	if err := sys.SetSystemProxy(enable, port); err != nil {
		slog.Error("设置系统代理失败", "err", err)
	}
}

func (a *Application) handleProxyStatusChange(ctx context.Context, status sys.ProxyStatus) {
	if a.State.IsExiting() {
		return
	}

	expectedProxy := a.Cfg.Get("proxy") == "true"
	expectedPort := a.Cfg.Get("port")
	expectedServer := "127.0.0.1:" + expectedPort

	if expectedProxy {
		if status.Enabled {
			if status.Server != "" && !strings.EqualFold(status.Server, expectedServer) {
				slog.Warn("系统代理被外部修改，已关闭本地代理", "server", status.Server)
				a.Cfg.Set("proxy", "false")
				a.pushUIState()
			}
			return
		}

		if !a.proxyRepairing.CompareAndSwap(false, true) {
			return
		}

		go func() {
			defer a.proxyRepairing.Store(false)

			for i := 1; i <= 10; i++ {
				if a.State.IsExiting() || ctx.Err() != nil || a.Cfg.Get("proxy") != "true" {
					return
				}
				a.syncSystemProxy()

				select {
				case <-ctx.Done(): return
				case <-time.After(1000 * time.Millisecond):
				}

				cur, err := sys.GetProxyStatus()
				if err == nil && cur.Enabled && strings.EqualFold(cur.Server, expectedServer) {
					return
				}
			}
			a.Cfg.Set("proxy", "false")
			a.pushUIState()
		}()
		return
	}
}

func (a *Application) calculateUIState() ui.UIState {
	s := ui.UIState{
		IsTun:            a.Cfg.Get("tun") == "true",
		IsProxy:          a.Cfg.Get("proxy") == "true",
		Mode:             a.Cfg.Get("mode"),
		AutoStart:        a.Cfg.Get("autostart") == "true",
		RunAsAdmin:       a.Cfg.Get("run_as_admin") == "true",
		IsAdmin:          sys.IsAdmin(),
	}

	activePath := a.Cfg.GetActivePath()
	profiles := a.Cfg.GetProfiles()
	s.CanAddProfile = len(profiles) < 5
	
	for _, p := range profiles {
		s.ProfileItems = append(s.ProfileItems, ui.ProfileItem{
			Name:     config.TruncateMiddle(p.Name),
			Path:     p.Path,
			IsActive: p.Path == activePath,
		})
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
	if a.State.IsExiting() { return }

	a.uiStateMutex.Lock()
	defer a.uiStateMutex.Unlock()

	newState := a.calculateUIState()
	changed := false

	if newState.IconState != a.lastUIState.IconState || 
	   newState.IsTun != a.lastUIState.IsTun || 
	   newState.IsProxy != a.lastUIState.IsProxy || 
	   newState.Mode != a.lastUIState.Mode || 
	   len(newState.ProfileItems) != len(a.lastUIState.ProfileItems) {
		changed = true
	} else {
		for i := range newState.ProfileItems {
			if newState.ProfileItems[i].Path != a.lastUIState.ProfileItems[i].Path ||
			   newState.ProfileItems[i].IsActive != a.lastUIState.ProfileItems[i].IsActive {
				changed = true
				break
			}
		}
	}

	if changed {
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

	go func() {
		defer a.State.SetReloading(false)

		activeRelPath := a.Cfg.GetActivePath()
		if _, err := a.Cfg.PrepareYAMLForPath(activeRelPath); err != nil {
			slog.Error("检查内核配置文件失败", "err", err)
		}

		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		payload := map[string]interface{}{"path": a.Cfg.GetActivePathAbs(), "payload": ""}
		_, err := a.API.DoRequest(reqCtx, "PUT", "/configs?force=true", payload)
		cancel()

		if err != nil {
			slog.Error("重载内核配置失败", "err", err)
			return
		}

		time.Sleep(200 * time.Millisecond)
		a.syncAllConfig(ctx)
		a.syncSystemProxy()

		select { case a.apiPollCh <- struct{}{}: default: }
	}()
}

func (a *Application) RestartKernel() {
	slog.Info("正在重启内核进程")
	a.State.SetRestarting(true)
	a.State.SetReloading(false)
	a.Kernel.HaltDaemon()

	activeRelPath := a.Cfg.GetActivePath()
	if _, err := a.Cfg.PrepareYAMLForPath(activeRelPath); err != nil {
		slog.Error("检查内核配置文件失败", "err", err)
	}

	if a.Cfg.Get("tun") == "true" {
		a.State.SetTunRequestedTime(time.Now())
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
		slog.Info("TUN 网卡状态变更", "device", tunDev, "active", alive)
		a.State.SetTunAlive(alive)
		if !alive {
			a.State.SetTunLostTime(time.Now())
		}

		go func() {
			for i := 0; i < 3; i++ {
				select {
				case <-ctx.Done(): return
				case <-time.After(300 * time.Millisecond):
				}
				select { case a.apiPollCh <- struct{}{}: default: }
			}
		}()
		a.pushUIState()
	}
}

func (a *Application) syncAllConfig(ctx context.Context) {
	if a.State.GetPhase() != state.PhaseRunning {
		return
	}
	tunPayload := map[string]interface{}{"enable": a.Cfg.Get("tun") == "true"}
	if dev := a.Cfg.Get("tun_device"); dev != "" {
		tunPayload["device"] = dev
	}
	payload := map[string]interface{}{
		"tun":  tunPayload,
		"mode": a.Cfg.Get("mode"),
	}
	_ = a.API.SyncConfigToKernel(ctx, payload)
}

func (a *Application) pollKernelAPI(ctx context.Context) bool {
	if a.State.IsExiting() || a.State.IsReloading() || a.State.IsConfigSyncing() {
		return false
	}

	queryCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	body, err := a.API.DoRequest(queryCtx, "GET", "/configs", nil)
	if err != nil { return false }

	var resp struct {
		Mode string `json:"mode"`
		Tun  struct {
			Enable bool   `json:"enable"`
			Device string `json:"device"`
		} `json:"tun"`
	}

	if json.Unmarshal(body, &resp) == nil {
		changed := false

		currentActual := a.getActualTunDevice()
		if resp.Tun.Device != "" && resp.Tun.Device != currentActual {
			a.setActualTunDevice(resp.Tun.Device)
			currentActual = resp.Tun.Device
			changed = true
		}
		realAlive := sys.IsTunActive(currentActual)
		if a.State.IsTunAlive() != realAlive {
			a.State.SetTunAlive(realAlive)
			changed = true
		}

		if resp.Mode != "" && resp.Mode != a.Cfg.Get("mode") {
			slog.Info("内核路由模式已变更", "from", a.Cfg.Get("mode"), "to", resp.Mode)
			a.Cfg.Set("mode", resp.Mode)
			changed = true
		}

		if a.reconcileTunState(resp.Tun.Enable) { changed = true }

		wantTun := a.Cfg.Get("tun") == "true"
		if changed && wantTun && !realAlive && !a.isTunInGracePeriod() {
			slog.Warn("TUN 网卡未就绪或已断开，检查驱动与权限", "device", currentActual)
		}

		return changed
	}
	return false
}
