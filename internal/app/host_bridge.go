package app

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

type HostBridge struct {
	cfg             *config.Manager
	onElevateRevert func()
}

func NewHostBridge(cfg *config.Manager, onElevateRevert func()) *HostBridge {
	return &HostBridge{
		cfg:             cfg,
		onElevateRevert: onElevateRevert,
	}
}

func (h *HostBridge) OpenFile(filePath string) error {
	if filePath == "" {
		return errors.New("目标文件路径不能为空")
	}
	return sys.ExecuteSystemCommand(filePath)
}

func (h *HostBridge) OpenFolder(dirPath string) error {
	if dirPath == "" {
		return errors.New("目标目录路径不能为空")
	}
	return sys.ExecuteSystemCommand(dirPath)
}

func (h *HostBridge) OpenBaseDir() error {
	return h.OpenFolder(h.cfg.BaseDir())
}

func (h *HostBridge) OpenAppConfig() error {
	jsonPath := filepath.Join(h.cfg.BaseDir(), domain.TrayConfigName)
	return h.OpenFile(jsonPath)
}

func (h *HostBridge) ElevatePrivilege(rollback func()) (restarted bool) {
	if sys.IsAdmin() {
		return false
	}
	slog.Debug("请求管理员提权")

	restartingArg := fmt.Sprintf("--restarting=%d", os.Getpid())
	err := sys.RunAsAdmin(h.cfg.ExePath(), h.cfg.BaseDir(), restartingArg)
	if err == nil {
		slog.Info("提权进程已启动")
		return true
	}

	slog.Warn("提权取消或失败，已回滚设置", "error", err)
	if rollback != nil {
		rollback()
	}
	if h.onElevateRevert != nil {
		h.onElevateRevert()
	}
	return false
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

	if !enable {
		slog.Info("已关闭以管理员身份运行")
		return false
	}

	return h.ElevatePrivilege(func() {
		h.cfg.Update(func(c *domain.TrayConfig) {
			c.General.RunAsAdmin = false
		})
	})
}
