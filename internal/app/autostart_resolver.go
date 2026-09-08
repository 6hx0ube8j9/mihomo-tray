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
					slog.Info("本地配置禁用开机启动，删除计划任务")
					sys.ToggleAutoStart(exePath, baseDir, false)
					return "false"
				}
				slog.Info("检测到匹配的计划任务，同步本地配置为启用")
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
			slog.Info("本地配置启用开机启动，创建计划任务")
			sys.ToggleAutoStart(exePath, baseDir, true)
			return "true"
		} else if cfgAutostart == "" {
			return "false"
		}
	}

	return cfgAutostart
}
