package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"mihomo-tray/internal/app"
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
	setupWakeEventListener(ctx, guard, application.UICommandCh)

	go func() {
		<-uiEngine.ReadyCh
		application.Bootstrap(ctx)
	}()

	if err := uiEngine.Run(); err != nil {
		slog.Error("界面运行异常", "error", err)
	}
}
