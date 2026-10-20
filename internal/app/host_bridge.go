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
	cfg             *config.Manager
	st              *state.RuntimeState
	onElevateRevert func()
}

func NewHostBridge(cfg *config.Manager, st *state.RuntimeState, onElevateRevert func()) *HostBridge {
	return &HostBridge{
		cfg:             cfg,
		st:              st,
		onElevateRevert: onElevateRevert,
	}
}

func (h *HostBridge) ElevatePrivilege(rollback func()) (restarted bool) {
	if sys.IsAdmin() {
		return false
	}
	slog.Debug("请求管理员提权")
	err := sys.RunAsAdmin(h.cfg.ExePath(), h.cfg.BaseDir())
	if err == nil {
		slog.Info("提权进程已启动")
		return true
	}
	slog.Warn("提权取消或失败，已执行回滚", "error", err)
	if rollback != nil {
		rollback()
	}
	if h.onElevateRevert != nil {
		h.onElevateRevert()
	}
	return false
}

func (h *HostBridge) ReconcileRuntimePrivileges() {
	if sys.IsAdmin() {
		return
	}

	cfg := h.cfg.GetConfig()
	if cfg.Config.Tun.Enable {
		slog.Warn("非管理员权限运行，关闭 TUN 模式回退普通权限运行")
		h.cfg.Update(func(c *domain.TrayConfig) {
			c.Config.Tun.Enable = false
		})
	}
}

func (h *HostBridge) ToggleAutoStart(enable bool) (restarted bool) {
	if sys.IsAdmin() {
		if sys.ToggleAutoStart(domain.AppTaskName, h.cfg.ExePath(), h.cfg.BaseDir(), enable) {
			h.cfg.Update(func(c *domain.TrayConfig) {
				b := enable
				c.General.Autostart = &b
			})
			slog.Info("已直接同步自启动状态", "enabled", enable)
		}
		return false
	}

	h.cfg.Update(func(c *domain.TrayConfig) {
		b := enable
		c.General.Autostart = &b
	})

	return h.ElevatePrivilege(func() {
		h.cfg.Update(func(c *domain.TrayConfig) {
			b := !enable
			c.General.Autostart = &b
		})
	})
}

func (h *HostBridge) ToggleRunAsAdmin(enable bool) (restarted bool) {
	h.cfg.Update(func(c *domain.TrayConfig) {
		c.General.RunAsAdmin = enable
	})
	return h.ElevatePrivilege(func() {
		h.cfg.Update(func(c *domain.TrayConfig) {
			c.General.RunAsAdmin = false
		})
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
