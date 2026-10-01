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
				if err := a.ImportLocalProfile(ctx, selectedPath); err != nil {
					ui.ShowErrorMessage(nil, "导入失败", err.Error())
				}
			}
		}()
		return

	case domain.ActionRequestAddRemote:
		go func() {
			if ui.GlobalEngine != nil {
				name, url, interval, ok := ui.GlobalEngine.ShowSubscriptionEditor("添加远程订阅", "", "", domain.DefaultUpdateInterval)
				if ok {
					if err := a.AddRemoteProfile(ctx, name, url, interval); err != nil {
						ui.ShowErrorMessage(nil, "添加订阅失败", err.Error())
					}
				}
			}
		}()
		return

	case domain.ActionRequestEditRemote:
		if profile, ok := a.GetProfileInfo(cmd.Payload); ok {
			go func(p domain.ProfileItem) {
				if ui.GlobalEngine != nil {
					name, url, interval, ok := ui.GlobalEngine.ShowSubscriptionEditor(
						"编辑订阅信息", p.Name, p.URL, p.Interval,
					)
					if ok {
						if err := a.EditRemoteProfile(ctx, p.Path, name, url, interval); err != nil {
							ui.ShowErrorMessage(nil, "保存失败", err.Error())
						}
					}
				}
			}(profile)
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
		a.MoveProfileUp(cmd.Payload)

	case domain.ActionMoveProfileDown:
		a.MoveProfileDown(cmd.Payload)

	case domain.ActionRequestEditPort:
		go func() {
			if ui.GlobalEngine != nil {
				cMixed, cSocks, cHttp := a.GetPortConfigSnapshot()
				nMixed, nSocks, nHttp, ok := ui.GlobalEngine.ShowPortEditor(cMixed, cSocks, cHttp)
				if ok {
					a.ApplyPortConfig(ctx, nMixed, nSocks, nHttp)
				}
			}
		}()
		
	case domain.ActionRequestEditController:
		go func() {
			if ui.GlobalEngine != nil {
				cAddr, cSec, cOnline, cSys := a.GetControllerConfigSnapshot()
				nAddr, nSec, nOnline, nSys, ok := ui.GlobalEngine.ShowControllerEditor(cAddr, cSec, cOnline, cSys)
				if ok {
					a.ApplyControllerConfig(nAddr, nSec, nOnline, nSys)
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
		a.ForceSyncAPI()
		return

	case domain.ActionOpenBaseDir:
		a.OpenBaseDir()

	case domain.ActionOpenAppConfig:
		a.OpenAppConfig()

	case domain.ActionOpenConfigFile:
		if err := a.OpenConfigFile(cmd.Payload); err != nil {
			ui.ShowErrorMessage(nil, "打开文件失败", err.Error())
		}

	case domain.ActionEditCurrentConfig:
		if err := a.EditCurrentConfig(); err != nil {
			ui.ShowErrorMessage(nil, "无法编辑", err.Error())
		}
		
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
		
    case domain.ActionOpenWebUI:
		if err := a.OpenWebUI(); err != nil {
			ui.ShowErrorMessage(nil, "提示", err.Error())
		}

	case domain.ActionCopyWebUIPassword:
		if err := a.CopyWebUIPassword(); err != nil {
			ui.ShowInfoMessage(nil, "复制密码", err.Error())
		} else {
			ui.ShowTrayNotification("复制成功", "Web 密码已复制到剪贴板。")
		}

	case domain.ActionClearWebUICache:
		go func() {
			if !ui.ShowConfirmMessage(nil, "确认清理？", "清理缓存将同时清除面板个性化设置（如主题、布局等），且无法恢复。\n\n是否继续？") {
				return
			}
			
			if err := a.ClearWebUICache(); err == nil {
				ui.ShowTrayNotification("清理完成", "Web 面板缓存已清除。")
			} else {
				ui.ShowErrorMessage(nil, "清理失败", err.Error())
			}
		}()

	a.pushUIState()
}
