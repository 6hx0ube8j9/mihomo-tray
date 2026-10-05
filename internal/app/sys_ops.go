package app

import (
	"fmt"
	"path/filepath"
	"log/slog"
	
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

func (a *Application) ElevatePrivilege(rollback func()) (restarted bool) {
	if sys.IsAdmin() {
		return false
	}
	slog.Debug("操作受限，正在拉起 UAC 申请提权")
	err := sys.RunAsAdmin(a.Cfg.ExePath(), a.Cfg.BaseDir(), "--restarting")
	if err == nil {
		slog.Info("高权限实例已唤起，当前受限实例准备退出")
		return true
	}
	slog.Warn("UAC 提权被取消或失败，回滚操作状态")
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
			slog.Warn("系统正以普通用户权限启动，高级网络功能已被暂时停用")
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
		return fmt.Errorf("配置文件已损坏或丢失，无法打开。\n\n%w", err)
	}
	absPath := filepath.Join(a.Cfg.BaseDir(), filepath.FromSlash(targetRelPath))
	_ = sys.ExecuteSystemCommand(absPath)
	return nil
}

func (a *Application) EditCurrentConfig() error {
	targetRelPath := a.Cfg.GetActivePath()
	if targetRelPath == "" {
		return fmt.Errorf("当前系统处于空转状态，没有正在运行的配置")
	}
	return a.OpenConfigFile(targetRelPath)
}
