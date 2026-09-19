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
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/ui"
	"mihomo-tray/internal/webui"
)

func (a *Application) safePreflightCheck(targetRelPath string, actionTitle string) error {
	if err := a.Cfg.ValidatePhysicalFile(targetRelPath); err != nil {
		ui.ShowErrorMessage(nil, actionTitle+"被拦截", "目标配置异常，请求已被中止：\n\n"+err.Error())
		return err
	}
	return nil
}

func (a *Application) handleUICommand(ctx context.Context, cmd domain.UICommand) {
	switch cmd.Action {
	case "OpenProfileManager":
		a.uiStateMutex.Lock()
		items := make([]domain.UIProfileItem, len(a.lastUIState.ProfileItems))
		copy(items, a.lastUIState.ProfileItems)
		a.uiStateMutex.Unlock()

		if a.ShowProfileManager != nil {
			a.ShowProfileManager(items)
		}
		return

	case "RequestAddLocalProfile":
		go func() {
			if selectedPath, ok := ui.OpenYAMLFileDialog(); ok {
				a.UICommandCh <- domain.UICommand{Action: "AddLocalProfile", Payload: selectedPath}
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
					a.UICommandCh <- domain.UICommand{Action: "AddRemoteProfile", Payload: payload}
				}
			}
		}()
		return

	case "RequestEditRemoteProfile":
		targetRelPath := cmd.Payload
		if p, ok := a.Cfg.GetProfileByPath(targetRelPath); ok {
			go func(profile domain.ProfileItem) {
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
							a.ForcePushUIState()
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
			defer a.ForcePushUIState()

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

		newItem := domain.ProfileItem{
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
					slog.Info("修改订阅自动更新频率", "path", targetPath, "interval", interval)
				}
			}
		}

	case "UpdateRemoteProfile":
		if p, ok := a.Cfg.GetProfileByPath(cmd.Payload); ok {
			go a.executeRemoteUpdate(ctx, p.Path, true, false)
		}

	case "SwitchProfile":
		if cmd.Payload != "" && cmd.Payload == a.Cfg.GetActivePath() {
			slog.Debug("配置已激活，忽略重复切换", "path", cmd.Payload)
			break
		}

		if a.State.IsProfileSwitching() {
			a.ForcePushUIState()
			break
		}
		a.State.SetProfileSwitching(true)

		go func(relPath string) {
			defer a.State.SetProfileSwitching(false)
			defer a.ForcePushUIState()

			target := relPath
			if target == "" {
				target = a.Cfg.GetActivePath()
			}

			if err := a.safePreflightCheck(target, "切换配置"); err != nil {
				return
			}

			exePath := core.GetKernelPath(a.Cfg.BaseDir())
			absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(target))
			if err := core.ValidateConfig(exePath, a.Cfg.BaseDir(), absPath); err != nil {
				ui.ShowErrorMessage(nil, "加载中止", "该配置存在语法错误，拒绝加载：\n\n"+err.Error())
				return
			}

			oldActive := a.Cfg.GetActivePath()
			a.Cfg.SetActiveProfile(target)
			a.pushUIState()

			if err := a.applyConfigTransaction(context.Background(), target); err != nil {
				ui.ShowErrorMessage(nil, "内核重启异常", "运行时发生错误：\n\n"+err.Error())
				a.Cfg.SetActiveProfile(oldActive)
			} else {
				a.restartWebUIIfOpen()
			}
		}(cmd.Payload)

	case "RemoveProfile":
		targetPath := cmd.Payload

		if targetPath == a.Cfg.GetActivePath() {
			slog.Warn("拒绝删除活跃配置")
			break
		}

		if !ui.ShowConfirmMessage(nil, "确认删除", "确定要删除此配置文件吗？\n\n此操作不可恢复，本地物理文件将被同时删除。") {
			slog.Info("取消删除配置", "path", targetPath)
			break
		}

		slog.Info("删除配置", "path", targetPath)

		absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(targetPath))
		if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("清理物理文件失败", "path", absPath, "err", err)
		}

		a.Cfg.RemoveProfile(targetPath)

	case "ToggleAutoStart":
		enable := cmd.Payload == "true"

		if !sys.IsAdmin() {
			slog.Info("发起 UAC 提权")
			arg := "--disable-autostart"
			if enable {
				arg = "--enable-autostart"
			}
			err := sys.RunAsAdmin(a.Cfg.ExePath(), a.Cfg.BaseDir(), arg, "--restarting")

			if sys.IsUserCancelled(err) {
				slog.Info("用户取消提权")
			} else if err == nil {
				slog.Info("提权请求成功，当前进程退出")
				a.SafeShutdown(nil)
				os.Exit(0)
			}
			a.ForcePushUIState()
			return
		}

		a.Cfg.Set("autostart", strconv.FormatBool(enable))

		if enable {
			sys.ToggleAutoStart(a.Cfg.ExePath(), a.Cfg.BaseDir(), true)
		} else {
			if sys.CheckAutoStartStatus() && !sys.IsTaskPathValid(a.Cfg.ExePath()) {
				slog.Warn("跳过清理未知计划任务")
			} else {
				sys.ToggleAutoStart(a.Cfg.ExePath(), a.Cfg.BaseDir(), false)
			}
		}

	case "ToggleRunAsAdmin":
		enable := cmd.Payload == "true"

		if enable && !sys.IsAdmin() {
			err := sys.RunAsAdmin(a.Cfg.ExePath(), a.Cfg.BaseDir(), "--enable-run-as-admin", "--restarting")
			if err == nil {
				a.SafeShutdown(nil)
				os.Exit(0)
			}
			a.ForcePushUIState()
			return
		}
		a.Cfg.Set("run_as_admin", strconv.FormatBool(enable))

	case "ToggleTun":
		enable := cmd.Payload == "true"

		if enable && !sys.IsAdmin() {
			err := sys.RunAsAdmin(a.Cfg.ExePath(), a.Cfg.BaseDir(), "--enable-tun", "--restarting")
			if err == nil {
				a.SafeShutdown(nil)
				os.Exit(0)
			}
			a.ForcePushUIState()
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

		if err := a.safePreflightCheck(targetRelPath, "打开文件"); err != nil {
			break
		}

		absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(targetRelPath))
		_ = sys.ExecuteSystemCommand(absPath)

	case "ExitApp":
		webui.Cleanup()
	}

	a.ForcePushUIState()
}

func (a *Application) OpenWebUI() {
	if a.State.GetPhase() != domain.PhaseRunning {
		slog.Warn("内核未就绪，无法打开 WebUI")
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
