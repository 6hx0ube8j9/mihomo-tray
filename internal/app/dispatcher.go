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

// ============================================================================
// 1. 核心调度基础设施 (Infrastructure, Runners & Notifiers)
// ============================================================================

// notifyError 阻塞/弹窗式强错误提醒（适用于关键操作失败与严重配置损坏）
func (a *Application) notifyError(actionTitle string, err error) {
	if err == nil {
		return
	}
	title := actionTitle
	if !strings.HasSuffix(title, "失败") {
		title += "失败"
	}

	slog.Error(title, "err", err)
	if a.ui != nil && !a.State.IsExiting() {
		a.ui.ShowError(title, err.Error())
	}
}

// notifyWarn 非阻塞托盘气泡通知（适用于异步轻量操作失败，优化交互打扰度）
func (a *Application) notifyWarn(actionTitle string, err error) {
	if err == nil {
		return
	}
	title := actionTitle
	if !strings.HasSuffix(title, "失败") {
		title += "失败"
	}

	slog.Warn(title, "err", err)
	if a.ui != nil && !a.State.IsExiting() {
		a.ui.ShowNotification(title, err.Error())
	}
}

// notifySuccess 操作成功轻量通知
func (a *Application) notifySuccess(title, message string) {
	slog.Info(title, "detail", message)
	if a.ui != nil && !a.State.IsExiting() {
		a.ui.ShowNotification(title, message)
	}
}

// runExclusiveAction 互斥独占动作执行器（如切换配置、重启内核）
func (a *Application) runExclusiveAction(action state.KernelAction, title string, task func() error) {
	if !a.State.TryBeginAction(action) {
		if a.ui != nil && !a.State.IsExiting() {
			a.ui.ShowNotification(title, "系统繁忙，正在处理其他任务，请稍后重试")
		}
		return
	}

	a.Presenter.ForcePushUIState()

	go func() {
		defer func() {
			a.State.EndAction()
			a.Presenter.ForcePushUIState()

			// 拦截未预期 Panic，防止子任务崩溃带崩托盘并确保释放互斥动作状态
			if r := recover(); r != nil {
				slog.Error("独占任务 Panic 崩溃拦截", "action", title, "panic", r)
				a.notifyError(title, fmt.Errorf("运行时异常: %v", r))
			}
		}()

		if err := task(); err != nil {
			a.notifyError(title, err)
		}
	}()
}

// asyncRun 后台异步轻量任务执行器（如快捷开关代理、切换模式）
// 任务完成时无条件 ForcePushUIState 刷新界面，彻底消除乐观打勾残留
func (a *Application) asyncRun(title string, task func() error) {
	go func() {
		defer a.Presenter.ForcePushUIState()

		defer func() {
			if r := recover(); r != nil {
				slog.Error("异步任务 Panic 崩溃拦截", "title", title, "panic", r)
				a.notifyWarn(title, fmt.Errorf("运行时异常: %v", r))
			}
		}()

		if err := task(); err != nil {
			a.notifyWarn(title, err)
		}
	}()
}

// ============================================================================
// 2. 指令路由中心 (Command Dispatcher: 0-Alloc Static Switch)
// ============================================================================

