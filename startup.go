package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

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
		// 仅在非自启动场景下作为信使唤醒面板
		if !isAutostart {
			sys.NotifyExistingInstance(domain.ShowUIEventName)
			// 撑住前台特权窗口期，让主进程有足够时间完成前台窗口渲染，避免秒退导致特权被操作系统撤销
			time.Sleep(200 * time.Millisecond)
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

func setupWakeEventListener(ctx context.Context, guard *sys.SingleInstanceGuard, cmdCh chan<- domain.UICommand) {
	if guard == nil {
		return
	}

	guard.ListenWakeEvent(ctx, func() {
		select {
		case cmdCh <- domain.UICommand{Action: domain.ActionOpenWebUI}:
		default:
			slog.Warn("唤醒指令队列已满，忽略本次触发")
		}
	})
}

func setupSignalHandler(ctx context.Context, cancel context.CancelFunc) {
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(sigCh)
		select {
		case sig := <-sigCh:
			slog.Info("收到终止信号", "signal", sig)
			cancel()
		case <-ctx.Done():
		}
	}()
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
			pidStr := strings.TrimPrefix(argClean, "restarting=")
			if pid, err := strconv.ParseUint(pidStr, 10, 32); err == nil {
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
		err := sys.RunScheduledTask(domain.AppTaskName)
		if err == nil {
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
	err := sys.RunAsAdmin(exePath, baseDir, args...)
	if err == nil {
		exitProcess(0)
		return
	}

	if sys.IsUserCancelled(err) {
		if runAsAdmin {
			exitProcess(0)
			return
		}
		cfgMgr.Update(func(c *domain.TrayConfig) {
			b := false
			c.General.Autostart = &b
		})
		return
	}

	if runAsAdmin {
		exitProcess(1)
		return
	}
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
