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
			slog.Error("任务执行失败", "action", title, "err", err)
			if a.ui != nil {
				a.ui.ShowError(title, err.Error())
			}
		}
	}()
}

func (a *Application) handleUICommand(ctx context.Context, cmd domain.UICommand) {
	switch cmd.Action {
	case domain.ActionOpenProfileManager:
		if a.ui != nil {
			a.ui.ShowProfileManager(a.GetUIStateSnapshot())
		}

	case domain.ActionRequestAddLocal:
		go func() {
			if a.ui != nil {
				if selectedPath, ok := a.ui.OpenYAMLFileDialog(); ok {
					if err := a.ImportLocalProfile(ctx, selectedPath); err != nil {
						slog.Error("导入本地配置失败", "path", selectedPath, "err", err)
						a.ui.ShowError("导入配置", err.Error())
					}
				}
			}
		}()

	case domain.ActionRequestAddRemote:
		go func() {
			if a.ui != nil {
				name, url, interval, ok := a.ui.ShowProfileInfoEditor("添加远程订阅", "", "", domain.DefaultUpdateInterval, true)
				if ok {
					if err := a.AddRemoteProfile(ctx, name, url, interval); err != nil {
						slog.Error("添加订阅失败", "url", url, "err", err)
						a.ui.ShowError("添加订阅", err.Error())
					}
				}
			}
		}()

	case domain.ActionEditProfileInfo:
		if p, ok := a.GetProfileInfo(cmd.Payload); ok {
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
					err = a.EditRemoteProfile(ctx, profile.Path, name, url, interval)
				} else {
					err = a.EditLocalProfile(ctx, profile.Path, name)
				}

				if err != nil {
					slog.Error("保存配置信息失败", "path", profile.Path, "err", err)
					a.ui.ShowError("保存配置", err.Error())
				}
			}(p)
		}

	case domain.ActionUpdateRemoteProfile:
		if p, ok := a.GetProfileInfo(cmd.Payload); ok {
			a.asyncRun("更新订阅", func() error { return a.UpdateRemoteProfile(ctx, p.Path, true) })
		}

	case domain.ActionSetProfileInterval:
		a.SetProfileInterval(cmd.Payload)

	case domain.ActionSwitchProfile:
		a.asyncRun("切换配置", func() error { return a.SwitchProfile(ctx, cmd.Payload) })

	case domain.ActionRemoveProfile:
		targetPath := cmd.Payload
		go func(path string) {
			if a.ui != nil {
				if !a.ui.ShowConfirm("删除配置", "确认删除该配置文件？本地文件将被清理且不可恢复。") {
					return
				}
			}

			if err := a.DeleteProfile(path); err != nil {
				slog.Error("删除配置失败", "err", err)
				if a.ui != nil {
					a.ui.ShowError("删除配置", err.Error())
				}
			}
		}(targetPath)

	case domain.ActionMoveProfileUp:
		a.MoveProfileUp(cmd.Payload)
		a.pushUIState()

	case domain.ActionMoveProfileDown:
		a.MoveProfileDown(cmd.Payload)
		a.pushUIState()

	case domain.ActionRequestEditPort:
		go func() {
			if a.ui != nil {
				cMixed, cSocks, cHttp := a.GetPortConfigSnapshot()
				nMixed, nSocks, nHttp, ok := a.ui.ShowPortEditor(cMixed, cSocks, cHttp)
				if ok {
					a.asyncRun("端口设置", func() error {
						return a.ApplyPortConfig(ctx, nMixed, nSocks, nHttp)
					})
				}
			}
		}()

	case domain.ActionRequestEditController:
		go func() {
			if a.ui != nil {
				cAddr, cSec, cOnline, cSys, cRemoteURL := a.GetControllerConfigSnapshot()
				nAddr, nSec, nOnline, nSys, nRemoteURL, ok := a.ui.ShowControllerEditor(cAddr, cSec, cOnline, cSys, cRemoteURL)
				if ok {
					a.asyncRun("面板设置", func() error {
						return a.ApplyControllerConfig(nAddr, nSec, nOnline, nSys, nRemoteURL)
					})
				}
			}
		}()

	case domain.ActionToggleTun:
		a.asyncRun("TUN 模式", func() error {
			restarted, err := a.ToggleTun(ctx, cmd.Payload == "true")
			if err != nil {
				return err
			}
			if restarted && a.ui != nil {
				a.ui.Exit()
			}
			time.Sleep(100 * time.Millisecond)
			a.ForcePushUIState()
			return nil
		})

	case domain.ActionToggleProxy:
		a.asyncRun("系统代理", func() error {
			a.ToggleProxy(cmd.Payload == "true")
			time.Sleep(100 * time.Millisecond)
			a.ForcePushUIState()
			return nil
		})

	case domain.ActionSwitchMode:
		a.asyncRun("路由模式", func() error {
			if err := a.SwitchMode(ctx, cmd.Payload); err != nil {
				return err
			}
			time.Sleep(100 * time.Millisecond)
			a.ForcePushUIState()
			return nil
		})

	case domain.ActionToggleAllowLan:
		a.asyncRun("局域网代理", func() error {
			if err := a.ToggleAllowLan(ctx, cmd.Payload == "true"); err != nil {
				return err
			}
			time.Sleep(100 * time.Millisecond)
			a.ForcePushUIState()
			return nil
		})

	case domain.ActionForceSyncAPI:
		a.ForceSyncAPI()

	case domain.ActionOpenBaseDir:
		a.OpenBaseDir()

	case domain.ActionOpenAppConfig:
		a.OpenAppConfig()

	case domain.ActionOpenConfigFile:
		a.asyncRun("打开配置", func() error { return a.OpenConfigFile(cmd.Payload) })

	case domain.ActionEditCurrentConfig:
		a.asyncRun("编辑配置", func() error { return a.EditCurrentConfig() })

	case domain.ActionToggleAutoStart:
		restarted := a.ToggleAutoStart(cmd.Payload == "true")
		if restarted && a.ui != nil {
			a.ui.Exit()
		}
		a.pushUIState()

	case domain.ActionToggleRunAsAdmin:
		restarted := a.ToggleRunAsAdmin(cmd.Payload == "true")
		if restarted && a.ui != nil {
			a.ui.Exit()
		}
		a.pushUIState()

	case domain.ActionReloadConfig:
		a.asyncRun("重载配置", func() error { return a.ReloadConfig(ctx) })

	case domain.ActionRestartKernel:
		a.asyncRun("重启内核", func() error { return a.RestartKernel(ctx) })

	case domain.ActionExitApp:
		slog.Info("收到退出指令")
		if a.ui != nil {
			a.ui.Exit()
		}

	case domain.ActionToggleSystemBrowser:
		a.ToggleSystemBrowser(cmd.Payload == "true")
		a.pushUIState()

	case domain.ActionToggleRemoteWebUI:
		a.ToggleRemoteWebUI(cmd.Payload == "true")
		a.pushUIState()

	case domain.ActionOpenWebUI:
		a.asyncRun("打开面板", func() error { return a.OpenWebUI() })

	case domain.ActionCopyWebUIPassword:
		if err := a.CopyWebUIPassword(); err != nil {
			slog.Warn("复制密码失败", "err", err)
			if a.ui != nil {
				a.ui.ShowError("复制密码", err.Error())
			}
		} else {
			if a.ui != nil {
				a.ui.ShowNotification("复制成功", "密码已复制到剪贴板。")
			}
		}

	case domain.ActionClearWebUICache:
		go func() {
			if a.ui != nil {
				if !a.ui.ShowConfirm("清理缓存", "将清除面板的本地设置与缓存，是否继续？") {
					return
				}
			}
			if err := a.ClearWebUICache(); err != nil {
				slog.Error("清理缓存失败", "err", err)
				if a.ui != nil {
					a.ui.ShowError("清理缓存", err.Error())
				}
			} else {
				slog.Info("Web 面板缓存已清理")
				if a.ui != nil {
					a.ui.ShowNotification("清理完成", "面板缓存已清除。")
				}
			}
		}()
	}
}
