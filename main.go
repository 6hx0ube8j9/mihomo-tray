package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"mihomo-tray/internal/app"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
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

	guard, logWriter, cfgMgr := superviseStartup(exePath, baseDir)
	defer guard.Close()
	if logWriter != nil {
		defer logWriter.Close()
	}

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
		application.SafeShutdown(cancel)
	})

	setupSignalHandler(ctx, cancel)

	if guard != nil {
		guard.ListenWakeEvent(ctx, func() {
			application.UICommandCh <- domain.UICommand{Action: domain.ActionOpenWebUI}
		})
	}

	go func() {
		<-uiEngine.ReadyCh
		application.Bootstrap(ctx)
	}()

	if err := uiEngine.Run(); err != nil {
		slog.Error("界面运行异常", "error", err)
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
