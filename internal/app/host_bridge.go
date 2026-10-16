package app

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
)

type HostBridge struct {
	cfg *config.Manager
	st  *state.RuntimeState
}

func NewHostBridge(cfg *config.Manager, st *state.RuntimeState) *HostBridge {
	return &HostBridge{
		cfg: cfg,
		st:  st,
	}
}

func (h *HostBridge) ElevatePrivilege(rollback func()) (restarted bool) {
	if sys.IsAdmin() {
		return false
	}
	slog.Debug("请求管理员提权")
	err := sys.RunAsAdmin(h.cfg.ExePath(), h.cfg.BaseDir(), "--restarting")
	if err == nil {
		slog.Info("提权进程已启动，当前受限进程即将退出")
		return true
	}
	slog.Warn("提权取消或失败，已回滚设置", "err", err)
	if rollback != nil {
		rollback()
	}
	return false
}

func (h *HostBridge) CheckAndReconcilePrivileges(isStartup bool) {
	cfg := h.cfg.GetConfig()
	needsAdmin := cfg.General.RunAsAdmin || cfg.Config.Tun.Enable || h.cfg.IsAutostartEnabled()

	if needsAdmin && !sys.IsAdmin() {
		if isStartup {
			slog.Warn("非管理员权限运行，停用特权项以保证基础服务启动")
			h.revertPrivilegedConfig()
			return
		}
		if restarted := h.ElevatePrivilege(h.revertPrivilegedConfig); restarted {
			h.st.ForceExitPhase()
		}
	}
}

func (h *HostBridge) revertPrivilegedConfig() {
	h.cfg.Update(func(c *domain.TrayConfig) {
		c.General.RunAsAdmin = false
		c.Config.Tun.Enable = false
		b := false
		c.General.Autostart = &b
	})
}

func (h *HostBridge) ToggleAutoStart(enable bool) (restarted bool) {
	h.cfg.Update(func(c *domain.TrayConfig) {
		b := enable
		c.General.Autostart = &b
	})

	restarted = h.ElevatePrivilege(func() {
		h.cfg.Update(func(c *domain.TrayConfig) {
			b := !enable
			c.General.Autostart = &b
		})
	})

	if !restarted && sys.IsAdmin() {
		sys.ToggleAutoStart(domain.AppTaskName, h.cfg.ExePath(), h.cfg.BaseDir(), enable)
	}
	return restarted
}

func (h *HostBridge) ToggleRunAsAdmin(enable bool) (restarted bool) {
	h.cfg.Update(func(c *domain.TrayConfig) { c.General.RunAsAdmin = enable })
	return h.ElevatePrivilege(func() {
		h.cfg.Update(func(c *domain.TrayConfig) { c.General.RunAsAdmin = false })
	})
}

func (h *HostBridge) OpenBaseDir() {
	_ = sys.ExecuteSystemCommand(h.cfg.BaseDir())
}

func (h *HostBridge) OpenAppConfig() {
	jsonPath := filepath.Join(h.cfg.BaseDir(), domain.TrayConfigName)
	_ = sys.ExecuteSystemCommand(jsonPath)
}

func (h *HostBridge) OpenConfigFile(targetRelPath string) error {
	if targetRelPath == "" {
		targetRelPath = h.cfg.GetActivePath()
	}
	if err := h.cfg.ValidatePhysicalFile(targetRelPath); err != nil {
		return fmt.Errorf("配置文件不可用: %w", err)
	}
	absPath := h.cfg.GetProfileAbsPath(targetRelPath)
	_ = sys.ExecuteSystemCommand(absPath)
	return nil
}

func (h *HostBridge) EditCurrentConfig() error {
	targetRelPath := h.cfg.GetActivePath()
	if targetRelPath == "" {
		return errors.New("未选中任何运行配置")
	}
	return h.OpenConfigFile(targetRelPath)
}
