package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"mihomo-tray/internal/app"
	"mihomo-tray/internal/config"
	"mihomo-tray/internal/ui"
)

const (
	AppMutex    = "Mihomo_Tray_Mutex"
	ShowUIEvent = "Mihomo_Tray_Mutex_ShowUI"
)


type syncFileWriter struct {
	file *os.File
}

func (w *syncFileWriter) Write(p []byte) (n int, err error) {
	n, err = w.file.Write(p)
	if err == nil {
		_ = w.file.Sync()
	}
	return n, err
}

func initEarlyLogger(baseDir string) *os.File {
	logPath := filepath.Join(baseDir, "app.log")
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return nil
	}

	log.SetOutput(&syncFileWriter{file: file})
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	return file
}

func main() {
	runtime.LockOSThread()

	exePath, err := os.Executable()
	if err != nil {
		return
	}
	baseDir := filepath.Dir(exePath)
	_ = os.Chdir(baseDir)

	logFile := initEarlyLogger(baseDir)
	if logFile != nil {
		defer logFile.Close()
	}

	log.Println("[INFO] ==================== Mihomo Tray 进程启动 ====================")
	log.Printf("[INFO] 进程 PID: %d, 可执行文件路径: %s", os.Getpid(), exePath)

	mName, _ := windows.UTF16PtrFromString(AppMutex)
	hM, err := windows.CreateMutex(nil, false, mName)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || err == windows.ERROR_ALREADY_EXISTS {
		log.Println("[WARN] 检测到应用已有实例在运行，尝试唤醒已有实例并退出当前进程...")
		if hM != 0 {
			windows.CloseHandle(hM)
		}

		eName, _ := windows.UTF16PtrFromString(ShowUIEvent)
		hEvent, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, eName)
		if err == nil && hEvent != 0 {
			windows.SetEvent(hEvent)
			windows.CloseHandle(hEvent)
			log.Println("[INFO] 成功触发已存在实例的唤醒事件 (ShowUIEvent)")
		} else {
			log.Printf("[ERROR] 无法打开或触发唤醒事件: %v", err)
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
	log.Printf("[INFO] 参数检查: autostart=%v, isAdmin=%v", isAutostart, isAdmin())

	if !isAdmin() && !isAutostart {
		log.Println("[WARN] 当前进程无管理员权限，准备请求 UAC 提权重新启动...")
		runAsAdmin(exePath, baseDir)
		log.Println("[INFO] UAC 提权指令已发送，当前普通权限进程退出")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfgMgr := config.NewManager(baseDir, exePath)
	application := app.NewApplication(cfgMgr)
	trayMenu := ui.NewTrayMenu(ctx, cancel, application.UICommandCh, application.UIStateCh)

	log.Println("[INFO] 初始化系统托盘 UI...")
	trayMenu.Init()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(sigCh)

		select {
		case sig := <-sigCh:
			log.Printf("[WARN] 接收到系统终止信号 (%v)，正在通知托盘退出...", sig)
			trayMenu.Stop()
		case <-ctx.Done():
			return
		}
	}()

	if hShowUIEvent != 0 {
		go func() {
			log.Println("[INFO] 唤醒事件监听协程已就绪")
			for {
				s, _ := windows.WaitForSingleObject(hShowUIEvent, windows.INFINITE)
				if s != windows.WAIT_OBJECT_0 {
					return
				}

				if ctx.Err() != nil {
					return
				}

				log.Println("[INFO] 收到外部唤醒信号，正在触发 OpenWebUI 指令")
				select {
				case application.UICommandCh <- ui.UICommand{Action: "OpenWebUI"}:
					time.Sleep(200 * time.Millisecond)
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	log.Println("[INFO] 启动应用程序后台主循环 (Bootstrap)...")
	go application.Bootstrap(ctx)

	log.Println("[INFO] 运行托盘界面事件循环...")
	trayMenu.Run()

	log.Println("[INFO] 托盘循环结束，开始执行退出清理程序...")
	cancel()

	if hShowUIEvent != 0 {
		windows.SetEvent(hShowUIEvent)
	}

	cfgMgr.State.ForceExitPhase()
	application.SafeShutdown(cancel)
	log.Println("[INFO] ==================== Mihomo Tray 进程安全退出 ====================")
}

func isAdmin() bool {
	var token windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil {
		log.Printf("[ERROR] 查询进程 Token 失败: %v", err)
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

func runAsAdmin(exe, dir string) {
	verb, _ := windows.UTF16PtrFromString("runas")
	exePtr, _ := windows.UTF16PtrFromString(exe)
	cwdPtr, _ := windows.UTF16PtrFromString(dir)
	err := windows.ShellExecute(0, verb, exePtr, nil, cwdPtr, windows.SW_SHOWNORMAL)
	if err != nil {
		log.Printf("[ERROR] 触发 UAC 提权失败 (ShellExecute): %v", err)
	}
}
