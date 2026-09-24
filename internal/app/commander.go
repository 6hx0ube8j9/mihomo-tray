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
		ui.ShowErrorMessage(nil, actionTitle+"失败", "目标配置异常，请求中止：\n\n"+err.Error())
		return err
	}
	return nil
}

func (a *Application) handleUICommand(ctx context.Context, cmd domain.UICommand) {
	switch cmd.Action {
	case domain.ActionOpenProfileManager:
		a.uiStateMutex.Lock()
		items := make([]domain.UIProfileItem, len(a.lastUIState.ProfileItems))
		copy(items, a.lastUIState.ProfileItems)
		a.uiStateMutex.Unlock()

		if a.ShowProfileManager != nil {
			a.ShowProfileManager(items)
		}
		return

	case domain.ActionRequestAddLocal:
		go func() {
			if selectedPath, ok := ui.OpenYAMLFileDialog(); ok {
				a.UICommandCh <- domain.UICommand{Action: domain.ActionAddLocalProfile, Payload: selectedPath}
			}
		}()
		return

	case domain.ActionRequestAddRemote:
		go func() {
			if a.ShowSubscriptionEditor != nil {
				name, url, interval, ok := a.ShowSubscriptionEditor("添加远程订阅", "", "", domain.DefaultUpdateInterval)
				if ok {
					autoUpdate := interval > 0
					payload := fmt.Sprintf("%s|%s|%d|%t", name, url, interval, autoUpdate)
					a.UICommandCh <- domain.UICommand{Action: domain.ActionAddRemoteProfile, Payload: payload}
				}
			}
		}()
		return

	case domain.ActionRequestEditRemote:
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
							a.pushUIState()
						}
					}
				}
			}(p)
		}
		return

	case domain.ActionAddLocalProfile:
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
				ui.ShowErrorMessage(nil, "导入失败", "文件拷贝失败:\n"+err.Error())
				return
			}
			a.Cfg.RegisterNewProfile(targetName)
		}(cmd.Payload)

	case domain.ActionAddRemoteProfile:
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

	case domain.ActionSetProfileInterval:
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

	case domain.ActionUpdateRemoteProfile:
		if p, ok := a.Cfg.GetProfileByPath(cmd.Payload); ok {
			go a.executeRemoteUpdate(ctx, p.Path, true, false)
		}

	case domain.ActionSwitchProfile:
		if cmd.Payload != "" && cmd.Payload == a.Cfg.GetActivePath() {
			slog.Debug("配置已激活，忽略重复切换", "path", cmd.Payload)
			a.ForcePushUIState()
			break
		}

		if a.State.IsProfileSwitching() {
			a.ForcePushUIState()
			break
		}
		a.State.SetProfileSwitching(true)

		go func(relPath string) {
			isTransactionFailed := false

			defer func() {
				a.State.SetProfileSwitching(false)
				if isTransactionFailed {
					slog.Debug("配置切换事务回滚，触发 UI 强调整")
					a.ForcePushUIState()
				} else {
					a.pushUIState()
				}
			}()

			target := relPath
			if target == "" {
				target = a.Cfg.GetActivePath()
			}

			if err := a.safePreflightCheck(target, "切换配置"); err != nil {
				isTransactionFailed = true
				return
			}

			exePath := core.GetKernelPath(a.Cfg.BaseDir())
			absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(target))
			if err := core.ValidateConfig(exePath, a.Cfg.BaseDir(), absPath); err != nil {
				ui.ShowErrorMessage(nil, "加载失败", "该配置存在错误，拒绝加载：\n\n"+err.Error())
				isTransactionFailed = true
				return
			}

			oldActive := a.Cfg.GetActivePath()
			a.Cfg.SetActiveProfile(target)
			a.pushUIState()

			if err := a.applyConfigTransaction(context.Background(), target); err != nil {
				ui.ShowErrorMessage(nil, "内核重启异常", "运行时发生错误：\n\n"+err.Error())
				a.Cfg.SetActiveProfile(oldActive)
				isTransactionFailed = true
			} else {
				a.restartWebUIIfOpen()
			}
		}(cmd.Payload)

	case domain.ActionRemoveProfile:
		targetPath := cmd.Payload
		if targetPath == a.Cfg.GetActivePath() {
			slog.Warn("拒绝删除活跃配置")
			break
		}
		if !ui.ShowConfirmMessage(nil, "确认删除", "确定要删除此配置文件吗？\n\n此操作不可恢复，本地文件将被同时删除。") {
			break
		}
		absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(targetPath))
		if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("清理物理文件失败", "path", absPath, "err", err)
		}
		a.Cfg.RemoveProfile(targetPath)

	case domain.ActionMoveProfileUp:
		a.Cfg.MoveProfile(cmd.Payload, -1)

	case domain.ActionMoveProfileDown:
		a.Cfg.MoveProfile(cmd.Payload, 1)

	case domain.ActionToggleAutoStart:
		enable := cmd.Payload == "true"
		
		a.Cfg.Update(func(c *domain.TrayConfig) {
			b := enable
			c.General.Autostart = &b
		})

		if !sys.IsAdmin() {
			slog.Info("修改自启需要管理员权限，发起 UAC 提权")
			err := sys.RunAsAdmin(a.Cfg.ExePath(), a.Cfg.BaseDir(), "--restarting")
			if sys.IsUserCancelled(err) {
				slog.Info("用户取消提权，回滚 JSON 状态")
				a.Cfg.Update(func(c *domain.TrayConfig) {
					b := !enable
					c.General.Autostart = &b
				})
			} else if err == nil {
				a.SafeShutdown(nil)
				os.Exit(0)
			}
			a.ForcePushUIState()
			return
		}

		if enable {
			sys.ToggleAutoStart(domain.AppTaskName, a.Cfg.ExePath(), a.Cfg.BaseDir(), true)
		} else {
			sys.ToggleAutoStart(domain.AppTaskName, a.Cfg.ExePath(), a.Cfg.BaseDir(), false)
		}

	case domain.ActionToggleRunAsAdmin:
		enable := cmd.Payload == "true"
		
		a.Cfg.Update(func(c *domain.TrayConfig) { c.General.RunAsAdmin = enable })
		
		if enable && !sys.IsAdmin() {
			err := sys.RunAsAdmin(a.Cfg.ExePath(), a.Cfg.BaseDir(), "--restarting")
			if err == nil {
				a.SafeShutdown(nil)
				os.Exit(0)
			}
			a.Cfg.Update(func(c *domain.TrayConfig) { c.General.RunAsAdmin = false })
			a.ForcePushUIState()
			return
		}

	case domain.ActionToggleTun:
		enable := cmd.Payload == "true"
		
		a.Cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = enable })

		if enable && !sys.IsAdmin() {
			err := sys.RunAsAdmin(a.Cfg.ExePath(), a.Cfg.BaseDir(), "--restarting")
			if err == nil {
				a.SafeShutdown(nil)
				os.Exit(0)
			}
			a.Cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = false })
			a.ForcePushUIState()
			return
		}

		if enable {
			a.State.SetTunRequestedTime(time.Now())
		}

		a.State.SetConfigSyncing(true)
		go func() {
			defer a.State.SetConfigSyncing(false)
			tunPayload := map[string]interface{}{"enable": enable}
			
			if dev := a.State.GetActualTunDevice(); dev != "" {
				tunPayload["device"] = dev
			}
			
			reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			if err := a.API.SyncConfigToKernel(reqCtx, map[string]interface{}{"tun": tunPayload}); err != nil {
				a.Cfg.Update(func(c *domain.TrayConfig) { c.Config.Tun.Enable = !enable })
			}
			select {
			case a.apiPollCh <- struct{}{}:
			default:
			}
		}()

	case domain.ActionToggleProxy:
		enable := cmd.Payload == "true"
		a.Cfg.Update(func(c *domain.TrayConfig) {
			b := enable
			c.General.SystemProxy = &b
		})
		a.syncSystemProxy()

	case domain.ActionSwitchMode:
		a.Cfg.Update(func(c *domain.TrayConfig) { c.Config.Mode = cmd.Payload })
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
		
	case domain.ActionToggleAllowLan:
		enable := cmd.Payload == "true"
		a.Cfg.Update(func(c *domain.TrayConfig) {
			b := enable
			c.Config.AllowLan = &b
		})
		a.State.SetConfigSyncing(true)
		go func() {
			defer a.State.SetConfigSyncing(false)
			reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			_ = a.API.SyncConfigToKernel(reqCtx, map[string]interface{}{"allow-lan": enable})
			select {
			case a.apiPollCh <- struct{}{}:
			default:
			}
		}()
		
	case domain.ActionForceSyncAPI:
		select {
		case a.apiPollCh <- struct{}{}:
		default:
		}
		return

	case domain.ActionOpenWebUI:
		if a.State.GetPhase() != domain.PhaseRunning {
			slog.Warn("内核未就绪，无法打开 WebUI")
			break
		}

		cfg := a.Cfg.GetConfig()
		
		apiAddr, secret, uiName := a.State.GetWebUISnapshot()
		
		slog.Info("【打开面板】", "ApiAddr", apiAddr, "强制系统浏览器", *cfg.General.SystemBrowser)
		
		wcfg := webui.Config{
			APIAddr:            apiAddr,
			Secret:             secret,
			ProxyPort:          strconv.Itoa(a.Cfg.GetEffectivePort(cfg.Config.MixedPort, domain.DefaultMixedPort)),
			BaseDir:            a.Cfg.BaseDir(),
			UIName:             uiName,
			ForceSystemBrowser: *cfg.General.SystemBrowser, 
		}
		
		go webui.Launch(wcfg, a.webuiEventCh)

	case domain.ActionOpenBaseDir:
		_ = sys.ExecuteSystemCommand(a.Cfg.BaseDir())

	case domain.ActionReloadConfig:
		a.ReloadConfig(ctx)

	case domain.ActionRestartKernel:
		a.RestartKernel()

	case domain.ActionOpenConfigFile:
		targetRelPath := cmd.Payload
		if targetRelPath == "" {
			targetRelPath = a.Cfg.GetActivePath()
		}
		if err := a.safePreflightCheck(targetRelPath, "打开文件"); err != nil {
			break
		}
		absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(targetRelPath))
		_ = sys.ExecuteSystemCommand(absPath)

	case domain.ActionExitApp:
		webui.Cleanup()
		
	case domain.ActionToggleSystemBrowser:
		enable := cmd.Payload == "true"
		a.Cfg.Update(func(c *domain.TrayConfig) {
			b := enable
			c.General.SystemBrowser = &b
		})

	case domain.ActionEditCurrentConfig:
		targetRelPath := a.Cfg.GetActivePath()
		if targetRelPath == "" {
			ui.ShowErrorMessage(nil, "无法编辑", "当前没有正在运行的配置文件。")
			break
		}
		if err := a.safePreflightCheck(targetRelPath, "编辑配置"); err != nil {
			break
		}
		absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(targetRelPath))
		_ = sys.ExecuteSystemCommand(absPath)

	case domain.ActionCopyWebUIPassword:
		_, secret, _ := a.State.GetWebUISnapshot()

		if secret == "" {
			ui.ShowInfoMessage(nil, "复制密码", "当前 Web 面板无需密码即可访问。")
			break
		}
		if err := sys.WriteToClipboard(secret); err == nil {
			ui.ShowTrayNotification("密码复制成功", "Web 密码已复制到剪贴板，可直接粘贴使用。")
		} else {
			ui.ShowErrorMessage(nil, "复制失败", "无法向剪贴板写入密码：\n\n"+err.Error())
		}

	case domain.ActionClearWebUICache:
		cacheDir := filepath.Join(a.Cfg.BaseDir(), "webcache")
		err := os.RemoveAll(cacheDir)
		if err == nil {
			ui.ShowTrayNotification("清理完成", "Web 面板缓存目录已完成清理。")
		} else {
			ui.ShowErrorMessage(nil, "清理失败", "无法清除缓存目录，文件可能被占用：\n\n"+err.Error())
		}
	}

	a.pushUIState()
}