// handleUICommand 顶层指令分发入口（基于编译期跳转表，0 堆内存分配）
func (a *Application) handleUICommand(ctx context.Context, cmd domain.UICommand) {
	switch cmd.Action {

	// --- 订阅与配置管理 (ProfileHub) ---
	case domain.ActionOpenProfileManager:
		a.handleOpenProfileManager(ctx, cmd)
	case domain.ActionRequestAddLocal:
		a.handleRequestAddLocal(ctx, cmd)
	case domain.ActionRequestAddRemote:
		a.handleRequestAddRemote(ctx, cmd)
	case domain.ActionEditProfileInfo:
		a.handleEditProfileInfo(ctx, cmd)
	case domain.ActionUpdateRemoteProfile:
		a.handleUpdateRemoteProfile(ctx, cmd)
	case domain.ActionSetProfileInterval:
		a.handleSetProfileInterval(ctx, cmd)
	case domain.ActionSwitchProfile:
		a.handleSwitchProfile(ctx, cmd)
	case domain.ActionRemoveProfile:
		a.handleRemoveProfile(ctx, cmd)
	case domain.ActionMoveProfileUp:
		a.handleMoveProfileUp(ctx, cmd)
	case domain.ActionMoveProfileDown:
		a.handleMoveProfileDown(ctx, cmd)

	// --- 系统代理控制 (ProxyGuard) ---
	case domain.ActionToggleProxy:
		a.handleToggleProxy(ctx, cmd)

	// --- 运行参数调优 (Tuner) ---
	case domain.ActionToggleTun:
		a.handleToggleTun(ctx, cmd)
	case domain.ActionSwitchMode:
		a.handleSwitchMode(ctx, cmd)
	case domain.ActionToggleAllowLan:
		a.handleToggleAllowLan(ctx, cmd)
	case domain.ActionRequestEditPort:
		a.handleRequestEditPort(ctx, cmd)

	// --- Web 面板管理 (Panel) ---
	case domain.ActionOpenWebUI:
		a.handleOpenWebUI(ctx, cmd)
	case domain.ActionRequestEditController:
		a.handleRequestEditController(ctx, cmd)
	case domain.ActionToggleSystemBrowser:
		a.handleToggleSystemBrowser(ctx, cmd)
	case domain.ActionToggleRemoteWebUI:
		a.handleToggleRemoteWebUI(ctx, cmd)
	case domain.ActionCopyWebUIPassword:
		a.handleCopyWebUIPassword(ctx, cmd)
	case domain.ActionClearWebUICache:
		a.handleClearWebUICache(ctx, cmd)

	// --- 系统与生命周期 (Sys & Lifecycle) ---
	case domain.ActionReloadConfig:
		a.handleReloadConfig(ctx, cmd)
	case domain.ActionRestartKernel:
		a.handleRestartKernel(ctx, cmd)
	case domain.ActionOpenBaseDir:
		a.handleOpenBaseDir(ctx, cmd)
	case domain.ActionOpenAppConfig:
		a.handleOpenAppConfig(ctx, cmd)
	case domain.ActionOpenConfigFile:
		a.handleOpenConfigFile(ctx, cmd)
	case domain.ActionEditCurrentConfig:
		a.handleEditCurrentConfig(ctx, cmd)
	case domain.ActionToggleAutoStart:
		a.handleToggleAutoStart(ctx, cmd)
	case domain.ActionToggleRunAsAdmin:
		a.handleToggleRunAsAdmin(ctx, cmd)
	case domain.ActionExitApp:
		a.handleExitApp(ctx, cmd)

	default:
		slog.Warn("未识别的 UI 指令", "action", cmd.Action)
	}
}

// ============================================================================
// 3. 订阅与配置管理 (Domain: ProfileHub)
// ============================================================================

func (a *Application) handleProfileAdded(ctx context.Context, targetRelPath string, isFirst bool) {
	if isFirst {
		a.runExclusiveAction(state.ActionSwitchProfile, "激活配置", func() error {
			if err := a.Deployer.DeployAndApply(ctx, targetRelPath, "激活首个导入配置"); err != nil {
				return err
			}

			a.ProfileHub.SetActiveProfile(targetRelPath)
			a.ProxyGuard.Sync()
			a.Panel.ReopenIfActive(a.ForceSyncAPI)
			return nil
		})
		return
	}
	a.Presenter.PushUIState()
}

func (a *Application) handleOpenProfileManager(_ context.Context, _ domain.UICommand) {
	if a.ui != nil {
		a.ui.ShowProfileManager(a.Presenter.GetSnapshot())
	}
}

func (a *Application) handleRequestAddLocal(ctx context.Context, _ domain.UICommand) {
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
}

func (a *Application) handleRequestAddRemote(ctx context.Context, _ domain.UICommand) {
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
}

func (a *Application) handleEditProfileInfo(ctx context.Context, cmd domain.UICommand) {
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
}

