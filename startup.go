package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/logger"
	"mihomo-tray/internal/sys"
)

func superviseStartup(exePath, baseDir string) (*sys.SingleInstanceGuard, io.Closer, *config.Manager) {
	isAutostart := parseIsAutostart(os.Args[1:])
	restartingPID := parseRestartingPID(os.Args[1:])

	guard, isOwner := sys.TryAcquireSingleInstance(domain.AppMutexName, domain.ShowUIEventName, restartingPID)
	if !isOwner {
		if !isAutostart {
			sys.NotifyExistingInstance(domain.ShowUIEventName)
		}
		os.Exit(0)
	}

	logWriter := logger.Init(baseDir, restartingPID > 0)
	admin := sys.IsAdmin()
	cfgMgr := config.NewManager(baseDir, exePath)
	cfgMgr.LoadAndInitMemory()
	logger.SyncLogLevel(cfgMgr.GetConfig().General.TrayLogLevel)

	slog.Info("程序启动", "admin", admin, "pid", os.Getpid())

	exitProcess := func(code int) {
		guard.Close()
		if logWriter != nil {
			_ = logWriter.Close()
		}
		os.Exit(code)
	}

	taskInSync, needElevate := inspectAndSyncAutostart(cfgMgr, exePath, baseDir, admin)
	handleElevation(cfgMgr, exePath, baseDir, isAutostart, taskInSync, needElevate, guard, exitProcess)

	return guard, logWriter, cfgMgr
}

func parseIsAutostart(args []string) bool {
	for _, arg := range args {
		if strings.EqualFold(strings.TrimLeft(arg, "-"), "autostart") {
			return true
		}
	}
	return false
}

func parseRestartingPID(args []string) uint32 {
	for _, arg := range args {
		argClean := strings.ToLower(strings.TrimLeft(arg, "-"))
		if strings.HasPrefix(argClean, "restarting=") {
			if pid, err := strconv.ParseUint(strings.TrimPrefix(argClean, "restarting="), 10, 32); err == nil {
				return uint32(pid)
			}
		} else if argClean == "restarting" {
			return 1
		}
	}
	return 0
}

func inspectAndSyncAutostart(cfgMgr *config.Manager, exePath, baseDir string, isAdmin bool) (taskInSync bool, needElevate bool) {
	osTaskExists := sys.CheckAutoStartStatus(domain.AppTaskName)
	cfgAutostart := cfgMgr.IsAutostartEnabled()

	if !osTaskExists {
		if cfgAutostart {
			if isAdmin {
				taskInSync = sys.ToggleAutoStart(domain.AppTaskName, exePath, baseDir, true)
			} else {
				needElevate = true
			}
		}
		return taskInSync, needElevate
	}

	if sys.IsTaskPathValid(domain.AppTaskName, exePath) {
		taskInSync = true
		if !cfgAutostart {
			if !isAdmin {
				cfgMgr.Update(func(c *domain.TrayConfig) {
					b := true
					c.General.Autostart = &b
				})
				return true, false
			}
			if sys.ToggleAutoStart(domain.AppTaskName, exePath, baseDir, false) {
				taskInSync = false
			}
		}
		return taskInSync, false
	}

	oldPath := sys.GetTaskPath(domain.AppTaskName)
	if !sys.CheckPathExists(oldPath) {
		if cfgAutostart {
			if isAdmin {
				taskInSync = sys.ToggleAutoStart(domain.AppTaskName, exePath, baseDir, true)
			} else {
				needElevate = true
			}
		}
		return taskInSync, needElevate
	}

	if cfgAutostart {
		if isAdmin {
			taskInSync = sys.ToggleAutoStart(domain.AppTaskName, exePath, baseDir, true)
			return taskInSync, false
		}
		cfgMgr.Update(func(c *domain.TrayConfig) {
			b := false
			c.General.Autostart = &b
		})
	}

	return false, false
}

func handleElevation(cfgMgr *config.Manager, exePath, baseDir string, isAutostart, taskInSync, needElevate bool, guard *sys.SingleInstanceGuard, exitProcess func(int)) {
	if sys.IsAdmin() || isAutostart {
		return
	}

	cfgAutostart := cfgMgr.IsAutostartEnabled()
	runAsAdmin := cfgMgr.GetConfig().General.RunAsAdmin

	if taskInSync && (cfgAutostart || runAsAdmin) {
		guard.Close()
		if err := sys.RunScheduledTask(domain.AppTaskName); err == nil {
			exitProcess(0)
			return
		}

		reacquiredGuard, isOwner := sys.TryAcquireSingleInstance(domain.AppMutexName, domain.ShowUIEventName, 0)
		if !isOwner {
			exitProcess(0)
			return
		}
		*guard = *reacquiredGuard
	}

	if !needElevate && !runAsAdmin {
		return
	}

	args := BuildSelfElevationArgs()
	if err := sys.RunAsAdmin(exePath, baseDir, args...); err == nil {
		exitProcess(0)
		return
	}

	if runAsAdmin {
		exitProcess(1)
		return
	}

	cfgMgr.Update(func(c *domain.TrayConfig) {
		b := false
		c.General.Autostart = &b
	})
}

func BuildSelfElevationArgs(extraArgs ...string) []string {
	var cleanArgs []string
	for _, arg := range os.Args[1:] {
		argLower := strings.ToLower(arg)
		if !strings.Contains(argLower, "restarting") && !strings.Contains(argLower, "enable-tun") {
			cleanArgs = append(cleanArgs, arg)
		}
	}
	cleanArgs = append(cleanArgs, fmt.Sprintf("--restarting=%d", os.Getpid()))
	for _, extra := range extraArgs {
		if strings.TrimSpace(extra) != "" {
			cleanArgs = append(cleanArgs, extra)
		}
	}
	return cleanArgs
}
