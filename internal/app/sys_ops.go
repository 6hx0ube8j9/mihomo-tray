package app

import (
	"fmt"
	"log/slog"
	"path/filepath"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

func (a *Application) ElevatePrivilege(rollback func()) (restarted bool) {
	if sys.IsAdmin() {
		return false
	}
	slog.Debug("当前权限不足，正在请求管理员权限")
	err := sys.RunAsAdmin(a.Cfg.ExePath(), a.Cfg.BaseDir(), "--restarting")
	if err == nil {
		slog.Info("已成功拉起管理员权限进程，当前进程即将退出")
		return true
	}
	slog.Warn("管理员提权请求被取消或失败，已回滚相关设置")
	if rollback != nil {
		rollback()
	}
	a.ForcePushUIState()
	return false
}

func (a *Application) CheckAndReconcilePrivileges(isStartup bool) {
	cfg := a.Cfg.GetConfig()
	needsAdmin := cfg.General.RunAsAdmin || cfg.Config.Tun.Enable || (cfg.General.Autostart != nil && *cfg.General.Autostart)

	if needsAdmin && !sys.IsAdmin() {
		if isStartup {
			slog.Warn("当前以普通用户权限运行，已暂时停用需要管理员权限的功能")
			a.revertPrivilegedConfig()
			return
		}
		if restarted := a.ElevatePrivilege(a.revertPrivilegedConfig); restarted {
			a.State.ForceExitPhase()
		}
	}
}

func (a *Application) revertPrivilegedConfig() {
	a.Cfg.Update(func(c *domain.TrayConfig) {
		c.General.RunAsAdmin = false
		c.Config.Tun.Enable = false
		b := false
		c.General.Autostart = &b
	})
}

func (a *Application) ToggleAutoStart(enable bool) (restarted bool) {
	a.Cfg.Update(func(c *domain.TrayConfig) {
		b := enable
		c.General.Autostart = &b
	})

	restarted = a.ElevatePrivilege(func() {
		a.Cfg.Update(func(c *domain.TrayConfig) {
			b := !enable
			c.General.Autostart = &b
		})
	})

	if !restarted && sys.IsAdmin() {
		sys.ToggleAutoStart(domain.AppTaskName, a.Cfg.ExePath(), a.Cfg.BaseDir(), enable)
	}
	return restarted
}

func (a *Application) ToggleRunAsAdmin(enable bool) (restarted bool) {
	a.Cfg.Update(func(c *domain.TrayConfig) { c.General.RunAsAdmin = enable })
	return a.ElevatePrivilege(func() {
		a.Cfg.Update(func(c *domain.TrayConfig) { c.General.RunAsAdmin = false })
	})
}

func (a *Application) OpenBaseDir() {
	_ = sys.ExecuteSystemCommand(a.Cfg.BaseDir())
}

func (a *Application) OpenAppConfig() {
	jsonPath := filepath.Join(a.Cfg.BaseDir(), domain.TrayConfigName)
	_ = sys.ExecuteSystemCommand(jsonPath)
}

func (a *Application) OpenConfigFile(targetRelPath string) error {
	if targetRelPath == "" {
		targetRelPath = a.Cfg.GetActivePath()
	}
	if err := a.Cfg.ValidatePhysicalFile(targetRelPath); err != nil {
		return fmt.Errorf("配置文件不存在或已损坏: %w", err)
	}
	absPath := a.Cfg.GetProfileAbsPath(targetRelPath)
	_ = sys.ExecuteSystemCommand(absPath)
	return nil
}

func (a *Application) EditCurrentConfig() error {
	targetRelPath := a.Cfg.GetActivePath()
	if targetRelPath == "" {
		return fmt.Errorf("当前未选择任何运行配置")
	}
	return a.OpenConfigFile(targetRelPath)
}
