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

	isAutostart, isRestarting := parseArgs()

	guard, isOwner := sys.TryAcquireSingleInstance(domain.AppMutexName, domain.ShowUIEventName, isRestarting)
	if !isOwner {
		sys.NotifyExistingInstance(domain.ShowUIEventName)
		return
	}
	defer guard.Close()

	logWriter := logger.Init(baseDir, isRestarting)
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

	slog.Info("程序启动", "admin", admin)
	slog.Debug("进程信息", "pid", os.Getpid(), "dir", baseDir)

	finalAutostart := syncAutostart(cfgMgr, exePath, baseDir)
	handleElevation(cfgMgr, exePath, baseDir, isAutostart, finalAutostart, exitProcess)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runtimeState := state.NewRuntimeState()
	application := app.NewApplication(cfgMgr, runtimeState)

	defer func() {
		if r := recover(); r != nil {
			slog.Error("捕获未处理异常，执行清理", "recover", r)
		}
		slog.Info("托盘事件循环结束，开始清理资源")
		application.SafeShutdown(cancel)
	}()

	slog.Debug("初始化托盘引擎")
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
			slog.Info("接收到系统终止信号", "signal", sig)
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	guard.ListenWakeEvent(ctx, func() {
		application.UICommandCh <- domain.UICommand{Action: domain.ActionOpenWebUI}
	})

	slog.Debug("启动后台业务服务")
	go func() {
		<-uiEngine.ReadyCh
		slog.Debug("托盘引擎已就绪，开始初始化")
		application.Bootstrap(ctx)
	}()

	slog.Debug("启动托盘事件循环")
	if err := uiEngine.Run(); err != nil {
		slog.Error("托盘引擎运行异常", "err", err)
	}
}

func parseArgs() (isAutostart, isRestarting bool) {
	for _, arg := range os.Args[1:] {
		clean := strings.ToLower(strings.TrimLeft(arg, "-"))
		if clean == "autostart" {
			isAutostart = true
		} else if strings.Contains(clean, "restarting") {
			isRestarting = true
		}
	}
	return
}

func syncAutostart(cfgMgr *config.Manager, exePath, baseDir string) bool {
	osTaskExists := sys.CheckAutoStartStatus(domain.AppTaskName)
	isMine := false
	if osTaskExists {
		isMine = sys.IsTaskPathValid(domain.AppTaskName, exePath)
	}

	cfgAutostart := cfgMgr.IsAutostartEnabled()
	finalAutostart := cfgAutostart

	if osTaskExists {
		if isMine {
			if !cfgAutostart {
				slog.Info("开机自启动已禁用，移除计划任务")
				sys.ToggleAutoStart(domain.AppTaskName, exePath, baseDir, false)
				finalAutostart = false
			}
		} else if cfgAutostart {
			slog.Warn("计划任务路径不匹配，跳过自动同步")
			finalAutostart = false
		}
	} else if cfgAutostart {
		slog.Info("开机自启动已启用，注册计划任务")
		sys.ToggleAutoStart(domain.AppTaskName, exePath, baseDir, true)
	}

	if cfgAutostart != finalAutostart {
		cfgMgr.Update(func(c *domain.TrayConfig) {
			b := finalAutostart
			c.General.Autostart = &b
		})
	}
	return finalAutostart
}

func handleElevation(cfgMgr *config.Manager, exePath, baseDir string, isAutostart, finalAutostart bool, exitProcess func(int)) {
	if sys.IsAdmin() || isAutostart {
		return
	}

	cfg := cfgMgr.GetConfig()
	if !finalAutostart && !cfg.General.RunAsAdmin {
		return
	}

	slog.Info("当前配置需要管理员权限，执行提权流程")
	osTaskExists := sys.CheckAutoStartStatus(domain.AppTaskName)
	if finalAutostart && osTaskExists && sys.IsTaskPathValid(domain.AppTaskName, exePath) {
		slog.Debug("尝试通过计划任务静默提权")
		if err := sys.RunScheduledTask(domain.AppTaskName); err == nil {
			slog.Info("计划任务提权成功，退出当前实例")
			exitProcess(0)
		} else {
			slog.Warn("计划任务提权失败，尝试请求系统提权", "err", err)
		}
	}

	err := sys.RunAsAdmin(exePath, baseDir, "--restarting")
	if sys.IsUserCancelled(err) {
		slog.Info("用户取消授权，程序退出")
		exitProcess(0)
	} else if err == nil {
		slog.Info("提权授权成功，退出当前实例")
		exitProcess(0)
	} else {
		slog.Error("提权启动失败", "err", err)
	}
}