func (a *Application) handleUpdateRemoteProfile(ctx context.Context, cmd domain.UICommand) {
	if p, ok := a.ProfileHub.GetProfileInfo(cmd.Payload); ok {
		a.runExclusiveAction(state.ActionReload, "更新订阅", func() error {
			if err := a.ProfileHub.UpdateRemote(ctx, p.Path); err != nil {
				if errors.Is(err, ErrProfileUpdating) {
					a.notifyWarn("更新订阅", errors.New("该订阅正在更新中，请勿重复操作"))
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
}

func (a *Application) handleSetProfileInterval(_ context.Context, cmd domain.UICommand) {
	a.ProfileHub.SetProfileInterval(cmd.Payload)
	a.Presenter.PushUIState()
}

func (a *Application) handleSwitchProfile(ctx context.Context, cmd domain.UICommand) {
	a.runExclusiveAction(state.ActionSwitchProfile, "切换配置", func() error {
		target := cmd.Payload
		if target == "" {
			return errors.New("未指定目标配置文件")
		}
		if a.ProfileHub.IsActive(target) {
			return nil
		}
		if err := a.ProfileHub.ValidateTarget(target); err != nil {
			return err
		}

		if err := a.Deployer.DeployAndApply(ctx, target, "切换配置"); err != nil {
			return err
		}

		a.ProfileHub.SetActiveProfile(target)
		a.ProxyGuard.Sync()
		a.Panel.ReopenIfActive(a.ForceSyncAPI)
		return nil
	})
}

func (a *Application) handleRemoveProfile(ctx context.Context, cmd domain.UICommand) {
	targetPath := cmd.Payload
	go func(path string) {
		if a.ui != nil && !a.ui.ShowConfirm("删除配置", "确认删除该配置文件？本地文件将被清理且不可恢复。") {
			return
		}

		a.runExclusiveAction(state.ActionSwitchProfile, "删除配置", func() error {
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
}

func (a *Application) handleMoveProfileUp(_ context.Context, cmd domain.UICommand) {
	a.ProfileHub.MoveProfileUp(cmd.Payload)
	a.Presenter.PushUIState()
}

func (a *Application) handleMoveProfileDown(_ context.Context, cmd domain.UICommand) {
	a.ProfileHub.MoveProfileDown(cmd.Payload)
	a.Presenter.PushUIState()
}

// ============================================================================
// 4. 系统代理控制 (Domain: ProxyGuard)
// ============================================================================

func (a *Application) handleToggleProxy(_ context.Context, cmd domain.UICommand) {
	a.asyncRun("系统代理", func() error {
		a.ProxyGuard.SetProxy(cmd.Payload == "true")
		return nil
	})
}

// ============================================================================
// 5. 运行参数调优 (Domain: Tuner)
// ============================================================================

func (a *Application) handleToggleTun(ctx context.Context, cmd domain.UICommand) {
	a.asyncRun("TUN 模式", func() error {
		restarted, err := a.Tuner.ToggleTun(ctx, cmd.Payload == "true", a.HostBridge.ElevatePrivilege)
		if err == nil && restarted {
			a.ExitForRestart()
		}
		return err
	})
}

func (a *Application) handleSwitchMode(ctx context.Context, cmd domain.UICommand) {
	a.asyncRun("路由模式", func() error {
		return a.Tuner.SwitchMode(ctx, cmd.Payload)
	})
}

func (a *Application) handleToggleAllowLan(ctx context.Context, cmd domain.UICommand) {
	a.asyncRun("局域网代理", func() error {
		return a.Tuner.ToggleAllowLan(ctx, cmd.Payload == "true")
	})
}

func (a *Application) handleRequestEditPort(ctx context.Context, _ domain.UICommand) {
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
}

// ============================================================================
// 6. Web 面板管理 (Domain: Panel)
// ============================================================================

func (a *Application) handleOpenWebUI(_ context.Context, _ domain.UICommand) {
	a.asyncRun("打开面板", func() error {
		return a.Panel.Open(a.ForceSyncAPI)
	})
}

func (a *Application) handleRequestEditController(_ context.Context, _ domain.UICommand) {
	go func() {
		if a.ui != nil {
			cAddr, cSec, cOnline, cSys, cRemoteURL := a.Tuner.GetControllerSnapshot()
			nAddr, nSec, nOnline, nSys, nRemoteURL, ok := a.ui.ShowControllerEditor(cAddr, cSec, cOnline, cSys, cRemoteURL)
			if ok {
				a.runExclusiveAction(state.ActionRestart, "面板设置", func() error {
					coreChanged, appChanged := a.Tuner.ApplyControllerConfig(nAddr, nSec, nOnline, nSys, nRemoteURL)
					if coreChanged {
						if err := a.Deployer.RestartKernel(); err != nil {
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
}

func (a *Application) handleToggleSystemBrowser(_ context.Context, cmd domain.UICommand) {
	a.Tuner.ToggleSystemBrowser(cmd.Payload == "true")
	a.Presenter.PushUIState()
}

func (a *Application) handleToggleRemoteWebUI(_ context.Context, cmd domain.UICommand) {
	a.Tuner.ToggleRemoteWebUI(cmd.Payload == "true")
	a.Presenter.PushUIState()
}

func (a *Application) handleCopyWebUIPassword(_ context.Context, _ domain.UICommand) {
	if err := a.Panel.CopyPassword(); err != nil {
		a.notifyWarn("复制密码", err)
	} else {
		a.notifySuccess("复制成功", "密码已复制到剪贴板。")
	}
}

func (a *Application) handleClearWebUICache(_ context.Context, _ domain.UICommand) {
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
}

// ============================================================================
// 7. 系统与生命周期 (Domain: Sys & Lifecycle)
// ============================================================================

func (a *Application) handleReloadConfig(ctx context.Context, _ domain.UICommand) {
	a.runExclusiveAction(state.ActionReload, "重载配置", func() error {
		_ = a.Cfg.ReloadFromDisk()
		a.HostBridge.ReconcileRuntimePrivileges()

		if err := a.Deployer.ReloadConfig(ctx); err != nil {
			return err
		}

		a.ProxyGuard.Sync()
		a.Panel.ReopenIfActive(a.ForceSyncAPI)
		return nil
	})
}

func (a *Application) handleRestartKernel(ctx context.Context, _ domain.UICommand) {
	a.runExclusiveAction(state.ActionRestart, "重启内核", func() error {
		_ = a.Cfg.ReloadFromDisk()
		a.HostBridge.ReconcileRuntimePrivileges()

		if err := a.Deployer.RestartKernel(); err != nil {
			a.FallbackToEmpty(ctx, "重启配置校验失败")
			a.notifyWarn("重启内核", errors.New("配置校验失败，已自动回退至空配置运行"))
			return nil
		}

		a.ProxyGuard.Sync()
		a.Panel.ReopenIfActive(a.ForceSyncAPI)
		return nil
	})
}

func (a *Application) handleOpenBaseDir(_ context.Context, _ domain.UICommand) {
	a.HostBridge.OpenBaseDir()
}

func (a *Application) handleOpenAppConfig(_ context.Context, _ domain.UICommand) {
	a.HostBridge.OpenAppConfig()
}

func (a *Application) handleOpenConfigFile(_ context.Context, cmd domain.UICommand) {
	a.asyncRun("打开配置", func() error {
		return a.HostBridge.OpenConfigFile(cmd.Payload)
	})
}

func (a *Application) handleEditCurrentConfig(_ context.Context, _ domain.UICommand) {
	a.asyncRun("编辑配置", func() error {
		return a.HostBridge.EditCurrentConfig()
	})
}

func (a *Application) handleToggleAutoStart(_ context.Context, cmd domain.UICommand) {
	a.asyncRun("开机自启", func() error {
		if a.HostBridge.ToggleAutoStart(cmd.Payload == "true") {
			a.ExitForRestart()
		}
		return nil
	})
}

func (a *Application) handleToggleRunAsAdmin(_ context.Context, cmd domain.UICommand) {
	a.asyncRun("以管理员运行", func() error {
		if a.HostBridge.ToggleRunAsAdmin(cmd.Payload == "true") {
			a.ExitForRestart()
		}
		return nil
	})
}

func (a *Application) handleExitApp(_ context.Context, _ domain.UICommand) {
	if a.ui != nil {
		a.ui.Exit()
	}
}
