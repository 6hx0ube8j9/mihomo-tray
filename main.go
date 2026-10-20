package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"mihomo-tray/internal/app"
	"mihomo-tray/internal/config"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/logger"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/ui"
)

func main() {
	runtime.LockOSThread()

	exePath, err := os.Executable()
	if err != nil {
		return
	}
	baseDir := filepath.Dir(exePath)
	_ = os.Chdir(baseDir)

	isAutostart := parseIsAutostart()
	restartingPID := sys.ParseRestartingPID(os.Args[1:])

	guard, isOwner := sys.TryAcquireSingleInstance(domain.AppMutexName, domain.ShowUIEventName, restartingPID, isAutostart)
	if !isOwner {
		sys.NotifyExistingInstance(domain.ShowUIEventName)
		return
	}
	defer guard.Close()

	logWriter := logger.Init(baseDir, restartingPID > 0)
	if logWriter != nil {
		defer logWriter.Close()
	}

	exitProcess := func(code int) {
		guard.Close()
		if logWriter != nil {
			_ = logWriter.Close()
		}
		os.Exit(code)
	}

	admin := sys.IsAdmin()
	cfgMgr := config.NewManager(baseDir, exePath)
	cfgMgr.LoadAndInitMemory()
	logger.SyncLogLevel(cfgMgr.GetConfig().General.TrayLogLevel)

	slog.Info("程序启动", "admin", admin, "pid", os.Getpid())

	taskInSync, needElevate := inspectAndSyncAutostart(cfgMgr, exePath, baseDir, admin)
	handleElevation(cfgMgr, exePath, baseDir, isAutostart, taskInSync, needElevate, exitProcess)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runtimeState := state.NewRuntimeState()
	application := app.NewApplication(cfgMgr, runtimeState)

	defer func() {
		if r := recover(); r != nil {
			slog.Error("程序异常退出", "error", r)
		}
		application.SafeShutdown(cancel)
	}()

	uiEngine := ui.NewEngine(ctx, cancel, application.UICommandCh, application.UIStateNotifyCh, application.Presenter.GetSnapshot)
	application.SetUIPort(uiEngine)

	uiEngine.SetOnShutdown(func() {
		application.State.ForceExitPhase()
		cancel()
		if application.ProxyGuard != nil {
			application.ProxyGuard.SafeShutdownCleanup()
		}
	})

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

	guard.ListenWakeEvent(ctx, func() {
		select {
		case application.UICommandCh <- domain.UICommand{Action: domain.ActionOpenWebUI}:
		default:
		}
	})

	go func() {
		<-uiEngine.ReadyCh
		application.Bootstrap(ctx)
	}()

	if err := uiEngine.Run(); err != nil {
		slog.Error("界面运行异常", "error", err)
	}
}

func parseIsAutostart() bool {
	for _, arg := range os.Args[1:] {
		if strings.EqualFold(strings.TrimLeft(arg, "-"), "autostart") {
			return true
		}
	}
	return false
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
			if isAdmin {
				slog.Info("移除自启动任务")
				if sys.ToggleAutoStart(domain.AppTaskName, exePath, baseDir, false) {
					taskInSync = false
				}
			} else {
				needElevate = true
			}
		}
		return taskInSync, needElevate
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
	} else {
		slog.Warn("已有自启动任务指向其他有效路径，跳过同步", "path", oldPath)
	}

	return taskInSync, needElevate
}

func handleElevation(cfgMgr *config.Manager, exePath, baseDir string, isAutostart, taskInSync, needElevate bool, exitProcess func(int)) {
	if sys.IsAdmin() || isAutostart {
		return
	}

	cfgAutostart := cfgMgr.IsAutostartEnabled()
	runAsAdmin := cfgMgr.GetConfig().General.RunAsAdmin

	if taskInSync && (cfgAutostart || runAsAdmin) {
		slog.Info("检测到自启动任务已就绪，通过计划任务执行静默提权")
		err := sys.RunScheduledTask(domain.AppTaskName)
		if err == nil {
			slog.Info("计划任务静默提权成功，受限实例退出")
			exitProcess(0)
			return
		}
		slog.Warn("计划任务静默提权失败，准备降级为 UAC 授权", "error", err)
	}

	if !needElevate && !runAsAdmin {
		return
	}

	slog.Info("正在请求管理员权限")
	err := sys.RunAsAdmin(exePath, baseDir)
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
		slog.Warn("用户取消授权，以当前权限运行")
		return
	}

	slog.Error("请求管理员权限失败", "error", err)
	if runAsAdmin {
		exitProcess(1)
		return
	}
}
