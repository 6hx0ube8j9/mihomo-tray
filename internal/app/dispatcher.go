package app

import (
	"context"
	"log/slog"

	"mihomo-tray/internal/domain"
)

func (a *Application) asyncRun(title string, task func() error) {
	go func() {
		if err := task(); err != nil {
			slog.Error(title+"失败", "err", err)
			if a.ui != nil {
				a.ui.ShowError(title+"失败", err.Error())
			}
		}
	}()
}

func (a *Application) handleUICommand(ctx context.Context, cmd domain.UICommand) {
	switch cmd.Action {
	// ================= 配置与订阅 (ProfileService) =================
	case domain.ActionOpenProfileManager:
		if a.ui != nil {
			a.ui.ShowProfileManager(a.Presenter.GetSnapshot())
		}
	case domain.ActionRequestAddLocal:
		go func() {
			if a.ui != nil {
				if path, ok := a.ui.OpenYAMLFileDialog(); ok {
					if err := a.Profiles.ImportLocalProfile(ctx, path); err != nil {
						slog.Error("导入本地配置失败", "err", err)
						a.ui.ShowError("导入配置", err.Error())
					} else {
						a.restartWebUIIfOpen()
					}
				}
			}
		}()
	case domain.ActionRequestAddRemote:
		go func() {
			if a.ui != nil {
				name, url, interval, ok := a.ui.ShowProfileInfoEditor("添加远程订阅", "", "", domain.DefaultUpdateInterval, true)
				if ok {
					if err := a.Profiles.AddRemoteProfile(ctx, name, url, interval); err != nil {
						slog.Error("添加订阅失败", "err", err)
						a.ui.ShowError("添加订阅", err.Error())
					} else {
						a.restartWebUIIfOpen()
					}
				}
			}
		}()
	case domain.ActionEditProfileInfo:
		if p, ok := a.Profiles.GetProfileInfo(cmd.Payload); ok {
			go func(profile domain.ProfileItem) {
				if a.ui == nil {
					return
				}
				isRemote := profile.URL != ""
				name, url, interval, ok := a.ui.ShowProfileInfoEditor("编辑信息", profile.Name, profile.URL, profile.Interval, isRemote)
				if !ok {
					return
				}
				var err error
				if isRemote {
					err = a.Profiles.EditRemoteProfile(ctx, profile.Path, name, url, interval)
				} else {
					err = a.Profiles.EditLocalProfile(ctx, profile.Path, name)
				}
				if err != nil {
					slog.Error("保存配置信息失败", "err", err)
					a.ui.ShowError("保存配置", err.Error())
				}
			}(p)
		}
	case domain.ActionUpdateRemoteProfile:
		if p, ok := a.Profiles.GetProfileInfo(cmd.Payload); ok {
			a.asyncRun("更新订阅", func() error { return a.Profiles.UpdateRemoteProfile(ctx, p.Path, true) })
		}
	case domain.ActionSetProfileInterval:
		a.Profiles.SetProfileInterval(cmd.Payload)
	case domain.ActionSwitchProfile:
		a.asyncRun("切换配置", func() error {
			if err := a.Profiles.SwitchProfile(ctx, cmd.Payload); err != nil {
				return err
			}
			a.restartWebUIIfOpen()
			return nil
		})
	case domain.ActionRemoveProfile:
		targetPath := cmd.Payload
		go func(path string) {
			if a.ui != nil && !a.ui.ShowConfirm("删除配置", "确认删除该配置文件？本地文件将被清理且不可恢复。") {
				return
			}
			a.asyncRun("删除配置", func() error { return a.Profiles.DeleteProfile(ctx, path) })
		}(targetPath)
	case domain.ActionMoveProfileUp:
		a.Profiles.MoveProfileUp(cmd.Payload)
		a.Presenter.PushUIState()
	case domain.ActionMoveProfileDown:
		a.Profiles.MoveProfileDown(cmd.Payload)
		a.Presenter.PushUIState()

	// ================= 系统与底层调度 (Deployer & SysOps) =================
	case domain.ActionReloadConfig:
		a.asyncRun("重载配置", func() error {
			if err := a.Deployer.ReloadConfig(ctx); err != nil {
				return err
			}
			a.CheckAndReconcilePrivileges(false)
			a.restartWebUIIfOpen()
			return nil
		})
	case domain.ActionRestartKernel:
		a.asyncRun("重启内核", func() error {
			if err := a.Deployer.RestartKernel(ctx); err != nil {
				return err
			}
			a.CheckAndReconcilePrivileges(false)
			a.restartWebUIIfOpen()
			return nil
		})
	case domain.ActionOpenBaseDir:
		a.OpenBaseDir()
	case domain.ActionOpenAppConfig:
		a.OpenAppConfig()
	case domain.ActionOpenConfigFile:
		a.asyncRun("打开配置", func() error { return a.OpenConfigFile(cmd.Payload) })
	case domain.ActionEditCurrentConfig:
		a.asyncRun("编辑配置", func() error { return a.EditCurrentConfig() })
	case domain.ActionToggleAutoStart:
		if a.ToggleAutoStart(cmd.Payload == "true") && a.ui != nil {
			a.ui.Exit()
		}
		a.Presenter.PushUIState()
	case domain.ActionToggleRunAsAdmin:
		if a.ToggleRunAsAdmin(cmd.Payload == "true") && a.ui != nil {
			a.ui.Exit()
		}
		a.Presenter.PushUIState()
	case domain.ActionExitApp:
		if a.ui != nil {
			a.ui.Exit()
		}
	case domain.ActionForceSyncAPI:
		a.ForceSyncAPI()

	// ================= 运行参数调节 (SettingsService) =================
	case domain.ActionToggleProxy:
		a.Settings.ToggleProxy(cmd.Payload == "true")
	case domain.ActionToggleTun:
		a.asyncRun("TUN 模式", func() error {
			restarted, err := a.Settings.ToggleTun(ctx, cmd.Payload == "true")
			if err == nil && restarted && a.ui != nil {
				a.ui.Exit()
			}
			return err
		})
	case domain.ActionSwitchMode:
		a.asyncRun("路由模式", func() error { return a.Settings.SwitchMode(ctx, cmd.Payload) })
	case domain.ActionToggleAllowLan:
		a.asyncRun("局域网代理", func() error { return a.Settings.ToggleAllowLan(ctx, cmd.Payload == "true") })
	case domain.ActionRequestEditPort:
		go func() {
			if a.ui != nil {
				cMixed, cSocks, cHttp := a.Settings.GetPortSnapshot()
				nMixed, nSocks, nHttp, ok := a.ui.ShowPortEditor(cMixed, cSocks, cHttp)
				if ok {
					a.asyncRun("端口设置", func() error {
						return a.Settings.ApplyPortConfig(ctx, nMixed, nSocks, nHttp)
					})
				}
			}
		}()

	// ================= Web面板交互 (WebUI & Settings) =================
	case domain.ActionOpenWebUI:
		a.asyncRun("打开面板", func() error { return a.OpenWebUI() })
	case domain.ActionRequestEditController:
		go func() {
			if a.ui != nil {
				cAddr, cSec, cOnline, cSys, cRemoteURL := a.Settings.GetControllerSnapshot()
				nAddr, nSec, nOnline, nSys, nRemoteURL, ok := a.ui.ShowControllerEditor(cAddr, cSec, cOnline, cSys, cRemoteURL)
				if ok {
					a.asyncRun("面板设置", func() error {
						restarted, err := a.Settings.ApplyControllerConfig(ctx, nAddr, nSec, nOnline, nSys, nRemoteURL)
						if err == nil && restarted {
							a.restartWebUIIfOpen()
						}
						return err
					})
				}
			}
		}()
	case domain.ActionToggleSystemBrowser:
		a.Settings.ToggleSystemBrowser(cmd.Payload == "true")
	case domain.ActionToggleRemoteWebUI:
		a.Settings.ToggleRemoteWebUI(cmd.Payload == "true")
	case domain.ActionCopyWebUIPassword:
		if err := a.CopyWebUIPassword(); err != nil {
			if a.ui != nil {
				a.ui.ShowError("复制密码", err.Error())
			}
		} else if a.ui != nil {
			a.ui.ShowNotification("复制成功", "密码已复制到剪贴板。")
		}
	case domain.ActionClearWebUICache:
		go func() {
			if a.ui != nil && !a.ui.ShowConfirm("清理缓存", "将清除面板的本地设置与缓存，是否继续？") {
				return
			}
			a.asyncRun("清理缓存", func() error {
				if err := a.ClearWebUICache(); err != nil {
					return err
				}
				if a.ui != nil {
					a.ui.ShowNotification("清理完成", "面板缓存已清除。")
				}
				return nil
			})
		}()
	}
}
