package app

import (
	"context"
	"log/slog"
	"time"

	"mihomo-tray/internal/domain"
)

// =====================================================================
// 核心语法糖 (双轨日志与弹窗透传规范)
// =====================================================================

func (a *Application) asyncRun(title string, task func() error) {
	go func() {
		if err := task(); err != nil {
			slog.Error("异步任务执行失败", "action", title, "err", err)
			
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
						slog.Error("导入本地配置失败", "path", selectedPath, "err", err)
						a.ui.ShowError("导入配置失败", err.Error())
					}
				}
			}
		}()

	case domain.ActionRequestAddRemote:
		go func() {
			if a.ui != nil {
				name, url, interval, ok := a.ui.ShowProfileInfoEditor("添加远程订阅", "", "", domain.DefaultUpdateInterval)
				if ok {
					if err := a.AddRemoteProfile(ctx, name, url, interval); err != nil {
						slog.Error("添加订阅失败", "url", url, "err", err)
						a.ui.ShowError("添加订阅失败", err.Error())
					}
				}
			}
		}()

    case domain.ActionEditProfileInfo:
		if profile, ok := a.GetProfileInfo(cmd.Payload); ok {
			go func(p domain.ProfileItem) {
				if a.ui != nil {
					isRemote := p.URL != ""

					name, url, interval, ok := a.ui.ShowProfileInfoEditor("编辑信息", p.Name, p.URL, p.Interval, isRemote)
					if ok {
						if isRemote {
							if err := a.EditRemoteProfile(ctx, p.Path, name, url, interval); err != nil {
								slog.Error("保存订阅修改失败", "path", p.Path, "err", err)
								a.ui.ShowError("保存失败", err.Error())
							}
						} else {
							if err := a.EditLocalProfile(ctx, p.Path, name); err != nil {
								slog.Error("保存配置名称失败", "path", p.Path, "err", err)
								a.ui.ShowError("保存失败", err.Error())
							}
						}
					}
				}
			}(profile)
		}

	case domain.ActionUpdateRemoteProfile:
		if p, ok := a.GetProfileInfo(cmd.Payload); ok {
			a.asyncRun("拉取订阅失败", func() error { return a.UpdateRemoteProfile(ctx, p.Path, true) })
		}

	case domain.ActionSetProfileInterval:
		a.SetProfileInterval(cmd.Payload)

	case domain.ActionSwitchProfile:
		a.asyncRun("切换配置失败", func() error { return a.SwitchProfile(ctx, cmd.Payload) })

	case domain.ActionRemoveProfile:
		targetPath := cmd.Payload
		go func(path string) {
			if a.ui != nil {
				if !a.ui.ShowConfirm("确认删除配置", "删除后本地文件将被同步清理且无法恢复。\n\n是否继续？") {
					return
				}
			}
			
			if err := a.DeleteProfile(path); err != nil {
				slog.Error("删除配置后重置状态失败", "err", err)
				if a.ui != nil {
					a.ui.ShowError("删除后重置失败", err.Error())
				}
			}
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
					a.asyncRun("端口设置失败", func() error {
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
					a.asyncRun("面板设置失败", func() error {
						return a.ApplyControllerConfig(nAddr, nSec, nOnline, nSys, nRemoteURL)
					})
				}
			}
		}()

	case domain.ActionToggleTun:
		a.asyncRun("TUN 状态切换失败", func() error {
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
		a.asyncRun("系统代理设置失败", func() error {
			a.ToggleProxy(cmd.Payload == "true")
			time.Sleep(100 * time.Millisecond)
			a.ForcePushUIState()
			return nil
		})

	case domain.ActionSwitchMode:
		a.asyncRun("路由模式切换失败", func() error {
			if err := a.SwitchMode(ctx, cmd.Payload); err != nil {
				return err
			}
			time.Sleep(100 * time.Millisecond)
			a.ForcePushUIState()
			return nil
		})

	case domain.ActionToggleAllowLan:
		a.asyncRun("局域网代理设置失败", func() error {
			if err := a.ToggleAllowLan(ctx, cmd.Payload == "true"); err != nil {
				return err
			}
			time.Sleep(100 * time.Millisecond)
			a.ForcePushUIState()
			return nil
		})

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
		a.asyncRun("打开配置文件失败", func() error { return a.OpenConfigFile(cmd.Payload) })

	case domain.ActionEditCurrentConfig:
		a.asyncRun("无法编辑配置", func() error { return a.EditCurrentConfig() })

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
		a.asyncRun("重载失败", func() error { return a.ReloadConfig(ctx) })

	case domain.ActionRestartKernel:
		a.asyncRun("重启失败", func() error { return a.RestartKernel(ctx) })

	case domain.ActionExitApp:
		slog.Info("收到退出指令，准备安全销毁应用")
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
		a.asyncRun("打开面板失败", func() error { return a.OpenWebUI() })

	case domain.ActionCopyWebUIPassword:
		if err := a.CopyWebUIPassword(); err != nil {
			slog.Warn("尝试复制密码失败", "err", err)
			if a.ui != nil {
				a.ui.ShowError("复制失败", err.Error())
			}
		} else {
			if a.ui != nil {
				a.ui.ShowNotification("复制成功", "访问密码已复制到剪贴板。")
			}
		}

	case domain.ActionClearWebUICache:
		go func() {
			if a.ui != nil {
				if !a.ui.ShowConfirm("清理确认", "这将清除面板的所有个性化设置（如主题、布局等）且不可恢复。\n\n是否继续？") {
					return
				}
			}
			if err := a.ClearWebUICache(); err != nil {
				slog.Error("清理 Web 面板缓存异常", "err", err)
				if a.ui != nil {
					a.ui.ShowError("清理失败", err.Error())
				}
			} else {
				slog.Info("Web 面板缓存已手动清理")
				if a.ui != nil {
					a.ui.ShowNotification("清理完成", "本地面板缓存已彻底清除。")
				}
			}
		}()
	}
}
