package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
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
		if logWriter != nil {
			_ = logWriter.Close()
		}
		if guard != nil {
			guard.Close()
		}
		os.Exit(code)
	}

	var dirErr error
	if !admin {
		dirErr = sys.ValidateDirWritable(baseDir)
	}

	canWrite := admin || (dirErr == nil)
	taskInSync, needElevate := inspectAndSyncAutostart(cfgMgr, exePath, baseDir, admin, canWrite)
	handleElevation(cfgMgr, exePath, baseDir, admin, isAutostart, taskInSync, needElevate, dirErr, &guard, exitProcess)

	return guard, logWriter, cfgMgr
}

func buildSelfElevationArgs(extraArgs ...string) []string {
	var cleanArgs []string
	for _, arg := range os.Args[1:] {
		argClean := strings.ToLower(strings.TrimLeft(arg, "-"))
		if argClean == "restarting" || strings.HasPrefix(argClean, "restarting=") || argClean == "enable-tun" {
			continue
		}
		cleanArgs = append(cleanArgs, arg)
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

func fileExists(path string) bool {
	clean := strings.Trim(strings.TrimSpace(path), `"`)
	if clean == "" {
		return false
	}
	info, err := os.Stat(clean)
	return err == nil && !info.IsDir()
}

func inspectAndSyncAutostart(cfgMgr *config.Manager, exePath, baseDir string, isAdmin, canWrite bool) (taskInSync, needElevate bool) {
	oldPath, osTaskExists := sys.QueryAutoStartTask(domain.AppTaskName)
	cfgAutostart := cfgMgr.GetConfig().IsAutostartEnabled()

	if !osTaskExists {
		if cfgAutostart {
			if isAdmin {
				slog.Info("注册自启动计划任务")
				if err := sys.ToggleAutoStart(domain.AppTaskName, exePath, baseDir, true); err != nil {
					slog.Error("注册自启动计划任务失败", "err", err)
				} else {
					taskInSync = true
				}
			} else {
				needElevate = true
			}
		}
		return taskInSync, needElevate
	}

	cleanExe := filepath.Clean(strings.Trim(exePath, `"`))
	if oldPath != "" && strings.EqualFold(oldPath, cleanExe) {
		taskInSync = true

		if !cfgAutostart {
			if !isAdmin {
				if canWrite {
					slog.Info("同步已有自启动任务状态为开启")
					cfgMgr.Update(func(c *domain.TrayConfig) {
						b := true
						c.General.Autostart = &b
					})
				}
				return true, false
			}

			slog.Info("删除已关闭的自启动计划任务")
			if err := sys.ToggleAutoStart(domain.AppTaskName, exePath, baseDir, false); err != nil {
				slog.Error("删除自启动计划任务失败", "err", err)
			} else {
				taskInSync = false
			}
			return taskInSync, false
		}

		return taskInSync, false
	}

	if !fileExists(oldPath) {
		if cfgAutostart {
			if isAdmin {
				slog.Info("更新失效的自启动任务", "path", exePath)
				if err := sys.ToggleAutoStart(domain.AppTaskName, exePath, baseDir, true); err != nil {
					slog.Error("更新自启动计划任务失败", "err", err)
				} else {
					taskInSync = true
				}
			} else {
				needElevate = true
			}
		}
		return taskInSync, needElevate
	}

	if cfgAutostart {
		if isAdmin {
			slog.Info("覆盖自启动任务路径", "path", exePath)
			if err := sys.ToggleAutoStart(domain.AppTaskName, exePath, baseDir, true); err != nil {
				slog.Error("覆盖自启动计划任务失败", "err", err)
			} else {
				taskInSync = true
			}
			return taskInSync, false
		}

		if canWrite {
			slog.Warn("自启动任务已被其他路径占用，关闭当前自启", "old", oldPath)
			cfgMgr.Update(func(c *domain.TrayConfig) {
				b := false
				c.General.Autostart = &b
			})
		}
	}

	return false, false
}

func handleElevation(
	cfgMgr *config.Manager,
	exePath, baseDir string,
	isAdmin, isAutostart, taskInSync, needElevate bool,
	dirErr error,
	guardPtr **sys.SingleInstanceGuard,
	exitProcess func(int),
) {
	if isAdmin {
		return
	}

	isDirRestricted := (dirErr != nil)
	if isAutostart && !isDirRestricted {
		return
	}

	cfg := cfgMgr.GetConfig()
	cfgAutostart := cfg.IsAutostartEnabled()
	runAsAdmin := cfg.General.RunAsAdmin

	if taskInSync && (cfgAutostart || runAsAdmin || isDirRestricted) {
		slog.Info("通过计划任务执行静默提权")

		(*guardPtr).Close()
		err := sys.RunScheduledTask(domain.AppTaskName)
		if err == nil {
			exitProcess(0)
			return
		}

		slog.Warn("静默提权失败，改用 UAC 弹窗", "err", err)
		reacquiredGuard, isOwner := sys.TryAcquireSingleInstance(domain.AppMutexName, domain.ShowUIEventName, 0)
		if !isOwner {
			slog.Warn("单实例锁已被接管，当前实例退出")
			exitProcess(0)
			return
		}
		*guardPtr = reacquiredGuard
	}

	if !needElevate && !runAsAdmin && !isDirRestricted {
		return
	}

	if isDirRestricted {
		slog.Warn("程序目录无写入权限，请求管理员权限", "dir", baseDir, "err", dirErr)
	} else {
		slog.Info("请求管理员权限")
	}

	args := buildSelfElevationArgs()
	err := sys.RunAsAdmin(exePath, baseDir, args...)
	if err == nil {
		exitProcess(0)
		return
	}

	if sys.IsUserCancelled(err) {
		if runAsAdmin || isDirRestricted {
			slog.Warn("用户取消授权，程序退出")
			exitProcess(0)
			return
		}

		slog.Warn("用户取消授权，继续普通权限运行并关闭自启")
		cfgMgr.Update(func(c *domain.TrayConfig) {
			b := false
			c.General.Autostart = &b
		})
		return
	}

	slog.Error("请求管理员权限失败", "err", err)
	if runAsAdmin || isDirRestricted {
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
			slog.Info("收到终止信号", "sig", sig)
			cancel()
		case <-ctx.Done():
		}
	}()
}

func setupWakeEventListener(ctx context.Context, guard *sys.SingleInstanceGuard, cmdCh chan<- domain.UICommand) {
	if guard == nil {
		return
	}

	guard.ListenWakeEvent(ctx, func() {
		select {
		case cmdCh <- domain.UICommand{
			Action: domain.ActionOpenWebUI,
			OnDone: func() {
				guard.AckWake()
			},
		}:
		default:
			slog.Warn("唤醒指令队列已满，放弃发送")
			guard.AckWake()
		}
	})
}
