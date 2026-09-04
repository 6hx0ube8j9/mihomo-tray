package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"log/slog"

	"golang.org/x/sys/windows"

	"mihomo-tray/internal/app"
	"mihomo-tray/internal/config"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/ui"
)

const (
	AppMutex    = "Mihomo_Tray_Mutex"
	ShowUIEvent = "Mihomo_Tray_Mutex_ShowUI"
	MaxLogSize  = 512 * 1024
)

var GlobalLogLevel = new(slog.LevelVar)

type rollingLogWriter struct {
	mu       sync.Mutex
	logPath  string
	bakPath  string
	file     *os.File
	currSize int64
}

func newRollingLogWriter(baseDir string) *rollingLogWriter {
	w := &rollingLogWriter{
		logPath: filepath.Join(baseDir, "mihomo-tray.log"),
		bakPath: filepath.Join(baseDir, "mihomo-tray.log.bak"),
	}
	w.open()
	return w
}

func (w *rollingLogWriter) open() {
	fi, err := os.Stat(w.logPath)
	if err == nil {
		w.currSize = fi.Size()
		if w.currSize >= MaxLogSize {
			w.rotate()
			return
		}
	} else {
		w.currSize = 0
	}
	w.file, _ = os.OpenFile(w.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
}

func (w *rollingLogWriter) rotate() {
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}
	_ = os.Rename(w.logPath, w.bakPath)
	w.file, _ = os.OpenFile(w.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	w.currSize = 0
}

func (w *rollingLogWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return len(p), nil
	}

	if w.currSize+int64(len(p)) > MaxLogSize {
		w.rotate()
	}

	n, err = w.file.Write(p)
	if err == nil {
		w.currSize += int64(n)
	}
	return n, err
}

func (w *rollingLogWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}
}

func initEarlyLogger(baseDir string) *rollingLogWriter {
	writer := newRollingLogWriter(baseDir)
	GlobalLogLevel.Set(slog.LevelError)

	opts := &slog.HandlerOptions{
		Level: GlobalLogLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				t := a.Value.Time()
				a.Value = slog.StringValue(t.Format("2006/01/02 15:04:05"))
			}
			return a
		},
	}

	logger := slog.New(slog.NewTextHandler(writer, opts))
	slog.SetDefault(logger)

	return writer
}

func syncLogLevel(cfgMgr *config.Manager) {
	levelStr := cfgMgr.GetJSON("tray_log_level")
	switch strings.ToLower(levelStr) {
	case "silent":
		GlobalLogLevel.Set(slog.Level(100))
	case "debug":
		GlobalLogLevel.Set(slog.LevelDebug)
	case "info":
		GlobalLogLevel.Set(slog.LevelInfo)
	case "warn":
		GlobalLogLevel.Set(slog.LevelWarn)
	case "error":
		GlobalLogLevel.Set(slog.LevelError)
	default:
		GlobalLogLevel.Set(slog.LevelError)
	}
}

func main() {
	runtime.LockOSThread()

	exePath, err := os.Executable()
	if err != nil {
		return
	}
	baseDir := filepath.Dir(exePath)
	_ = os.Chdir(baseDir)

	logWriter := initEarlyLogger(baseDir)
	if logWriter != nil {
		defer logWriter.Close()
	}

	cfgMgr := config.NewManager(baseDir, exePath)
	syncLogLevel(cfgMgr)

	slog.Info("程序启动", "PID", os.Getpid(), "工作目录", baseDir)

	mName, _ := windows.UTF16PtrFromString(AppMutex)
	hM, err := windows.CreateMutex(nil, false, mName)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || err == windows.ERROR_ALREADY_EXISTS {
		slog.Warn("检测到旧实例，尝试唤醒")
		if hM != 0 {
			windows.CloseHandle(hM)
		}

		eName, _ := windows.UTF16PtrFromString(ShowUIEvent)
		hEvent, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, eName)
		if err == nil && hEvent != 0 {
			windows.SetEvent(hEvent)
			windows.CloseHandle(hEvent)
			slog.Info("成功触发唤醒事件")
		} else {
			slog.Error("触发唤醒事件失败", "err", err)
		}
		return
	}
	if hM != 0 {
		defer windows.CloseHandle(hM)
	}

	eName, _ := windows.UTF16PtrFromString(ShowUIEvent)
	hShowUIEvent, _ := windows.CreateEvent(nil, 0, 0, eName)
	if hShowUIEvent != 0 {
		defer windows.CloseHandle(hShowUIEvent)
	}

	isAutostart := false
	for _, arg := range os.Args {
		if arg == "---autostart" || arg == "--autostart" {
			isAutostart = true
			break
		}
	}
	
	slog.Debug("启动参数检查", "autostart", isAutostart, "isAdmin", isAdmin())

	if !isAdmin() && !isAutostart {
		slog.Warn("权限不足，准备请求提权")
		sys.RunAsAdmin(exePath, baseDir)
		slog.Info("提权指令已发送，当前进程退出")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runtimeState := state.NewRuntimeState()
	application := app.NewApplication(cfgMgr, runtimeState)
	trayMenu := ui.NewTrayMenu(ctx, cancel, application.UICommandCh, application.UIStateCh)

	slog.Debug("初始化系统托盘")
	trayMenu.Init()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(sigCh)

		select {
		case sig := <-sigCh:
			slog.Info("接收到退出信号，准备清理", "信号", sig)
			trayMenu.Stop()
		case <-ctx.Done():
			return
		}
	}()

	if hShowUIEvent != 0 {
		go func() {
			slog.Debug("唤醒监听已就绪")
			for {
				s, _ := windows.WaitForSingleObject(hShowUIEvent, windows.INFINITE)
				if s != windows.WAIT_OBJECT_0 {
					return
				}

				if ctx.Err() != nil {
					return
				}

				slog.Info("收到外部唤醒信号")
				select {
				case application.UICommandCh <- ui.UICommand{Action: "OpenWebUI"}:
					time.Sleep(200 * time.Millisecond)
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	slog.Debug("启动后台主循环")
	go application.Bootstrap(ctx)

	slog.Debug("运行托盘界面事件循环")
	trayMenu.Run()

	slog.Debug("托盘循环结束")
	cancel()

	if hShowUIEvent != 0 {
		windows.SetEvent(hShowUIEvent)
	}

	runtimeState.ForceExitPhase()
	application.SafeShutdown(cancel)
	slog.Info("程序安全退出")
}

func isAdmin() bool {
	var token windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil {
		slog.Error("查询 Token 失败", "err", err)
		return false
	}
	defer token.Close()
	return token.IsElevated()
}
