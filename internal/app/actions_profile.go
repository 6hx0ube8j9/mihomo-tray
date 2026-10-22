package app

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

// ============================================================================
// 订阅与配置管理指令分发 (Domain: ProfileHub Actions)
// ============================================================================

func (a *Application) handleProfileAdded(ctx context.Context, targetRelPath string, isFirst bool) {
	if isFirst {
		a.runExclusiveAction(state.ActionSwitchProfile, "激活配置", func() error {
			if err := a.KernelDeployer.DeployAndApply(ctx, targetRelPath, "激活首个导入配置"); err != nil {
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
						if err := a.KernelDeployer.DeployAndApply(ctx, profile.Path, "应用订阅地址变更"); err != nil {
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
				if err := a.KernelDeployer.DeployAndApply(ctx, p.Path, "应用订阅更新"); err != nil {
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
	parts := strings.SplitN(cmd.Payload, "|", 2)
	if len(parts) != 2 {
		return
	}
	interval, err := strconv.Atoi(parts[1])
	if err != nil {
		return
	}

	if a.ProfileHub.SetInterval(parts[0], interval) {
		a.Presenter.PushUIState()
	}
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

		if err := a.KernelDeployer.DeployAndApply(ctx, target, "切换配置"); err != nil {
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

func (a *Application) handleOpenConfigFile(_ context.Context, cmd domain.UICommand) {
	a.asyncRun("打开配置", func() error {
		targetRelPath := cmd.Payload
		if targetRelPath == "" {
			targetRelPath = a.ProfileHub.GetActiveProfile()
		}
		if targetRelPath == "" {
			return errors.New("未指定配置文件路径")
		}
		if err := a.Cfg.ValidatePhysicalFile(targetRelPath); err != nil {
			return fmt.Errorf("配置文件不可用: %w", err)
		}
		absPath := a.Cfg.GetProfileAbsPath(targetRelPath)
		return a.Platform.OpenFile(absPath)
	})
}

func (a *Application) handleEditCurrentConfig(_ context.Context, _ domain.UICommand) {
	a.asyncRun("编辑配置", func() error {
		targetRelPath := a.ProfileHub.GetActiveProfile()
		if targetRelPath == "" {
			return errors.New("未选中任何运行配置")
		}
		if err := a.Cfg.ValidatePhysicalFile(targetRelPath); err != nil {
			return fmt.Errorf("当前配置文件不可用: %w", err)
		}
		absPath := a.Cfg.GetProfileAbsPath(targetRelPath)
		return a.Platform.OpenFile(absPath)
	})
}
