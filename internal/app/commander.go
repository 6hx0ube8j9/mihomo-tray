package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/core"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/ui"
	"mihomo-tray/internal/webui"
)

func (a *Application) handleUICommand(ctx context.Context, cmd ui.UICommand) {
	switch cmd.Action {
	case "OpenProfileManager":
		a.uiStateMutex.Lock()
		items := make([]ui.ProfileItem, len(a.lastUIState.ProfileItems))
		copy(items, a.lastUIState.ProfileItems)
		a.uiStateMutex.Unlock()

		if a.ShowProfileManager != nil {
			a.ShowProfileManager(items)
		}
		return

	case "RequestAddLocalProfile":
		go func() {
			if selectedPath, ok := ui.OpenYAMLFileDialog(); ok {
				a.UICommandCh <- ui.UICommand{Action: "AddLocalProfile", Payload: selectedPath}
			}
		}()
		return

	case "RequestAddRemoteProfile":
		go func() {
			if a.ShowSubscriptionEditor != nil {
				name, url, interval, ok := a.ShowSubscriptionEditor("添加远程订阅", "", "", 3)
				if ok {
					autoUpdate := interval > 0
					payload := fmt.Sprintf("%s|%s|%d|%t", name, url, interval, autoUpdate)
					a.UICommandCh <- ui.UICommand{Action: "AddRemoteProfile", Payload: payload}
				}
			}
		}()
		return

	case "RequestEditRemoteProfile":
		targetRelPath := cmd.Payload
		if p, ok := a.Cfg.GetProfileByPath(targetRelPath); ok {
			go func(profile config.ProfileItem) {
				if a.ShowSubscriptionEditor != nil {
					name, url, interval, ok := a.ShowSubscriptionEditor("编辑订阅信息", profile.Name, profile.URL, profile.Interval)
					if ok {
						if name != profile.Name || url != profile.URL || interval != profile.Interval {
							oldURL := profile.URL

							profile.Name = name
							profile.URL = url
							profile.Interval = interval
							profile.AutoUpdate = interval > 0

							a.Cfg.UpsertProfile(profile)

							if url != oldURL {
								go a.executeRemoteUpdate(context.Background(), profile.Path, true, false)
							}
							a.pushUIState()
						}
					}
				}
			}(p)
		}
		return

	case "AddLocalProfile":
		if a.State.IsProfileSwitching() {
			break
		}
		a.State.SetProfileSwitching(true)

		go func(sourcePath string) {
			defer a.State.SetProfileSwitching(false)
			defer a.pushUIState()

			exePath := core.GetKernelPath(a.Cfg.BaseDir())
			if err := core.ValidateConfig(exePath, a.Cfg.BaseDir(), sourcePath); err != nil {
				ui.ShowErrorMessage(nil, "导入失败", "配置文件存在错误：\n\n"+err.Error())
				return
			}

			targetName, _, err := a.Cfg.SafeCopyUntrustedConfig(sourcePath)
			if err != nil {
				ui.ShowErrorMessage(nil, "导入异常", "文件拷贝失败:\n"+err.Error())
				return
			}
			a.Cfg.RegisterNewProfile(targetName)
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
		targetRelPath := filepath.ToSlash(filepath.Join(config.ProfilesDir, fileName))

		newItem := config.ProfileItem{
			Name:       safeName,
			Path:       targetRelPath,
			URL:        strings.TrimSpace(parts[1]),
			AutoUpdate: parts[3] == "true",
			Interval:   interval,
		}

		_, exists := a.Cfg.GetProfileByPath(targetRelPath)

		a.Cfg.UpsertProfile(newItem)
		go a.executeRemoteUpdate(ctx, targetRelPath, true, !exists)

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
			go a.executeRemoteUpdate(ctx, p.Path, true, false)
		}

	case "SwitchProfile":
		if a.State.IsProfileSwitching() {
			slog.Warn("操作过快或正在处理中，已阻断并发请求，强制刷新 UI 纠正残留")
			a.pushUIState()
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

			if err := a.Cfg.ValidatePhysicalFile(target); err != nil {
				go ui.ShowErrorMessage(nil, "切换失败", "目标配置无法读取或已丢失：\n"+err.Error())
				return
			}

			exePath := core.GetKernelPath(a.Cfg.BaseDir())
			absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(target))
			if err := core.ValidateConfig(exePath, a.Cfg.BaseDir(), absPath); err != nil {
				go ui.ShowErrorMessage(nil, "加载中止", "该配置存在语法错误，拒绝加载：\n\n"+err.Error())
				return
			}

			oldActive := a.Cfg.GetActivePath()
			a.Cfg.SetActiveProfile(target)
			a.pushUIState() 

			if err := a.applyConfigTransaction(context.Background(), target); err != nil {
				go ui.ShowErrorMessage(nil, "内核重启异常", "运行时发生错误：\n\n"+err.Error())
				a.Cfg.SetActiveProfile(oldActive) 
			} else {
				a.restartWebUIIfOpen()
			}
		}(cmd.Payload)

	case "RemoveProfile":
		targetPath := cmd.Payload

		if targetPath == a.Cfg.GetActivePath() {
			slog.Warn("尝试删除活跃配置，已将其静默拦截")
			break
		}

		if !ui.ShowConfirmMessage(nil, "确认删除", "确定要删除此配置文件吗？\n\n此操作不可恢复，本地物理文件将被同时删除。") {
			slog.Info("用户取消了删除操作", "path", targetPath)
			break
		}

		slog.Info("删除配置文件", "path", targetPath)

		absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(targetPath))
		if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("清理物理底层文件失败，文件可能被占用", "path", absPath, "err", err)
		}

		a.Cfg.RemoveProfile(targetPath)

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
		a.OpenWebUI()

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

		if err := a.Cfg.ValidatePhysicalFile(targetRelPath); err != nil {
			ui.ShowErrorMessage(nil, "打开失败", "配置文件不存在或已损坏：\n\n"+err.Error())
			break
		}

		absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(targetRelPath))
		_ = sys.ExecuteSystemCommand(absPath)

	case "ExitApp":
		webui.Cleanup()
	}

	a.pushUIState()
}

func (a *Application) OpenWebUI() {
	if a.State.GetPhase() != state.PhaseRunning {
		slog.Warn("内核尚未就绪，无法打开 WebUI")
		return
	}

	activeApiAddr, activeSecret := a.API.GetEndpoint()

	cfg := webui.Config{
		APIAddr:   activeApiAddr,
		Secret:    activeSecret,
		ProxyPort: a.Cfg.Get("port"),
		BaseDir:   a.Cfg.BaseDir(),
		UIName:    a.Cfg.Get("external-ui-name"),
	}
	go webui.Launch(cfg, a.webuiEventCh)
}
