package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"

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
		if ui.GlobalEngine != nil {
			ui.GlobalEngine.ShowProfileManager(a.GetUIStateSnapshot()) 
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
								go func() {
									if err := a.UpdateRemoteProfile(context.Background(), profile.Path, true, false); err != nil {
										ui.ShowErrorMessage(nil, "更新失败", err.Error())
									}
								}()
							}
							a.pushUIState()
						}
					}
				}
			}(p)
		}
		return
			
	case domain.ActionAddLocalProfile:
		go func(path string) {
			if err := a.ImportLocalProfile(ctx, path); err != nil {
				ui.ShowErrorMessage(nil, "导入失败", err.Error())
			}
		}(cmd.Payload)

	case domain.ActionAddRemoteProfile:
		go func(payload string) {
			if err := a.AddRemoteProfile(ctx, payload); err != nil {
				ui.ShowErrorMessage(nil, "添加订阅失败", err.Error())
			}
		}(cmd.Payload)

	case domain.ActionUpdateRemoteProfile:
		if p, ok := a.Cfg.GetProfileByPath(cmd.Payload); ok {
			go func(path string) {
				if err := a.UpdateRemoteProfile(ctx, path, true, false); err != nil {
					ui.ShowErrorMessage(nil, "更新失败", err.Error())
				}
			}(p.Path)
		}

	case domain.ActionSetProfileInterval:
		a.SetProfileInterval(cmd.Payload)

	case domain.ActionSwitchProfile:
		go func(path string) {
			if err := a.SwitchProfile(ctx, path); err != nil {
				ui.ShowErrorMessage(nil, "切换失败", err.Error())
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
			a.DeleteProfile(path)
		}(targetPath)

	case domain.ActionMoveProfileUp:
		a.Cfg.MoveProfile(cmd.Payload, -1)

	case domain.ActionMoveProfileDown:
		a.Cfg.MoveProfile(cmd.Payload, 1)

	case domain.ActionRequestEditPort:
		go func() {
			cfg := a.Cfg.GetConfig()
			cMixed := a.Cfg.GetEffectivePort(cfg.Config.MixedPort, domain.DefaultMixedPort)
			cSocks := a.Cfg.GetEffectivePort(cfg.Config.SocksPort, domain.DefaultSocksPort)
			cHttp := a.Cfg.GetEffectivePort(cfg.Config.Port, domain.DefaultPort)

			if ui.GlobalEngine != nil {
				nMixed, nSocks, nHttp, ok := ui.GlobalEngine.ShowPortEditor(cMixed, cSocks, cHttp)
				if ok && (nMixed != cMixed || nSocks != cSocks || nHttp != cHttp) {
					a.ApplyPortConfig(ctx, nMixed, nSocks, nHttp)
				}
			}
		}()
		
	case domain.ActionRequestEditController:
		go func() {
			cfg := a.Cfg.GetConfig()
			cAddr := cfg.Config.ExternalController
			if cAddr == "" { cAddr = domain.DefaultExternalController }
			cSec := ""
			if cfg.Config.Secret != nil { cSec = *cfg.Config.Secret }
			cOnline := cfg.General.RemoteWebUI != nil && *cfg.General.RemoteWebUI
			cSys := cfg.General.SystemBrowser != nil && *cfg.General.SystemBrowser

			if ui.GlobalEngine != nil {
				nAddr, nSec, nOnline, nSys, ok := ui.GlobalEngine.ShowControllerEditor(cAddr, cSec, cOnline, cSys)
				if ok {
					coreChanged := (cAddr != nAddr) || (cSec != nSec)
					appChanged := (cOnline != nOnline) || (cSys != nSys)
					if coreChanged || appChanged {
						a.ApplyControllerConfig(nAddr, nSec, nOnline, nSys, coreChanged)
					}
				}
			}
		}()

	case domain.ActionToggleAutoStart:
		if restarted := a.ToggleAutoStart(cmd.Payload == "true"); restarted && ui.GlobalEngine != nil {
			ui.GlobalEngine.Exit()
		}

	case domain.ActionToggleRunAsAdmin:
		if restarted := a.ToggleRunAsAdmin(cmd.Payload == "true"); restarted && ui.GlobalEngine != nil {
			ui.GlobalEngine.Exit()
		}

	case domain.ActionToggleTun:
		go func(enableStr string) {
			if restarted := a.ToggleTun(ctx, enableStr == "true"); restarted && ui.GlobalEngine != nil {
				ui.GlobalEngine.Exit()
			}
		}(cmd.Payload)

	case domain.ActionToggleProxy:
		a.ToggleProxy(cmd.Payload == "true")

	case domain.ActionSwitchMode:
		go a.SwitchMode(ctx, cmd.Payload)
		
	case domain.ActionToggleAllowLan:
		go a.ToggleAllowLan(ctx, cmd.Payload == "true")
		
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
		
		go a.WebUI.Launch(wcfg, a.webuiEventCh)

	case domain.ActionOpenBaseDir:
		_ = sys.ExecuteSystemCommand(a.Cfg.BaseDir())
		
	case domain.ActionOpenAppConfig:
		jsonPath := filepath.Join(a.Cfg.BaseDir(), domain.TrayConfigName)
		_ = sys.ExecuteSystemCommand(jsonPath)
		
	case domain.ActionReloadConfig:
		go func() {
			if err := a.ReloadConfig(ctx); err != nil {
				ui.ShowErrorMessage(nil, "重载失败", err.Error())
			}
		}()

	case domain.ActionRestartKernel:
		go func() {
			if err := a.RestartKernel(); err != nil {
				ui.ShowErrorMessage(nil, "重启失败", err.Error())
			}
		}()

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
		slog.Info("收到退出指令，准备安全销毁应用...")
		if ui.GlobalEngine != nil {
			ui.GlobalEngine.Exit()
		}
		return
		
	case domain.ActionToggleSystemBrowser:
		a.ToggleSystemBrowser(cmd.Payload == "true")

	case domain.ActionToggleRemoteWebUI:
		a.ToggleRemoteWebUI(cmd.Payload == "true")

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
			if !ui.ShowConfirmMessage(nil, "确认清理？", "清理缓存将同时清除面板个性化设置（如主题、布局等），且无法恢复。\n\n是否继续？") {
				return
			}
			
			if err := a.ClearWebUICache(); err == nil {
				ui.ShowTrayNotification("清理完成", "Web 面板缓存已清除。")
			} else {
				ui.ShowErrorMessage(nil, "清理失败", fmt.Sprintf("无法彻底清除缓存目录，文件可能正在被使用。\n\n错误: %v", err))
			}
		}()
	}

	a.pushUIState()
}
