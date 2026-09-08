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
					slog.Info("检测到本地配置要求关闭自启，清除属于自身的系统任务")
					sys.ToggleAutoStart(exePath, baseDir, false)
					return "false"
				}
				slog.Info("识别到正确的系统自启任务，自愈恢复本地配置为开启")
				return "true"
			}
		} else {
			if cfgAutostart != "false" {
				slog.Warn("检测到系统自启任务归属其他路径，已自动放弃接管以防越权")
				return "false"
			}
		}
	} else {
		if cfgAutostart == "true" {
			slog.Info("检测到本地配置要求开机自启，补充创建系统任务")
			sys.ToggleAutoStart(exePath, baseDir, true)
			return "true"
		} else if cfgAutostart == "" {
			return "false"
		}
	}
	
	return cfgAutostart
}
