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

	isAutostart := false
	isRestarting := false
	for _, arg := range os.Args[1:] {
		argClean := strings.ToLower(strings.TrimLeft(arg, "-"))
		if argClean == "autostart" {
			isAutostart = true
		} else if strings.Contains(argClean, "restarting") {
			isRestarting = true
		}
	}

	guard, isOwner := sys.TryAcquireSingleInstance(domain.AppMutexName, domain.ShowUIEventName, isRestarting)
	if !isOwner {
		sys.NotifyExistingInstance(domain.ShowUIEventName)
		return
	}
	defer guard.Close()

	logWriter := logger.Init(baseDir)
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
	cfgMgr := config.NewManager(baseDir, exePath, admin)
	cfgMgr.LoadAndInitMemory()
	logger.SyncLogLevel(cfgMgr.GetConfig().General.TrayLogLevel)

	slog.Info("==================== 程序启动 ====================", "pid", os.Getpid(), "admin", admin, "dir", baseDir)

	cfg := cfgMgr.GetConfig()

	osTaskExists := sys.CheckAutoStartStatus(domain.AppTaskName)
	isMine := false
	if osTaskExists {
		isMine = sys.IsTaskPathValid(domain.AppTaskName, exePath)
	}

	cfgAutostart := *cfg.General.Autostart
	finalAutostart := cfgAutostart

	if osTaskExists {
		if isMine {
			if !cfgAutostart {
				slog.Info("开机自启动已禁用，移除计划任务")
				sys.ToggleAutoStart(domain.AppTaskName, exePath, baseDir, false)
				finalAutostart = false
			}
		} else {
			if cfgAutostart {
				slog.Warn("计划任务指向其他路径，跳过自动同步")
				finalAutostart = false
			}
		}
	} else {
		if cfgAutostart {
			slog.Info("开机自启动已启用，注册计划任务")
			sys.ToggleAutoStart(domain.AppTaskName, exePath, baseDir, true)
		}
	}

	if cfgAutostart != finalAutostart {
		cfgMgr.Update(func(c *domain.TrayConfig) {
			b := finalAutostart
			c.General.Autostart = &b
		})
	}

	needsAdminStartup := cfgAutostart || cfg.General.RunAsAdmin

	if !admin && !isAutostart {
		if needsAdminStartup {
			slog.Info("当前配置需要管理员权限，执行提权流程")

			if cfgAutostart && osTaskExists && isMine {
				slog.Debug("尝试通过计划任务静默提权")
				if err := sys.RunScheduledTask(domain.AppTaskName); err == nil {
					slog.Info("计划任务提权成功，当前实例退出")
					exitProcess(0)
				} else {
					slog.Warn("计划任务提权失败，回退至系统 UAC 弹窗", "err", err)
				}
			}

			err := sys.RunAsAdmin(exePath, baseDir, "--restarting")
			if sys.IsUserCancelled(err) {
				slog.Info("用户取消授权，程序退出")
				exitProcess(0)
			} else if err == nil {
				slog.Info("UAC 授权成功，受限实例退出")
				exitProcess(0)
			} else {
				slog.Error("UAC 提权启动失败", "err", err)
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runtimeState := state.NewRuntimeState()
	application := app.NewApplication(cfgMgr, runtimeState)

	slog.Debug("初始化托盘引擎")
	uiEngine := ui.NewEngine(ctx, cancel, application.UICommandCh, application.UIStateNotifyCh, application.GetUIStateSnapshot)
	application.SetUIPort(uiEngine)

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
		slog.Debug("托盘引擎已就绪，开始执行初始化流程")
		application.Bootstrap(ctx)
	}()

	slog.Debug("启动托盘事件循环")
	if err := uiEngine.Run(); err != nil {
		slog.Error("托盘引擎运行异常", "err", err)
	}

	slog.Info("托盘事件循环结束，开始清理资源")
	cancel()

	runtimeState.ForceExitPhase()
	application.SafeShutdown(cancel)
	slog.Info("应用已安全退出")
}
