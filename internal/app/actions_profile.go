package app

import (
	"context"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

func (a *Application) applyAndSyncRuntime(ctx context.Context, targetRelPath, actionDesc string) error {
	err := a.KernelDeployer.DeployAndApply(ctx, targetRelPath, actionDesc, func() {
		a.Cfg.SetActiveProfile(targetRelPath)
	})
	if err != nil {
		return err
	}

	a.ProxyGuard.Sync()
	a.ForceSyncAPI()
	return nil
}

func (a *Application) applyActiveProfileUpdate(ctx context.Context, targetRelPath, actionDesc string) {
	a.runExclusiveAction(state.ActionReload, "应用订阅更新", func() error {
		if a.Cfg.GetActivePath() != targetRelPath {
			return nil
		}
		return a.applyAndSyncRuntime(ctx, targetRelPath, actionDesc)
	})
}

func (a *Application) runRemoteProfileUpdate(ctx context.Context, targetPath, actionDesc string, updateFn func() (bool, error)) {
	a.asyncRunError("更新订阅", func() error {
		needReload, err := updateFn()
		if err != nil {
			return err
		}
		a.Presenter.PushUIState()
		if needReload && a.Cfg.GetActivePath() == targetPath {
			a.applyActiveProfileUpdate(ctx, targetPath, actionDesc)
		}
		return nil
	})
}

func (a *Application) handleProfileAdded(ctx context.Context, targetRelPath string) {
	if targetRelPath == "" {
		return
	}

	if a.Cfg.GetActivePath() == "" {
		a.runExclusiveProfileAction(state.ActionSwitchProfile, "激活配置", targetRelPath, func() error {
			if a.Cfg.GetActivePath() != "" {
				return nil
			}
			return a.applyAndSyncRuntime(ctx, targetRelPath, "激活首个导入配置")
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
		path, ok := a.ui.OpenYAMLFileDialog()
		if !ok {
			return
		}

		targetRelPath, err := a.ProfileHub.ImportLocal(path)
		if err != nil {
			a.notifyError("导入配置", err)
			return
		}
		a.handleProfileAdded(ctx, targetRelPath)
	}()
}

func (a *Application) handleRequestAddRemote(ctx context.Context, _ domain.UICommand) {
	go func() {
		if a.ui == nil {
			return
		}
		name, url, interval, ok := a.ui.ShowProfileInfoEditor("添加远程订阅", "", "", domain.DefaultUpdateInterval, true)
		if !ok {
			return
		}

		targetRelPath, err := a.ProfileHub.AddRemote(ctx, name, url, interval)
		if err != nil {
			a.notifyError("添加订阅", err)
			return
		}
		a.handleProfileAdded(ctx, targetRelPath)
	}()
}

func (a *Application) handleEditProfileInfo(ctx context.Context, cmd domain.UICommand) {
	p, ok := a.Cfg.GetProfileByPath(cmd.Payload)
	if !ok {
		return
	}

	go func(profile domain.ProfileItem) {
		if a.ui == nil {
			return
		}
		isRemote := profile.URL != ""
		name, url, interval, ok := a.ui.ShowProfileInfoEditor("编辑信息", profile.Name, profile.URL, profile.Interval, isRemote)
		if !ok {
			return
		}

		if !isRemote {
			if profile.Name != name {
				profile.Name = name
				a.Cfg.UpsertProfile(profile)
			}
			a.Presenter.PushUIState()
			return
		}

		a.runRemoteProfileUpdate(ctx, profile.Path, "应用订阅地址变更", func() (bool, error) {
			return a.ProfileHub.EditRemote(ctx, profile.Path, name, url, interval)
		})
	}(p)
}

func (a *Application) handleUpdateRemoteProfile(ctx context.Context, cmd domain.UICommand) {
	p, ok := a.Cfg.GetProfileByPath(cmd.Payload)
	if !ok {
		return
	}

	a.runRemoteProfileUpdate(ctx, p.Path, "应用订阅更新", func() (bool, error) {
		if err := a.ProfileHub.UpdateRemote(ctx, p.Path); err != nil {
			return false, err
		}
		return true, nil
	})
}

func (a *Application) handleSwitchProfile(ctx context.Context, cmd domain.UICommand) {
	target := cmd.Payload
	if target == "" {
		a.Presenter.ForcePushUIState()
		go a.notifyError("切换配置", config.ErrProfilePathEmpty)
		return
	}
	if a.Cfg.GetActivePath() == target {
		a.Presenter.ForcePushUIState()
		return
	}

	a.runExclusiveProfileAction(state.ActionSwitchProfile, "切换配置", target, func() error {
		if a.Cfg.GetActivePath() == target {
			return nil
		}
		if err := a.Cfg.ValidatePhysicalFile(target); err != nil {
			return err
		}
		return a.applyAndSyncRuntime(ctx, target, "切换配置")
	})
}

func (a *Application) handleRemoveProfile(ctx context.Context, cmd domain.UICommand) {
	targetPath := cmd.Payload
	if targetPath == "" {
		return
	}

	go func(path string) {
		if a.ui == nil || !a.ui.ShowConfirm("删除确认", "此操作将同时删除本地文件，是否确定？") {
			return
		}

		a.runExclusiveAction(state.ActionSwitchProfile, "删除配置", func() error {
			wasActive, err := a.ProfileHub.Delete(path)
			if err != nil {
				return err
			}

			if wasActive {
				a.FallbackToEmpty(ctx, "当前配置已被删除")
			}
			return nil
		})
	}(targetPath)
}

func (a *Application) handleMoveProfileUp(_ context.Context, cmd domain.UICommand) {
	if a.Cfg.MoveProfile(cmd.Payload, -1) {
		a.Presenter.PushUIState()
	}
}

func (a *Application) handleMoveProfileDown(_ context.Context, cmd domain.UICommand) {
	if a.Cfg.MoveProfile(cmd.Payload, 1) {
		a.Presenter.PushUIState()
	}
}

func (a *Application) openProfile(targetRelPath string) error {
	if err := a.Cfg.ValidatePhysicalFile(targetRelPath); err != nil {
		return err
	}
	return a.Platform.OpenFile(a.Cfg.GetProfileAbsPath(targetRelPath))
}

func (a *Application) handleOpenConfigFile(_ context.Context, cmd domain.UICommand) {
	if cmd.Payload == "" {
		a.showError("打开配置", "当前未选择配置文件")
		return
	}
	a.asyncRunError("打开配置", func() error {
		return a.openProfile(cmd.Payload)
	})
}

func (a *Application) handleEditCurrentConfig(_ context.Context, _ domain.UICommand) {
	active := a.Cfg.GetActivePath()
	if active == "" {
		a.showError("编辑配置", "当前未选择配置文件")
		return
	}
	a.asyncRunError("编辑配置", func() error {
		return a.openProfile(active)
	})
}
