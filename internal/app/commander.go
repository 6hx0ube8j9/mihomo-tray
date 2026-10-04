package app

import (
	"context"
	"log/slog"

	"mihomo-tray/internal/domain"
)

// asyncRun “语法糖”方法。
func (a *Application) asyncRun(title string, task func() error) {
	go func() {
		if err := task(); err != nil {
			if a.ui != nil {
				a.ui.ShowError(title, err.Error())
			}
		}
	}()
}

func (a *Application) handleUICommand(ctx context.Context, cmd domain.UICommand) {
	switch cmd.Action {

	// =====================================================================
	// 1. 配置文件操作 (对接 profile_ops.go)
	// =====================================================================
	case domain.ActionOpenProfileManager:
		if a.ui != nil {
			a.ui.ShowProfileManager(a.GetUIStateSnapshot())
		}

	case domain.ActionRequestAddLocal:
		go func() {
			if a.ui != nil {
				if selectedPath, ok := a.ui.OpenYAMLFileDialog(); ok {
					if err := a.ImportLocalProfile(ctx, selectedPath); err != nil {
						a.ui.ShowError("导入失败", err.Error())
					}
				}
			}
		}()

	case domain.ActionRequestAddRemote:
		go func() {
			if a.ui != nil {
				name, url, interval, ok := a.ui.ShowSubscriptionEditor("添加远程订阅", "", "", domain.DefaultUpdateInterval)
				if ok {
					if err := a.AddRemoteProfile(ctx, name, url, interval); err != nil {
						a.ui.ShowError("添加订阅失败", err.Error())
					}
				}
			}
		}()

	case domain.ActionRequestEditRemote:
		if profile, ok := a.GetProfileInfo(cmd.Payload); ok {
			go func(p domain.ProfileItem) {
				if a.ui != nil {
					name, url, interval, ok := a.ui.ShowSubscriptionEditor("编辑订阅信息", p.Name, p.URL, p.Interval)
					if ok {
						if err := a.EditRemoteProfile(ctx, p.Path, name, url, interval); err != nil {
							a.ui.ShowError("保存失败", err.Error())
						}
					}
				}
			}(profile)
		}

	case domain.ActionUpdateRemoteProfile:
		if p, ok := a.GetProfileInfo(cmd.Payload); ok {
			a.asyncRun("更新失败", func() error { return a.UpdateRemoteProfile(ctx, p.Path, true) })
		}

	case domain.ActionSetProfileInterval:
		a.SetProfileInterval(cmd.Payload)

	case domain.ActionSwitchProfile:
		a.asyncRun("切换失败", func() error { return a.SwitchProfile(ctx, cmd.Payload) })

	case domain.ActionRemoveProfile:
		targetPath := cmd.Payload
		go func(path string) {
			if a.ui != nil {
				if !a.ui.ShowConfirm("确认删除", "确定要删除此配置文件吗？\n\n此操作不可恢复，本地文件将被同时删除。") {
					return
				}
			}
			a.DeleteProfile(path)
		}(targetPath)

	case domain.ActionMoveProfileUp:
		a.MoveProfileUp(cmd.Payload)
		a.pushUIState()

	case domain.ActionMoveProfileDown:
		a.MoveProfileDown(cmd.Payload)
		a.pushUIState()

	// =====================================================================
	// 2. 内核动态参数配置 (对接 config_ops.go)
	// =====================================================================
	case domain.ActionRequestEditPort:
		go func() {
			if a.ui != nil {
				cMixed, cSocks, cHttp := a.GetPortConfigSnapshot()
				nMixed, nSocks, nHttp, ok := a.ui.ShowPortEditor(cMixed, cSocks, cHttp)
				if ok {
					a.ApplyPortConfig(ctx, nMixed, nSocks, nHttp)
				}
			}
		}()

	case domain.ActionRequestEditController:
		go func() {
			if a.ui != nil {
				cAddr, cSec, cOnline, cSys, cRemoteURL := a.GetControllerConfigSnapshot()
				nAddr, nSec, nOnline, nSys, nRemoteURL, ok := a.ui.ShowControllerEditor(cAddr, cSec, cOnline, cSys, cRemoteURL)
				if ok {
					a.ApplyControllerConfig(nAddr, nSec, nOnline, nSys, nRemoteURL)
				}
			}
		}()

	case domain.ActionToggleTun:
		go func(enableStr string) {
			restarted, err := a.ToggleTun(ctx, enableStr == "true")
			if restarted && a.ui != nil {
				a.ui.Exit()
				return
			}
			
			a.pushUIState()

			if err != nil && a.ui != nil {
				a.ui.ShowError("TUN 设置失败", err.Error())
			}
		}(cmd.Payload)

	case domain.ActionToggleProxy:
		a.asyncRun("系统代理设置失败", func() error {
			a.ToggleProxy(cmd.Payload == "true")
			a.pushUIState()
			return nil
		})

	case domain.ActionSwitchMode:
		go func() {
			a.SwitchMode(ctx, cmd.Payload)
			a.pushUIState()
		}()

	case domain.ActionToggleAllowLan:
		go func() {
			a.ToggleAllowLan(ctx, cmd.Payload == "true")
			a.pushUIState()
		}()

	case domain.ActionForceSyncAPI:
		a.ForceSyncAPI()

	// =====================================================================
	// 3. 操作系统与生命周期控制 (对接 sys_ops.go & transaction.go)
	// =====================================================================
	case domain.ActionOpenBaseDir:
		a.OpenBaseDir()

	case domain.ActionOpenAppConfig:
		a.OpenAppConfig()

	case domain.ActionOpenConfigFile:
		a.asyncRun("打开文件失败", func() error { return a.OpenConfigFile(cmd.Payload) })

	case domain.ActionEditCurrentConfig:
		a.asyncRun("无法编辑", func() error { return a.EditCurrentConfig() })

	case domain.ActionToggleAutoStart:
		if restarted := a.ToggleAutoStart(cmd.Payload == "true"); restarted && a.ui != nil {
			a.ui.Exit()
		}
		a.pushUIState()

	case domain.ActionToggleRunAsAdmin:
		if restarted := a.ToggleRunAsAdmin(cmd.Payload == "true"); restarted && a.ui != nil {
			a.ui.Exit()
		}
		a.pushUIState()

	case domain.ActionReloadConfig:
		a.asyncRun("重载失败", func() error { return a.ReloadConfig(ctx) })

	case domain.ActionRestartKernel:
		a.asyncRun("重启失败", func() error { return a.RestartKernel(ctx) })

	case domain.ActionExitApp:
		slog.Info("收到退出指令，准备安全销毁应用...")
		if a.ui != nil {
			a.ui.Exit()
		}

	// =====================================================================
	// 4. Web 面板与偏好设置 (对接 webui_ops.go & config_ops.go)
	// =====================================================================
	case domain.ActionToggleSystemBrowser:
		a.ToggleSystemBrowser(cmd.Payload == "true")
		a.pushUIState()

	case domain.ActionToggleRemoteWebUI:
		a.ToggleRemoteWebUI(cmd.Payload == "true")
		a.pushUIState()

	case domain.ActionOpenWebUI:
		if err := a.OpenWebUI(); err != nil {
			if a.ui != nil {
				a.ui.ShowError("提示", err.Error())
			}
		}

	case domain.ActionCopyWebUIPassword:
		if err := a.CopyWebUIPassword(); err != nil {
			if a.ui != nil {
				a.ui.ShowInfo("复制密码", err.Error())
			}
		} else {
			if a.ui != nil {
				a.ui.ShowNotification("复制成功", "Web 密码文本已复制到剪贴板。")
			}
		}

	case domain.ActionClearWebUICache:
		go func() {
			if a.ui != nil {
				if !a.ui.ShowConfirm("确认清理？", "清理缓存将同时清除面板个性化设置（如主题、布局等），且无法恢复。\n\n是否继续？") {
					return
				}
			}
			if err := a.ClearWebUICache(); err == nil {
				if a.ui != nil {
					a.ui.ShowNotification("清理完成", "Web 面板缓存已清除。")
				}
			} else {
				if a.ui != nil {
					a.ui.ShowError("清理失败", err.Error())
				}
			}
		}()
	}

}
