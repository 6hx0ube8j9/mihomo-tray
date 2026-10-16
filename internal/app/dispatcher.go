package app

import (
	"context"
	"log/slog"
	"time"

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

func (a *Application) tryRestartWebUIIfOpen() {
	if a.WebUI.IsActive() {
		a.WebUI.Cleanup()
		go func() {
			for i := 0; i < 60; i++ {
				if a.State.IsExiting() {
					return
				}
				if a.State.GetPhase() == domain.PhaseRunning {
					a.UICommandCh <- domain.UICommand{Action: domain.ActionOpenWebUI}
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
		}()
	}
}

func (a *Application) handleUICommand(ctx context.Context, cmd domain.UICommand) {
	switch cmd.Action {
	
	// ================= 订阅与配置管理 (ProfileHub) =================
	case domain.ActionOpenProfileManager:
		if a.ui != nil {
			a.ui.ShowProfileManager(a.Presenter.GetSnapshot())
		}
	case domain.ActionRequestAddLocal:
		go func() {
			if a.ui != nil {
				if path, ok := a.ui.OpenYAMLFileDialog(); ok {
					if err := a.ProfileHub.ImportLocalProfile(ctx, path); err != nil {
						slog.Error("导入本地配置失败", "err", err)
						a.ui.ShowError("导入配置", err.Error())
					} else {
						a.tryRestartWebUIIfOpen()
					}
				}
			}
		}()
	case domain.ActionRequestAddRemote:
		go func() {
			if a.ui != nil {
				name, url, interval, ok := a.ui.ShowProfileInfoEditor("添加远程订阅", "", "", domain.DefaultUpdateInterval, true)
				if ok {
					if err := a.ProfileHub.AddRemoteProfile(ctx, name, url, interval); err != nil {
						slog.Error("添加订阅失败", "err", err)
						a.ui.ShowError("添加订阅", err.Error())
					} else {
						a.tryRestartWebUIIfOpen()
					}
				}
			}
		}()
	case domain.ActionEditProfileInfo:
		if p, ok := a.ProfileHub.GetProfileInfo(cmd.Payload); ok {
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
					err = a.ProfileHub.EditRemoteProfile(ctx, profile.Path, name, url, interval)
				} else {
					err = a.ProfileHub.EditLocalProfile(ctx, profile.Path, name)
				}
				if err != nil {
					slog.Error("保存配置信息失败", "err", err)
					a.ui.ShowError("保存配置", err.Error())
				}
			}(p)
		}
	case domain.ActionUpdateRemoteProfile:
		if p, ok := a.ProfileHub.GetProfileInfo(cmd.Payload); ok {
			a.asyncRun("更新订阅", func() error { return a.ProfileHub.UpdateRemoteProfile(ctx, p.Path, true) })
		}
	case domain.ActionSetProfileInterval:
		a.ProfileHub.SetProfileInterval(cmd.Payload)
		a.Presenter.PushUIState()
	case domain.ActionSwitchProfile:
		a.asyncRun("切换配置", func() error {
			if err := a.ProfileHub.SwitchProfile(ctx, cmd.Payload); err != nil {
				return err
			}
			a.tryRestartWebUIIfOpen()
			return nil
		})
	case domain.ActionRemoveProfile:
		targetPath := cmd.Payload
		go func(path string) {
			if a.ui != nil && !a.ui.ShowConfirm("删除配置", "确认删除该配置文件？本地文件将被清理且不可恢复。") {
				return
			}
			a.asyncRun("删除配置", func() error { return a.ProfileHub.DeleteProfile(ctx, path) })
		}(targetPath)
	case domain.ActionMoveProfileUp:
		a.ProfileHub.MoveProfileUp(cmd.Payload)
		a.Presenter.PushUIState()
	case domain.ActionMoveProfileDown:
		a.ProfileHub.MoveProfileDown(cmd.Payload)
		a.Presenter.PushUIState()

	// ================= 运行参数调优 (Tuner) =================
	case domain.ActionToggleProxy:
		a.asyncRun("系统代理", func() error {
			a.Tuner.ToggleProxy(cmd.Payload == "true")
			return nil
		})
	case domain.ActionToggleTun:
		a.asyncRun("TUN 模式", func() error {
			restarted, err := a.Tuner.ToggleTun(ctx, cmd.Payload == "true", a.HostBridge.ElevatePrivilege)
			if err == nil && restarted && a.ui != nil {
				a.ui.Exit()
			}
			return err
		})
	case domain.ActionSwitchMode:
		a.asyncRun("路由模式", func() error { return a.Tuner.SwitchMode(ctx, cmd.Payload) })
	case domain.ActionToggleAllowLan:
		a.asyncRun("局域网代理", func() error { return a.Tuner.ToggleAllowLan(ctx, cmd.Payload == "true") })
	case domain.ActionRequestEditPort:
		go func() {
			if a.ui != nil {
				cMixed, cSocks, cHttp := a.Tuner.GetPortSnapshot()
				nMixed, nSocks, nHttp, ok := a.ui.ShowPortEditor(cMixed, cSocks, cHttp)
				if ok {
					a.asyncRun("端口设置", func() error {
						return a.Tuner.ApplyPortConfig(ctx, nMixed, nSocks, nHttp)
					})
				}
			}
		}()

	// ================= Web 面板管理 (Panel) =================
	case domain.ActionOpenWebUI:
		a.asyncRun("打开面板", func() error { return a.Panel.Open(a.ForceSyncAPI) })
	case domain.ActionRequestEditController:
		go func() {
			if a.ui != nil {
				cAddr, cSec, cOnline, cSys, cRemoteURL := a.Tuner.GetControllerSnapshot()
				nAddr, nSec, nOnline, nSys, nRemoteURL, ok := a.ui.ShowControllerEditor(cAddr, cSec, cOnline, cSys, cRemoteURL)
				if ok {
					a.asyncRun("面板设置", func() error {
						restarted, err := a.Tuner.ApplyControllerConfig(ctx, nAddr, nSec, nOnline, nSys, nRemoteURL)
						if err == nil && restarted {
							a.tryRestartWebUIIfOpen()
						}
						return err
					})
				}
			}
		}()
	case domain.ActionToggleSystemBrowser:
		a.Tuner.ToggleSystemBrowser(cmd.Payload == "true")
	case domain.ActionToggleRemoteWebUI:
		a.Tuner.ToggleRemoteWebUI(cmd.Payload == "true")
	case domain.ActionCopyWebUIPassword:
		if err := a.Panel.CopyPassword(); err != nil {
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
				if err := a.Panel.ClearCache(); err != nil {
					return err
				}
				if a.ui != nil {
					a.ui.ShowNotification("清理完成", "面板缓存已清除。")
				}
				return nil
			})
		}()

	// ================= 系统特权与文件资源 (HostBridge / Deployer) =================
	case domain.ActionReloadConfig:
		a.asyncRun("重载配置", func() error {
			if err := a.Deployer.ReloadConfig(ctx); err != nil {
				return err
			}
			a.HostBridge.CheckAndReconcilePrivileges(false)
			a.tryRestartWebUIIfOpen()
			return nil
		})
	case domain.ActionRestartKernel:
		a.asyncRun("重启内核", func() error {
			if err := a.Deployer.RestartKernel(ctx); err != nil {
				return err
			}
			a.HostBridge.CheckAndReconcilePrivileges(false)
			a.tryRestartWebUIIfOpen()
			return nil
		})
	case domain.ActionOpenBaseDir:
		a.HostBridge.OpenBaseDir()
	case domain.ActionOpenAppConfig:
		a.HostBridge.OpenAppConfig()
	case domain.ActionOpenConfigFile:
		a.asyncRun("打开配置", func() error { return a.HostBridge.OpenConfigFile(cmd.Payload) })
	case domain.ActionEditCurrentConfig:
		a.asyncRun("编辑配置", func() error { return a.HostBridge.EditCurrentConfig() })
	case domain.ActionToggleAutoStart:
		if a.HostBridge.ToggleAutoStart(cmd.Payload == "true") && a.ui != nil {
			a.ui.Exit()
		}
		a.Presenter.PushUIState()
	case domain.ActionToggleRunAsAdmin:
		if a.HostBridge.ToggleRunAsAdmin(cmd.Payload == "true") && a.ui != nil {
			a.ui.Exit()
		}
		a.Presenter.PushUIState()
	case domain.ActionExitApp:
		if a.ui != nil {
			a.ui.Exit()
		}
	case domain.ActionForceSyncAPI:
		a.ForceSyncAPI()
	}
}
