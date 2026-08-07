package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"mihomo-tray/internal/app"
	"mihomo-tray/internal/fsm"
	"mihomo-tray/internal/ui"
)

const (
	AppMutex    = "Mihomo_Tray_Mutex"
	ShowUIEvent = "Mihomo_Tray_Mutex_ShowUI"
)

func main() {
	runtime.LockOSThread()

	exePath, err := os.Executable()
	if err != nil {
		return
	}
	baseDir := filepath.Dir(exePath)
	_ = os.Chdir(baseDir)

	mName, _ := windows.UTF16PtrFromString(AppMutex)
	hM, err := windows.CreateMutex(nil, false, mName)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) || err == windows.ERROR_ALREADY_EXISTS {
		if hM != 0 {
			windows.CloseHandle(hM)
		}

		eName, _ := windows.UTF16PtrFromString(ShowUIEvent)
		hEvent, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, eName)
		if err == nil && hEvent != 0 {
			windows.SetEvent(hEvent)
			windows.CloseHandle(hEvent)
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
	if !isAdmin() && !isAutostart {
		runAsAdmin(exePath, baseDir)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfgMgr := fsm.NewManager(baseDir, exePath)
	application := app.NewApplication(cfgMgr)
	trayMenu := ui.NewTrayMenu(ctx, cancel, application.UICommandCh, application.UIStateCh)

	trayMenu.Init()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(sigCh)

		select {
		case <-sigCh:
			trayMenu.Stop()
		case <-ctx.Done():
			return
		}
	}()

	if hShowUIEvent != 0 {
		go func() {
			for {
				s, _ := windows.WaitForSingleObject(hShowUIEvent, windows.INFINITE)
				if s != windows.WAIT_OBJECT_0 {
					return
				}

				if ctx.Err() != nil {
					return
				}
				
				select {
				case application.UICommandCh <- ui.UICommand{Action: "OpenWebUI"}:
					time.Sleep(200 * time.Millisecond)
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go application.Bootstrap(ctx)
	trayMenu.Run()

	cancel()

	if hShowUIEvent != 0 {
		windows.SetEvent(hShowUIEvent)
	}

	cfgMgr.State.ForceExitPhase()
	application.SafeShutdown(cancel)
}

func isAdmin() bool {
	var token windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

func runAsAdmin(exe, dir string) {
	verb, _ := windows.UTF16PtrFromString("runas")
	exePtr, _ := windows.UTF16PtrFromString(exe)
	cwdPtr, _ := windows.UTF16PtrFromString(dir)
	_ = windows.ShellExecute(0, verb, exePtr, nil, cwdPtr, windows.SW_SHOWNORMAL)
}
