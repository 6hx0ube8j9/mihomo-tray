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
		ui.ShowErrorMessage(nil, actionTitle+"失败", fmt.Sprintf("目标配置异常，请求已取消。\n\n错误: %v", err))
		return err
	}
	return nil
}

func (a *Application) handleUICommand(ctx context.Context, cmd domain.UICommand) {
	switch cmd.Action {
		
	case domain.ActionOpenProfileManager:
		a.uiStateMutex.Lock()
		stateSnapshot := a.lastUIState
		items := make([]domain.UIProfileItem, len(a.lastUIState.ProfileItems))
		copy(items, a.lastUIState.ProfileItems)
		
		stateSnapshot.ProfileItems = items 
		a.uiStateMutex.Unlock()

		if ui.GlobalEngine != nil {
			ui.GlobalEngine.ShowProfileManager(stateSnapshot) 
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
			if ui.GlobalEngine != nil {
				name, url, interval, ok := ui.GlobalEngine.ShowSubscriptionEditor("添加远程订阅", "", "", domain.DefaultUpdateInterval)
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
				if ui.GlobalEngine != nil {
					name, url, interval, ok := ui.GlobalEngine.ShowSubscriptionEditor("编辑订阅信息", profile.Name, profile.URL, profile.Interval)
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
		
    case domain.ActionRequestEditPort:
		go func() {
			cfg := a.Cfg.GetConfig()

			currentMixed := a.Cfg.GetEffectivePort(cfg.Config.MixedPort, domain.DefaultMixedPort)
			currentSocks := a.Cfg.GetEffectivePort(cfg.Config.SocksPort, domain.DefaultSocksPort)
			currentHttp := a.Cfg.GetEffectivePort(cfg.Config.Port, domain.DefaultPort)

			if ui.GlobalEngine != nil {
				newMixed, newSocks, newHttp, ok := ui.GlobalEngine.ShowPortEditor(currentMixed, currentSocks, currentHttp)
				
				if ok && (newMixed != currentMixed || newSocks != currentSocks || newHttp != currentHttp) {
					
					a.Cfg.Update(func(c *domain.TrayConfig) {
						m, s, h := newMixed, newSocks, newHttp
						c.Config.MixedPort = &m
						c.Config.SocksPort = &s
						c.Config.Port = &h
					})

					a.pushUIState()

					if *a.Cfg.GetConfig().General.SystemProxy {
						a.syncSystemProxy()
					}

					a.State.SetConfigSyncing(true)
					
					go func() {
						defer a.State.SetConfigSyncing(false)
						
						if a.State.GetPhase() == domain.PhaseRunning {
							reqCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
							defer cancel()
							
							payload := map[string]interface{}{
								"mixed-port": newMixed,
								"socks-port": newSocks,
								"port":       newHttp,
							}
							
							if err := a.API.SyncConfigToKernel(reqCtx, payload); err != nil {
								slog.Warn("热刷端口到内核失败，等待下次内核重载生效", "err", err)
							}
						}
						
						select {
						case a.apiPollCh <- struct{}{}:
						default:
						}
					}()
				}
			}
		}()
		return
		
    case domain.ActionRequestEditController:
		go func() {
			cfg := a.Cfg.GetConfig()

			currAddr := cfg.Config.ExternalController
			if currAddr == "" { currAddr = domain.DefaultExternalController }
			
			currSecret := ""
			if cfg.Config.Secret != nil { currSecret = *cfg.Config.Secret }
			
			currOnline := false
			if cfg.General.RemoteWebUI != nil { currOnline = *cfg.General.RemoteWebUI }
			
			currSysBrowser := false
			if cfg.General.SystemBrowser != nil { currSysBrowser = *cfg.General.SystemBrowser }

			if ui.GlobalEngine != nil {
				newAddr, newSecret, newOnline, newSysBrowser, ok := ui.GlobalEngine.ShowControllerEditor(
					currAddr, currSecret, currOnline, currSysBrowser,
				)
				
				if ok {
					coreChanged := (currAddr != newAddr) || (currSecret != newSecret)
					appChanged := (currOnline != newOnline) || (currSysBrowser != newSysBrowser)

					if !coreChanged && !appChanged {
						return
					}
					
					a.Cfg.Update(func(c *domain.TrayConfig) {
						if coreChanged {
							c.Config.ExternalController = newAddr
							c.Config.Secret = &newSecret
						}
						if appChanged {
							bOnline, bSys := newOnline, newSysBrowser
							c.General.RemoteWebUI = &bOnline
							c.General.SystemBrowser = &bSys
						}
					})

					a.pushUIState()

					if coreChanged {
						slog.Info("Web 面板核心网络参数已变更，重启内核生效")
						a.RestartKernel() 
					} else if appChanged {
						slog.Info("Web 面板应用偏好已保存")
						ui.GlobalEngine.ShowTrayNotification("提示", "Web 面板偏好设置已保存生效")
					}
				}
			}
		}()
		return
		
	case domain.ActionAddLocalProfile:
		if a.State.IsProfileSwitching() {
			break
		}
		a.State.SetProfileSwitching(true)

		go func(sourcePath string) {
			defer a.pushUIState()
			defer a.State.SetProfileSwitching(false)

			exePath := core.GetKernelPath(a.Cfg.BaseDir())
			if err := core.ValidateConfig(exePath, a.Cfg.BaseDir(), sourcePath); err != nil {
				ui.ShowErrorMessage(nil, "导入失败", fmt.Sprintf("配置文件存在语法或规则错误。\n\n错误: %v", err))
				return
			}

			targetName, _, err := a.Cfg.SafeCopyUntrustedConfig(sourcePath)
			if err != nil {
				ui.ShowErrorMessage(nil, "导入失败", fmt.Sprintf("文件复制失败，请检查系统权限。\n\n错误: %v", err))
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
					slog.Info("修改订阅更新频率", "path", targetPath, "interval", interval)
				}
			}
		}

	case domain.ActionUpdateRemoteProfile:
		if p, ok := a.Cfg.GetProfileByPath(cmd.Payload); ok {
			go a.executeRemoteUpdate(ctx, p.Path, true, false)
		}

	case domain.ActionSwitchProfile:
		if cmd.Payload != "" && cmd.Payload == a.Cfg.GetActivePath() {
			slog.Debug("配置已在使用中，忽略重复切换", "path", cmd.Payload)
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
					slog.Debug("配置切换失败，恢复原状态并刷新界面")
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
				ui.ShowErrorMessage(nil, "加载失败", fmt.Sprintf("该配置存在错误，拒绝加载。\n\n错误: %v", err))
				isTransactionFailed = true
				return
			}

			oldActive := a.Cfg.GetActivePath()
			a.Cfg.SetActiveProfile(target)
			a.pushUIState()

			if err := a.applyConfigTransaction(context.Background(), target); err != nil {
				ui.ShowErrorMessage(nil, "内核异常", fmt.Sprintf("配置加载失败，已自动恢复原配置。\n\n错误: %v", err))
				a.Cfg.SetActiveProfile(oldActive)
				isTransactionFailed = true
			} else {
				a.restartWebUIIfOpen()
			}
		}(cmd.Payload)

	case domain.ActionRemoveProfile:
		targetPath := cmd.Payload
		if targetPath == a.Cfg.GetActivePath() {
			slog.Warn("拒绝删除当前正在使用的配置")
			break
		}

		go func(path string) {
			if !ui.ShowConfirmMessage(nil, "确认删除", "确定要删除此配置文件吗？\n\n此操作不可恢复，本地文件将被同时删除。") {
				return
			}

			absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(path))
			if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
				slog.Warn("清理本地文件失败", "path", absPath, "err", err)
			}

			a.Cfg.RemoveProfile(path)
			a.pushUIState()
		}(targetPath)

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
			slog.Info("修改自启状态需要管理员权限，正在申请")
			err := sys.RunAsAdmin(a.Cfg.ExePath(), a.Cfg.BaseDir(), "--restarting")
			if sys.IsUserCancelled(err) {
				slog.Info("用户取消授权，操作已取消")
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
			slog.Info("设置始终以管理员运行，正在申请权限")
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
			slog.Info("开启 TUN 模式需要管理员权限，正在申请")
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
			slog.Warn("内核未启动完成，暂无法打开 WebUI")
			break
		}

		cfg := a.Cfg.GetConfig()
		apiAddr, secret, uiName := a.State.GetWebUISnapshot()
		
		slog.Info("正在打开面板", "强制系统浏览器", *cfg.General.SystemBrowser, "使用远程面板", *cfg.General.RemoteWebUI)
		
		wcfg := webui.Config{
			APIAddr:            apiAddr,
			Secret:             secret,
			ProxyPort:          strconv.Itoa(a.Cfg.GetEffectivePort(cfg.Config.MixedPort, domain.DefaultMixedPort)),
			BaseDir:            a.Cfg.BaseDir(),
			UIName:             uiName,
			ForceSystemBrowser: *cfg.General.SystemBrowser, 
			RemoteWebUI:        *cfg.General.RemoteWebUI,
		}
		
		go webui.Launch(wcfg, a.webuiEventCh)

	case domain.ActionOpenBaseDir:
		_ = sys.ExecuteSystemCommand(a.Cfg.BaseDir())
		
	case domain.ActionOpenAppConfig:
		jsonPath := filepath.Join(a.Cfg.BaseDir(), domain.TrayConfigName)
		_ = sys.ExecuteSystemCommand(jsonPath)
		
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

	case domain.ActionToggleRemoteWebUI:
		enable := cmd.Payload == "true"
		a.Cfg.Update(func(c *domain.TrayConfig) {
			b := enable
			c.General.RemoteWebUI = &b
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
			ui.ShowTrayNotification("复制成功", "Web 密码已复制到剪贴板。")
		} else {
			ui.ShowErrorMessage(nil, "复制失败", fmt.Sprintf("无法写入系统剪贴板。\n\n错误: %v", err))
		}

	case domain.ActionClearWebUICache:
		go func() {
			if !ui.ShowConfirmMessage(nil, "确认清理？", "清理 Web 面板缓存将同时清除所有面板设置（如主题、布局等），且无法恢复。\n\n是否继续？") {
				return
			}
			
			cacheDir := filepath.Join(a.Cfg.BaseDir(), "webcache")
			err := os.RemoveAll(cacheDir)
			if err == nil {
				ui.ShowTrayNotification("清理完成", "Web 面板缓存已清除。")
			} else {
				ui.ShowErrorMessage(nil, "清理失败", fmt.Sprintf("无法彻底清除缓存目录，文件可能正在被使用。\n\n错误: %v", err))
			}
		}()
	}

	a.pushUIState()
}
