package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
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
		webuiEventCh:  make(chan ui.Event, 1),
	}
}

func (a *Application) getActualTunDevice() string {
	if dev := a.State.GetActualTunDevice(); dev != "" {
		return dev
	}
	return a.Cfg.Get("tun_device")
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

	// [核心重构]：开机强制对齐流水线
	activeRelPath := a.Cfg.GetActivePath()
	if _, extracted, err := a.Cfg.PrepareYAMLForPath(activeRelPath); err != nil {
		slog.Error("开机生成核心运行配置失败", "err", err)
	} else {
		if len(extracted) > 0 {
			a.Cfg.UpdateBatch(extracted)
		}
	}

	// 严格从最终缝合生成的 config.yaml 提取 API 参数
	runtimeAbs := filepath.Join(a.Cfg.BaseDir(), "config.yaml")
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

func (a *Application) executeRemoteUpdate(ctx context.Context, targetRelPath string, isManual bool) {
	if !a.State.TryAcquireProfileLock(targetRelPath) {
		if isManual {
			slog.Warn("该订阅正在后台更新，已拦截重复操作", "path", targetRelPath)
		}
		return
	}
	defer a.State.ReleaseProfileLock(targetRelPath)

	validator := func(tmpPath string) error {
		exePath := core.GetKernelPath(a.Cfg.BaseDir())
		return core.ValidateConfig(exePath, a.Cfg.BaseDir(), tmpPath)
	}

	port := a.Cfg.Get("port")
	success, err := a.Cfg.UpgradeSubscription(targetRelPath, port, validator)

	if err != nil {
		if isManual {
			sys.ShowErrorMessage("订阅更新拦截", err.Error())
		}
		slog.Error("订阅更新终止", "path", targetRelPath, "err", err)
		return
	}

	if success {
		slog.Info("订阅更新已完成", "path", targetRelPath)
		if a.Cfg.GetActivePath() == targetRelPath {
			slog.Info("活跃配置发生变更，触发内核热重载")
			_ = a.applyConfigTransaction(context.Background(), targetRelPath)
		}
		a.pushUIState()
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

    case "RequestAddRemoteProfile":
		go func() {
			res := sys.ShowAddSubDialog()
			if res.OK {
				payload := fmt.Sprintf("%s|%s|%d|%t", res.Name, res.URL, res.Interval, res.AutoUpdate)
				a.UICommandCh <- ui.UICommand{Action: "AddRemoteProfile", Payload: payload}
			}
		}()
		return
		
	case "AddLocalProfile":
		if a.State.IsProfileSwitching() {
			slog.Warn("配置操作正在进行中，已阻断并发请求")
			break
		}
		a.State.SetProfileSwitching(true)

		go func(sourcePath string) {
			defer a.State.SetProfileSwitching(false)
			defer a.pushUIState()

			slog.Info("开始沙箱预检新导入的本地配置", "source", sourcePath)

			exePath := core.GetKernelPath(a.Cfg.BaseDir())
			if err := core.ValidateConfig(exePath, a.Cfg.BaseDir(), sourcePath); err != nil {
				sys.ShowErrorMessage("配置导入被拦截", "该文件存在语法错误:\n\n"+err.Error())
				return
			}

			targetName, _, err := a.Cfg.SafeCopyUntrustedConfig(sourcePath)
			if err != nil {
				sys.ShowErrorMessage("导入配置异常", "文件拷贝失败:\n"+err.Error())
				return
			}

			a.Cfg.RegisterNewProfile(targetName)
			slog.Info("新配置已通过预检并入库", "name", targetName)

			if err := a.applyConfigTransaction(context.Background(), targetName); err != nil {
				sys.ShowErrorMessage("配置应用失败", "内核拒绝切换该配置 (可能是端口冲突)：\n\n"+err.Error())
			} else {
				a.restartWebUIIfOpen()
			}
		}(cmd.Payload)

	case "AddRemoteProfile":
		parts := strings.SplitN(cmd.Payload, "|", 4)
		if len(parts) != 4 {
			return
		}

		interval, _ := strconv.Atoi(parts[2])
		rawName := strings.TrimSpace(parts[0])
		if rawName == "" {
			rawName = fmt.Sprintf("%d", time.Now().Unix())
		}

		safeName := strings.ReplaceAll(rawName, "/", "_")
		fileName := fmt.Sprintf("%s.yaml", safeName)
		targetRelPath := filepath.ToSlash(filepath.Join("profiles", fileName))

		newItem := config.ProfileItem{
			Name:       safeName,
			Path:       targetRelPath,
			URL:        strings.TrimSpace(parts[1]),
			AutoUpdate: parts[3] == "true",
			Interval:   interval,
		}

		a.Cfg.UpsertProfile(newItem)
		go a.executeRemoteUpdate(ctx, targetRelPath, true)

     case "SetProfileInterval":
		parts := strings.Split(cmd.Payload, "|")
		if len(parts) == 2 {
			targetPath := parts[0]
			interval, err := strconv.Atoi(parts[1])
			if err == nil {
				if p, ok := a.Cfg.GetProfileByPath(targetPath); ok {
					p.Interval = interval
					p.AutoUpdate = interval > 0
					a.Cfg.UpsertProfile(p)
					slog.Info("已修改订阅自动更新频率", "path", targetPath, "interval", interval)
				}
			}
		}
		
	case "UpdateRemoteProfile":
		if p, ok := a.Cfg.GetProfileByPath(cmd.Payload); ok {
			go a.executeRemoteUpdate(ctx, p.Path, true)
		}

	case "SwitchProfile":
		if a.State.IsProfileSwitching() {
			slog.Warn("配置切换正在进行中，已阻断并发请求")
			break
		}
		a.State.SetProfileSwitching(true)

		go func(relPath string) {
			defer a.State.SetProfileSwitching(false)
			defer a.pushUIState()

			target := relPath
			if target == "" {
				target = a.Cfg.GetActivePath()
			}

			slog.Info("开始执行配置切换事务", "target", target)
			if err := a.applyConfigTransaction(context.Background(), target); err != nil {
				if strings.Contains(err.Error(), "底稿文件丢失") {
					sys.ShowErrorMessage("配置文件失效", "找不到该配置，它可能已被删除。系统已自动为您清理列表。")
					a.Cfg.RemoveProfile(target)
				} else {
					sys.ShowErrorMessage("切换配置失败", "内核拒绝加载该配置：\n\n"+err.Error())
				}
			} else {
				a.restartWebUIIfOpen()
			}
		}(cmd.Payload)

	case "RemoveProfile":
		slog.Info("移除本地配置", "path", cmd.Payload)
		a.Cfg.RemoveProfile(cmd.Payload)

	case "ToggleAutoStart":
		enable := cmd.Payload == "true"

		if !sys.IsAdmin() {
			slog.Info("普通权限修改开机自启，发起 UAC 提权")
			arg := "--disable-autostart"
			if enable {
				arg = "--enable-autostart"
			}
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
			if err == nil {
				os.Exit(0)
			}
			a.pushUIState()
			return
		}
		a.Cfg.Set("run_as_admin", strconv.FormatBool(enable))

	case "ToggleTun":
		enable := cmd.Payload == "true"

		if enable && !sys.IsAdmin() {
			err := sys.RunAsAdmin(a.Cfg.ExePath(), a.Cfg.BaseDir(), "--enable-tun", "--restarting")
			if err == nil {
				os.Exit(0)
			}
			a.pushUIState()
			return
		}

		a.Cfg.Set("tun", strconv.FormatBool(enable))

		if enable {
			a.State.SetTunRequestedTime(time.Now())
			a.State.SetActualTunDevice(a.Cfg.Get("tun_device"))
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
			select {
			case a.apiPollCh <- struct{}{}:
			default:
			}
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
			select {
			case a.apiPollCh <- struct{}{}:
			default:
			}
		}()

	case "ForceSyncAPI":
		select {
		case a.apiPollCh <- struct{}{}:
		default:
		}
		return

	case "OpenWebUI":
		if a.State.GetPhase() != state.PhaseRunning {
			slog.Warn("内核尚未就绪，无法打开 WebUI")
			break
		}

		activeApiAddr, activeSecret := a.API.GetEndpoint()

		cfg := ui.Config{
			APIAddr:   activeApiAddr,
			Secret:    activeSecret,
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
		targetRelPath := cmd.Payload
		if targetRelPath == "" {
			targetRelPath = a.Cfg.GetActivePath()
		}
		
		absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(targetRelPath))
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			sys.ShowErrorMessage("打开失败", "底稿文件不存在或已被删除，无法启动编辑器。")
			break
		}
		_ = sys.ExecuteSystemCommand(absPath)

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

		if !a.State.TryAcquireProxyRepair() {
			return
		}

		go func() {
			defer a.State.ReleaseProxyRepair()

			for i := 1; i <= 10; i++ {
				if a.State.IsExiting() || ctx.Err() != nil || a.Cfg.Get("proxy") != "true" {
					return
				}
				a.syncSystemProxy()

				select {
				case <-ctx.Done():
					return
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
		IsTun:      a.Cfg.Get("tun") == "true",
		IsProxy:    a.Cfg.Get("proxy") == "true",
		Mode:       a.Cfg.Get("mode"),
		AutoStart:  a.Cfg.Get("autostart") == "true",
		RunAsAdmin: a.Cfg.Get("run_as_admin") == "true",
		IsAdmin:    sys.IsAdmin(),
	}

	activePath := a.Cfg.GetActivePath()
	profiles := a.Cfg.GetProfiles()
	s.CanAddProfile = len(profiles) < 5

	for _, p := range profiles {
		item := ui.ProfileItem{
			Name:     config.TruncateMiddle(p.Name),
			Path:     p.Path,
			IsActive: p.Path == activePath,
			IsRemote: p.URL != "",
			Interval: p.Interval,
		}

		if item.IsRemote {
			if p.LastUpdate == 0 {
				item.LastUpdate = "从未更新"
			} else {
				diff := time.Since(time.Unix(p.LastUpdate, 0))
				if diff.Hours() > 24 {
					item.LastUpdate = fmt.Sprintf("%d 天前", int(diff.Hours()/24))
				} else if diff.Hours() > 1 {
					item.LastUpdate = fmt.Sprintf("%d 小时前", int(diff.Hours()))
				} else if diff.Minutes() > 1 {
					item.LastUpdate = fmt.Sprintf("%d 分钟前", int(diff.Minutes()))
				} else {
					item.LastUpdate = "刚刚"
				}
			}
		}
		s.ProfileItems = append(s.ProfileItems, item)
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
				newState.ProfileItems[i].IsActive != a.lastUIState.ProfileItems[i].IsActive ||
				newState.ProfileItems[i].Interval != a.lastUIState.ProfileItems[i].Interval ||
				newState.ProfileItems[i].LastUpdate != a.lastUIState.ProfileItems[i].LastUpdate {
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

func (a *Application) applyConfigTransaction(ctx context.Context, targetRelPath string) error {
	absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(targetRelPath))

	if _, err := os.Stat(absPath); err != nil {
		return fmt.Errorf("目标底稿文件丢失或无法读取: %w", err)
	}

	_, extracted, err := a.Cfg.PrepareYAMLForPath(targetRelPath)
	if err != nil {
		return fmt.Errorf("生成运行时配置失败: %w", err)
	}

	runtimeAbs := filepath.Join(a.Cfg.BaseDir(), "config.yaml")
	isKernelRunning := a.State.GetPhase() == state.PhaseRunning

	if isKernelRunning {
		reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()

		payload := map[string]interface{}{"path": filepath.ToSlash(runtimeAbs)}
		_, err := a.API.DoRequest(reqCtx, "PUT", "/configs?force=true", payload)

		if err != nil {
			logMsg := fmt.Errorf("热重载被内核拒绝 | 配置: %s | 原因: %v", targetRelPath, err)
			a.Kernel.WriteCoreLog("ERROR", logMsg.Error())
			slog.Error("配置应用失败，事务已回滚", "target", targetRelPath)
			return err
		}
		slog.Info("内核热重载接受配置，事务提交准备就绪")
	} else {
		slog.Info("内核处于停止状态，准备使用新配置唤醒")
		apiAddr, apiSecret := a.Cfg.ResolveKernelEndpoint(runtimeAbs)
		a.API.SetEndpoint(apiAddr, apiSecret)
	}

	a.Cfg.SetActiveProfile(targetRelPath)
	if len(extracted) > 0 {
		a.Cfg.UpdateBatch(extracted)
	}
	a.syncSystemProxy()

	if !isKernelRunning {
		a.Kernel.WakeDaemon()
	} else {
		time.Sleep(500 * time.Millisecond)
		a.syncAllConfig(ctx)
		select {
		case a.apiPollCh <- struct{}{}:
		default:
		}
	}

	return nil
}

func (a *Application) ReloadConfig(ctx context.Context) {
	if a.State.IsReloading() {
		return
	}
	slog.Info("开始执行配置重载事务")
	a.State.SetReloading(true)

	go func() {
		defer a.State.SetReloading(false)
		defer a.pushUIState()

		if err := a.applyConfigTransaction(ctx, a.Cfg.GetActivePath()); err != nil {
			sys.ShowErrorMessage("配置重载失败", "内核拒绝加载当前配置文件，请检查语法：\n\n"+err.Error())
		} else {
			a.restartWebUIIfOpen()
		}
	}()
}

func (a *Application) RestartKernel() {
	slog.Info("正在重启内核进程")
	a.State.SetRestarting(true)
	a.State.SetReloading(false)
	a.State.SetPhase(state.PhaseInitializing)

	a.Kernel.HaltDaemon()

	activeRelPath := a.Cfg.GetActivePath()
	
	// [核心重构]：重启内核也必须强制对齐生成一次 config.yaml
	if _, extracted, err := a.Cfg.PrepareYAMLForPath(activeRelPath); err != nil {
		slog.Error("生成运行时配置文件失败", "err", err)
	} else if len(extracted) > 0 {
		a.Cfg.UpdateBatch(extracted)
	}

	runtimeAbs := filepath.Join(a.Cfg.BaseDir(), "config.yaml")
	apiAddr, apiSecret := a.Cfg.ResolveKernelEndpoint(runtimeAbs)
	a.API.SetEndpoint(apiAddr, apiSecret)

	if a.Cfg.Get("tun") == "true" {
		a.State.SetTunRequestedTime(time.Now())
	}

	a.Kernel.WakeDaemon()
	a.pushUIState()

	a.restartWebUIIfOpen()
}

func (a *Application) restartWebUIIfOpen() {
	wasOpen := ui.IsActive()
	ui.Cleanup()

	if wasOpen {
		slog.Debug("检测到 Web 面板原先处于活跃状态，等待内核就绪后拉起新环境")

		go func() {
			for i := 0; i < 50; i++ {
				if a.State.IsExiting() {
					return
				}

				if a.State.GetPhase() == state.PhaseRunning {
					slog.Debug("内核已就绪，正在自动重新拉起 Web 面板")
					select {
					case a.UICommandCh <- ui.UICommand{Action: "OpenWebUI"}:
					default:
					}
					return
				}

				time.Sleep(200 * time.Millisecond)
			}
			slog.Warn("等待内核就绪超时，自动拉起 Web 面板失败")
		}()
	}
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
				case <-ctx.Done():
					return
				case <-time.After(300 * time.Millisecond):
				}
				select {
				case a.apiPollCh <- struct{}{}:
				default:
				}
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

		currentActual := a.getActualTunDevice()
		if resp.Tun.Device != "" && resp.Tun.Device != currentActual {
			a.State.SetActualTunDevice(resp.Tun.Device)
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

		if a.reconcileTunState(resp.Tun.Enable) {
			changed = true
		}

		wantTun := a.Cfg.Get("tun") == "true"
		if changed && wantTun && !realAlive && !a.isTunInGracePeriod() {
			slog.Warn("TUN 网卡未就绪或已断开，检查驱动与权限", "device", currentActual)
		}

		return changed
	}
	return false
}
