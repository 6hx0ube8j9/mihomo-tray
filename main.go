package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"mihomo-tray/internal/app"
	"mihomo-tray/internal/config"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/ui"
)

const (
	AppMutex    = "Local\\Mihomo_Tray_Mutex"
	ShowUIEvent = "Local\\Mihomo_Tray_Mutex_ShowUI"
	MaxLogSize  = 1024 * 1024
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
	logDir := filepath.Join(baseDir, "logs")
	return &rollingLogWriter{
		logPath: filepath.Join(logDir, "mihomo-tray.log"),
		bakPath: filepath.Join(logDir, "mihomo-tray.log.bak"),
	}
}

func (w *rollingLogWriter) open() {
	_ = os.MkdirAll(filepath.Dir(w.logPath), 0755)

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
	_ = os.Remove(w.bakPath)
	renameErr := os.Rename(w.logPath, w.bakPath)
	var file *os.File
	var err error
	if renameErr == nil {
		file, err = os.OpenFile(w.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	} else {
		file, err = os.OpenFile(w.logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	}
	if err == nil {
		w.file = file
		w.currSize = 0
	}
}

func (w *rollingLogWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		w.open()
		if w.file == nil {
			return len(p), nil
		}
	}
	if w.currSize+int64(len(p)) > MaxLogSize {
		w.rotate()
		if w.file == nil {
			return len(p), nil
		}
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

func getPermissiveSecAttr() *windows.SecurityAttributes {
	sd, err := windows.SecurityDescriptorFromString("D:(A;;GA;;;WD)S:(ML;;NW;;;LW)")
	if err != nil {
		slog.Error("创建安全描述符失败", "err", err)
		return nil
	}
	var sa windows.SecurityAttributes
	sa.Length = uint32(unsafe.Sizeof(sa))
	sa.SecurityDescriptor = sd
	return &sa
}

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
	enableTunArg := false
	for _, arg := range os.Args[1:] {
		argClean := strings.ToLower(strings.TrimLeft(arg, "-"))
		if argClean == "autostart" {
			isAutostart = true
		} else if strings.Contains(argClean, "restarting") {
			isRestarting = true
		} else if strings.Contains(argClean, "enable-tun") {
			enableTunArg = true
		}
	}

	sa := getPermissiveSecAttr()
	mName, _ := windows.UTF16PtrFromString(AppMutex)

	var hM windows.Handle
	var isAlreadyExist bool
	for i := 0; i < 10; i++ {
		hM, err = windows.CreateMutex(sa, false, mName)
		isAlreadyExist = errors.Is(err, windows.ERROR_ALREADY_EXISTS) ||
			errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
			err == windows.ERROR_ALREADY_EXISTS ||
			err == windows.ERROR_ACCESS_DENIED

		if !isAlreadyExist || !isRestarting {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}

	if isAlreadyExist {
		if hM != 0 {
			_ = windows.CloseHandle(hM)
		}
		eName, _ := windows.UTF16PtrFromString(ShowUIEvent)
		hEvent, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, eName)
		if err == nil && hEvent != 0 {
			_ = windows.SetEvent(hEvent)
			_ = windows.CloseHandle(hEvent)
		}
		return
	}

	logWriter := initEarlyLogger(baseDir)
	if logWriter != nil {
		defer logWriter.Close()
	}

	cfgMgr := config.NewManager(baseDir, exePath)
	cfgMgr.LoadAndInitMemory()
	syncLogLevel(cfgMgr)

	slog.Info("程序启动", "pid", os.Getpid(), "dir", baseDir)

	if enableTunArg {
		cfgMgr.Set("tun", "true")
	}

	admin := sys.IsAdmin()
	isAutostartConfig := cfgMgr.Get("autostart") == "true"
	isRunAsAdminConfig := cfgMgr.Get("run_as_admin") == "true"

	if !admin && !isAutostart {
		if isAutostartConfig || isRunAsAdminConfig {
			slog.Info("配置要求特权，请求 UAC 提权")
			err := sys.RunAsAdmin(exePath, baseDir, "--restarting")
			
			if sys.IsUserCancelled(err) {
				slog.Info("用户在启动时取消了 UAC，优雅退出")
				if hM != 0 {
					windows.CloseHandle(hM)
				}
				os.Exit(0)
			} else if err == nil {
				slog.Info("提权请求成功，当前普通进程退出")
				if hM != 0 {
					windows.CloseHandle(hM)
				}
				os.Exit(0)
			} else {
				slog.Error("提权启动失败", "err", err)
			}
		}
	}

	if hM != 0 {
		defer windows.CloseHandle(hM)
	}

	eName, _ := windows.UTF16PtrFromString(ShowUIEvent)
	hShowUIEvent, _ := windows.CreateEvent(sa, 0, 0, eName)
	if hShowUIEvent != 0 {
		defer windows.CloseHandle(hShowUIEvent)
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
			slog.Info("收到系统退出信号", "signal", sig)
			trayMenu.Stop()
		case <-ctx.Done():
			return
		}
	}()

	if hShowUIEvent != 0 {
		go func() {
			slog.Debug("唤醒事件监听已就绪")
			for {
				s, _ := windows.WaitForSingleObject(hShowUIEvent, windows.INFINITE)
				if s != windows.WAIT_OBJECT_0 || ctx.Err() != nil {
					return
				}
				slog.Info("收到外部进程唤醒信号")
				select {
				case application.UICommandCh <- ui.UICommand{Action: "OpenWebUI"}:
					time.Sleep(200 * time.Millisecond)
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	slog.Debug("启动后台核心服务")
	go application.Bootstrap(ctx)

	slog.Debug("进入托盘界面事件循环")
	trayMenu.Run()

	slog.Debug("托盘循环退出，开始释放资源")
	cancel()
	if hShowUIEvent != 0 {
		_ = windows.SetEvent(hShowUIEvent)
	}

	runtimeState.ForceExitPhase()
	application.SafeShutdown(cancel)
	slog.Info("程序已安全退出")
}

func isAdmin() bool {
	var token windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil {
		slog.Error("获取进程 Token 失败", "err", err)
		return false
	}
	defer token.Close()
	return token.IsElevated()
}
