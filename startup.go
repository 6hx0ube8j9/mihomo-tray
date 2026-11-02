package main

import (
	"context"
	"io"
	"log/slog"
	"os"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/logger"
	"mihomo-tray/internal/sys"
)

func superviseStartup(exePath, baseDir string) (*sys.SingleInstanceGuard, io.Closer, *config.Manager) {
	guard, isOwner := sys.TryAcquireSingleInstance(domain.AppMutexName, domain.ShowUIEventName, 0)
	if !isOwner {
		sys.NotifyExistingInstance(domain.ShowUIEventName)
		os.Exit(0)
	}

	logWriter := logger.Init(baseDir, false)
	cfgMgr := config.NewManager(baseDir, exePath)
	cfgMgr.LoadAndInitMemory()
	logger.SyncLogLevel(cfgMgr.GetConfig().General.TrayLogLevel)

	slog.Info("程序启动", "admin", sys.IsAdmin(), "pid", os.Getpid())
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
		}
	})
}

func setupSignalHandler(_ context.Context, _ context.CancelFunc) {}
