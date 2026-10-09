package app

import (
	"errors"
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
	slog.Debug("请求管理员提权")
	err := sys.RunAsAdmin(a.Cfg.ExePath(), a.Cfg.BaseDir(), "--restarting")
	if err == nil {
		slog.Info("提权进程已启动，当前进程即将退出")
		return true
	}
	slog.Warn("提权取消或失败，已回滚设置", "err", err)
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
			slog.Warn("非管理员权限运行，停用特权项")
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
		return fmt.Errorf("配置文件错误: %w", err)
	}
	absPath := a.Cfg.GetProfileAbsPath(targetRelPath)
	_ = sys.ExecuteSystemCommand(absPath)
	return nil
}

func (a *Application) EditCurrentConfig() error {
	targetRelPath := a.Cfg.GetActivePath()
	if targetRelPath == "" {
		return errors.New("未选择运行配置")
	}
	return a.OpenConfigFile(targetRelPath)
}
