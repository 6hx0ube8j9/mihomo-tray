package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

func (a *Application) notifyError(actionTitle string, err error) {
	if err == nil {
		return
	}
	title := actionTitle
	if !strings.HasSuffix(title, "失败") {
		title += "失败"
	}

	slog.Error(title, "err", err)
	if a.ui != nil {
		a.ui.ShowError(title, err.Error())
	}
}

func (a *Application) notifySuccess(title, message string) {
	slog.Info(title, "detail", message)
	if a.ui != nil {
		a.ui.ShowNotification(title, message)
	}
}

func (a *Application) runExclusiveAction(action state.KernelAction, title string, task func() error) {
	if !a.State.TryBeginAction(action) {
		if a.ui != nil {
			a.ui.ShowError(title, "系统繁忙，请稍后重试")
		}
		return
	}

	a.Presenter.ForcePushUIState()

	go func() {
		defer func() {
			a.State.EndAction()
			a.Presenter.ForcePushUIState()
		}()

		if err := task(); err != nil {
			a.notifyError(title, err)
		}
	}()
}

func (a *Application) asyncRun(title string, task func() error) {
	go func() {
		defer a.Presenter.PushUIState()

		if err := task(); err != nil {
			a.notifyError(title, err)
		}
	}()
}

// handleProfileAdded 采用事务设计：预先确立活跃状态，防止物理重启钩子读取到空配置
func (a *Application) handleProfileAdded(ctx context.Context, targetRelPath string, isFirst bool) {
	if isFirst {
		a.runExclusiveAction(state.ActionSwitchProfile, "激活配置", func() error {
			a.ProfileHub.SetActiveProfile(targetRelPath)

			if err := a.Deployer.DeployAndApply(ctx, targetRelPath, "激活首个导入配置"); err != nil {
				a.ProfileHub.ClearActiveProfile()
				return err
			}

			a.ProxyGuard.Sync()
			a.Panel.ReopenIfActive(a.ForceSyncAPI)
			return nil
		})
		return
	}
	a.Presenter.PushUIState()
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
			if a.ui == nil {
				return
			}
			if path, ok := a.ui.OpenYAMLFileDialog(); ok {
				targetRelPath, isFirst, err := a.ProfileHub.ImportLocal(path)
				if err != nil {
					a.notifyError("导入配置", err)
					return
				}
				a.handleProfileAdded(ctx, targetRelPath, isFirst)
			}
		}()

	case domain.ActionRequestAddRemote:
		go func() {
			if a.ui == nil {
				return
			}
			name, url, interval, ok := a.ui.ShowProfileInfoEditor("添加远程订阅", "", "", domain.DefaultUpdateInterval, true)
			if ok {
				targetRelPath, isFirst, err := a.ProfileHub.AddRemote(ctx, name, url, interval)
				if err != nil {
					a.notifyError("添加订阅", err)
					return
				}
				a.handleProfileAdded(ctx, targetRelPath, isFirst)
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

				if isRemote {
					a.runExclusiveAction(state.ActionReload, "更新订阅", func() error {
						urlChanged, err := a.ProfileHub.EditRemote(ctx, profile.Path, name, url, interval)
						if err != nil {
							return err
						}
						if urlChanged && a.ProfileHub.IsActive(profile.Path) {
							if err := a.Deployer.DeployAndApply(ctx, profile.Path, "应用订阅地址变更"); err != nil {
								return err
							}
							a.ProxyGuard.Sync()
							a.Panel.ReopenIfActive(a.ForceSyncAPI)
						}
						return nil
					})
				} else {
					if err := a.ProfileHub.EditLocal(profile.Path, name); err != nil {
						a.notifyError("保存配置", err)
					} else {
						a.Presenter.PushUIState()
					}
				}
			}(p)
		}

	case domain.ActionUpdateRemoteProfile:
		if p, ok := a.ProfileHub.GetProfileInfo(cmd.Payload); ok {
			a.runExclusiveAction(state.ActionReload, "更新订阅", func() error {
				if err := a.ProfileHub.UpdateRemote(ctx, p.Path); err != nil {
					if errors.Is(err, ErrProfileUpdating) {
						a.notifySuccess("更新订阅", "该订阅当前正在更新中，请勿重复操作。")
						return nil
					}
					return err
				}
				if a.ProfileHub.IsActive(p.Path) {
					if err := a.Deployer.DeployAndApply(ctx, p.Path, "应用订阅更新"); err != nil {
						return err
					}
					a.ProxyGuard.Sync()
					a.Panel.ReopenIfActive(a.ForceSyncAPI)
				}
				return nil
			})
		}

	case domain.ActionSetProfileInterval:
		a.ProfileHub.SetProfileInterval(cmd.Payload)
		a.Presenter.PushUIState()

	case domain.ActionSwitchProfile:
		a.runExclusiveAction(state.ActionSwitchProfile, "切换配置", func() error {
			target := cmd.Payload
			if target == "" {
				a.FallbackToEmpty(ctx, "用户主动切换为空配置")
				return nil
			}
			if a.ProfileHub.IsActive(target) {
				return nil
			}
			if err := a.ProfileHub.ValidateTarget(target); err != nil {
				return err
			}

			oldActive := a.ProfileHub.GetActivePath()
			a.ProfileHub.SetActiveProfile(target)

			if err := a.Deployer.DeployAndApply(ctx, target, "切换配置"); err != nil {
				a.ProfileHub.SetActiveProfile(oldActive)
				return err
			}

			a.ProxyGuard.Sync()
			a.Panel.ReopenIfActive(a.ForceSyncAPI)
			return nil
		})

	case domain.ActionRemoveProfile:
		targetPath := cmd.Payload
		go func(path string) {
			if a.ui != nil && !a.ui.ShowConfirm("删除配置", "确认删除该配置文件？本地文件将被清理且不可恢复。") {
				return
			}
			a.asyncRun("删除配置", func() error {
				wasActive, err := a.ProfileHub.Delete(path)
				if err != nil {
					return err
				}
				if wasActive {
					a.FallbackToEmpty(ctx, "正在使用的配置文件被删除")
				}
				return nil
			})
		}(targetPath)

	case domain.ActionMoveProfileUp:
		a.ProfileHub.MoveProfileUp(cmd.Payload)
		a.Presenter.PushUIState()

	case domain.ActionMoveProfileDown:
		a.ProfileHub.MoveProfileDown(cmd.Payload)
		a.Presenter.PushUIState()

	// ================= 系统代理控制 (ProxyGuard) =================
	case domain.ActionToggleProxy:
		a.asyncRun("系统代理", func() error {
			a.ProxyGuard.SetProxy(cmd.Payload == "true")
			return nil
		})

	// ================= 运行参数调优 (Tuner) =================
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
						changed, err := a.Tuner.ApplyPortConfig(ctx, nMixed, nSocks, nHttp)
						if err != nil {
							return err
						}
						if changed && a.Cfg.IsSystemProxyEnabled() {
							a.ProxyGuard.Sync()
						}
						return nil
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
					a.runExclusiveAction(state.ActionRestart, "面板设置", func() error {
						coreChanged, appChanged := a.Tuner.ApplyControllerConfig(nAddr, nSec, nOnline, nSys, nRemoteURL)
						if coreChanged {
							if err := a.Deployer.RestartKernel(ctx); err != nil {
								return err
							}
						}
						if coreChanged || appChanged {
							a.Panel.ReopenIfActive(a.ForceSyncAPI)
						}
						return nil
					})
				}
			}
		}()

	case domain.ActionToggleSystemBrowser:
		a.Tuner.ToggleSystemBrowser(cmd.Payload == "true")
		a.Presenter.PushUIState()

	case domain.ActionToggleRemoteWebUI:
		a.Tuner.ToggleRemoteWebUI(cmd.Payload == "true")
		a.Presenter.PushUIState()

	case domain.ActionCopyWebUIPassword:
		if err := a.Panel.CopyPassword(); err != nil {
			a.notifyError("复制密码", err)
		} else {
			a.notifySuccess("复制成功", "密码已复制到剪贴板。")
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
				a.notifySuccess("清理完成", "面板缓存已清除。")
				return nil
			})
		}()

	// ================= 重载与重启 (自愈与静默编排) =================
	case domain.ActionReloadConfig:
		a.runExclusiveAction(state.ActionReload, "重载配置", func() error {
			if err := a.Cfg.ReloadFromDisk(); err != nil {
				a.notifySuccess("配置回退", "主配置文件格式损坏，已自动恢复为上一次保存的设置。")
			}

			a.HostBridge.CheckAndReconcilePrivileges(false)

			if err := a.Deployer.ReloadConfig(ctx); err != nil {
				a.FallbackToEmpty(ctx, "重载配置校验未通过")
				a.notifyError("配置错误", fmt.Errorf("配置文件内容校验未通过，已回退空配置"))
			} else {
				a.ProxyGuard.Sync()
			}

			a.Panel.ReopenIfActive(a.ForceSyncAPI)
			return nil
		})

	case domain.ActionRestartKernel:
		a.runExclusiveAction(state.ActionRestart, "重启内核", func() error {
			if err := a.Cfg.ReloadFromDisk(); err != nil {
				a.notifySuccess("配置回退", "主配置文件格式损坏，已自动恢复为上一次保存的设置。")
			}

			a.HostBridge.CheckAndReconcilePrivileges(false)

			if err := a.Deployer.RestartKernel(ctx); err != nil {
				a.FallbackToEmpty(ctx, "重启配置校验未通过")
				a.Deployer.PhysicalRestart(a.Cfg.GetConfig())
				a.notifyError("配置错误", fmt.Errorf("当前配置校验未通过，已切换为空配置拉起内核"))
			} else {
				a.ProxyGuard.Sync()
			}

			a.Panel.ReopenIfActive(a.ForceSyncAPI)
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
	}
}
