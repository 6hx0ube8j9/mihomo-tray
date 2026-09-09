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

	sa := getPermissiveSecAttr()
	mName, _ := windows.UTF16PtrFromString(AppMutex)
	hM, err := windows.CreateMutex(sa, false, mName)
	isAlreadyExist := errors.Is(err, windows.ERROR_ALREADY_EXISTS) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		err == windows.ERROR_ALREADY_EXISTS ||
		err == windows.ERROR_ACCESS_DENIED

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

	isAutostart := false
	for _, arg := range os.Args[1:] {
		if strings.EqualFold(strings.TrimLeft(arg, "-"), "autostart") {
			isAutostart = true
			break
		}
	}
	
    admin := isAdmin()
	slog.Debug("启动参数与权限检查", "autostart", isAutostart, "admin", admin)

	if !isAdmin() && !isAutostart {
		if hM != 0 {
			_ = windows.CloseHandle(hM)
			hM = 0
		}
		
		if sys.IsTaskPathValid(exePath) {
			slog.Debug("检测到自启计划任务，尝试提权启动")
			schtasksPath := filepath.Join(os.Getenv("SystemRoot"), "System32", "schtasks.exe")
			cmd := exec.Command(schtasksPath, "/Run", "/TN", sys.TaskName)
			cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}

			if out, err := cmd.CombinedOutput(); err == nil {
				slog.Info("已通过计划任务启动新实例，当前进程退出")
				return
			} else {
				slog.Warn("计划任务启动失败，转为 UAC 提权", "err", err, "output", strings.TrimSpace(string(out)))
			}
		} else {
			slog.Debug("计划任务未配置或路径无效，跳过提权启动")
		}

		slog.Info("权限不足，请求 UAC 提权")
		sys.RunAsAdmin(exePath, baseDir)
		return
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
