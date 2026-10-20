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
		sys.NotifyExistingInstance(domain.ShowUIEventName)
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
				slog.Info("注册自启动任务")
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
				slog.Info("检测到系统已存在有效自启动任务，自动认领并同步本地配置为开启")
				cfgMgr.Update(func(c *domain.TrayConfig) {
					b := true
					c.General.Autostart = &b
				})
				return true, false
			}

			slog.Info("用户主动关闭自启动，执行计划任务注销")
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
				slog.Info("检测到旧路径已失效，更新自启动任务", "old", oldPath, "new", exePath)
				taskInSync = sys.ToggleAutoStart(domain.AppTaskName, exePath, baseDir, true)
			} else {
				needElevate = true
			}
		}
		return taskInSync, needElevate
	}

	if cfgAutostart {

		if isAdmin {
			slog.Info("用户显式授权管理员权限，强制覆写自启动任务指向当前路径", "old", oldPath, "new", exePath)
			taskInSync = sys.ToggleAutoStart(domain.AppTaskName, exePath, baseDir, true)
			return taskInSync, false
		}

		slog.Warn("已有自启动任务指向其他存活程序，当前普通权限副本放弃自启动", "path", oldPath)
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
		slog.Info("检测到自启动任务已就绪，通过计划任务执行静默提权")
		
		guard.Close()
		err := sys.RunScheduledTask(domain.AppTaskName)
		if err == nil {
			slog.Info("计划任务静默提权成功，受限实例退出")
			exitProcess(0)
			return
		}

		slog.Warn("计划任务静默提权失败，重新抢占锁并降级为 UAC 弹窗", "error", err)

		reacquiredGuard, isOwner := sys.TryAcquireSingleInstance(domain.AppMutexName, domain.ShowUIEventName, 0)
		if !isOwner {
			slog.Warn("锁已被其他实例接管，当前进程安全退出")
			exitProcess(0)
			return
		}
		*guard = *reacquiredGuard
	}

	if !needElevate && !runAsAdmin {
		return
	}

	slog.Info("正在请求管理员权限")
	args := BuildSelfElevationArgs()
	err := sys.RunAsAdmin(exePath, baseDir, args...)
	if err == nil {
		slog.Info("UAC 授权成功，受限实例退出")
		exitProcess(0)
		return
	}
	
	if sys.IsUserCancelled(err) {
		if runAsAdmin {
			slog.Info("用户取消授权，程序退出")
			exitProcess(0)
			return
		}
		slog.Warn("用户取消授权，以当前权限运行，并重置自启配置为 false")
		cfgMgr.Update(func(c *domain.TrayConfig) {
			b := false
			c.General.Autostart = &b
		})
		return
	}

	slog.Error("请求管理员权限失败", "error", err)
	if runAsAdmin {
		exitProcess(1)
		return
	}
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

func setupWakeEventListener(ctx context.Context, guard *sys.SingleInstanceGuard, cmdCh chan<- domain.UICommand) {
	guard.ListenWakeEvent(ctx, func() {
		select {
		case cmdCh <- domain.UICommand{Action: domain.ActionOpenWebUI}:
		default:
		}
	})
}
