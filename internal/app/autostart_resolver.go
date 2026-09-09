package app

import (
	"log/slog"

	"mihomo-tray/internal/sys"
)

func ResolveAutostart(cfgAutostart, exePath, baseDir string) string {
	osTaskExists := sys.CheckAutoStartStatus()
	isMine := false
	if osTaskExists {
		isMine = sys.IsTaskPathValid(exePath)
	}

	if osTaskExists {
		if isMine {
			if cfgAutostart != "true" {
				if cfgAutostart == "false" {
					slog.Info("自启配置为禁用，清除计划任务")
					sys.ToggleAutoStart(exePath, baseDir, false)
					return "false"
				}
				slog.Info("检测到已有自启任务，自动启用配置")
				return "true"
			}
		} else {
			if cfgAutostart != "false" {
				slog.Warn("计划任务指向其他路径，跳过同步")
				return "false"
			}
		}
	} else {
		if cfgAutostart == "true" {
			slog.Info("自启配置为启用，创建计划任务")
			sys.ToggleAutoStart(exePath, baseDir, true)
			return "true"
		} else if cfgAutostart == "" {
			return "false"
		}
	}

	return cfgAutostart
}
