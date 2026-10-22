package app

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

// Platform 纯净的操作系统基础设施适配器（无业务状态，无配置写权限）
type Platform struct {
	exePath string
	baseDir string
}

func NewPlatform(exePath, baseDir string) *Platform {
	return &Platform{
		exePath: exePath,
		baseDir: baseDir,
	}
}

// OpenFile 调用系统默认关联程序打开指定文件
func (p *Platform) OpenFile(filePath string) error {
	if filePath == "" {
		return errors.New("目标文件路径不能为空")
	}
	return sys.ExecuteSystemCommand(filePath)
}

// OpenFolder 调用文件资源管理器打开指定目录
func (p *Platform) OpenFolder(dirPath string) error {
	if dirPath == "" {
		return errors.New("目标目录路径不能为空")
	}
	return sys.ExecuteSystemCommand(dirPath)
}

// SetAutoStartTask 注册或注销 Windows 任务计划程序开机自启
func (p *Platform) SetAutoStartTask(enable bool) bool {
	return sys.ToggleAutoStart(domain.AppTaskName, p.exePath, p.baseDir, enable)
}

// Elevate 请求 Windows UAC 提权并启动新实例
func (p *Platform) Elevate() (restarted bool, err error) {
	if sys.IsAdmin() {
		return false, nil
	}
	slog.Debug("向系统请求管理员提权")

	restartingArg := fmt.Sprintf("--restarting=%d", os.Getpid())
	if err := sys.RunAsAdmin(p.exePath, p.baseDir, restartingArg); err != nil {
		slog.Warn("提权请求被用户取消或执行失败", "error", err)
		return false, err
	}

	slog.Info("提权子进程已成功唤起")
	return true, nil
}

// ElevateWithRollback 带有回滚闭包的高级提权辅助函数，方便与 Tuner 协同
func (p *Platform) ElevateWithRollback(rollback func()) (restarted bool) {
	restarted, err := p.Elevate()
	if err != nil || !restarted {
		if rollback != nil {
			rollback()
		}
		return false
	}
	return true
}
