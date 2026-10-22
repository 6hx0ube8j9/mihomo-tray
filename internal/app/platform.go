// internal/app/platform.go
package app

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

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

// IsAdmin 检查当前进程是否具备管理员权限
func (p *Platform) IsAdmin() bool {
	return sys.IsAdmin()
}

// OpenFile 调用默认程序打开文件
func (p *Platform) OpenFile(filePath string) error {
	if filePath == "" {
		return errors.New("目标文件路径不能为空")
	}
	return sys.ExecuteSystemCommand(filePath)
}

// OpenFolder 打开指定目录
func (p *Platform) OpenFolder(dirPath string) error {
	if dirPath == "" {
		return errors.New("目标目录路径不能为空")
	}
	return sys.ExecuteSystemCommand(dirPath)
}

// WriteClipboard 写入系统剪贴板
func (p *Platform) WriteClipboard(text string) error {
	return sys.WriteToClipboard(text)
}

// SetAutoStartTask 注册或注销 Windows 计划任务开机自启
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
