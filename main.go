package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/energye/systray"
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
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	baseDir := filepath.Dir(exePath)
	_ = os.Chdir(baseDir)

	mName, _ := windows.UTF16PtrFromString(AppMutex)
	hM, err := windows.CreateMutex(nil, false, mName)
	if err != nil || windows.GetLastError() == windows.ERROR_ALREADY_EXISTS {
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
	defer windows.CloseHandle(hM)

	eName, _ := windows.UTF16PtrFromString(ShowUIEvent)
	hShowUIEvent, _ := windows.CreateEvent(nil, 0, 0, eName)
	if hShowUIEvent != 0 {
		defer windows.CloseHandle(hShowUIEvent)
	}

	isAutostart := false
	for _, arg := range os.Args {
		if arg == "---autostart" {
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

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(sigCh)

		select {
		case <-sigCh:
			cancel()
			cfgMgr.State.ForceExitPhase()
			systray.Quit()
		case <-ctx.Done():
			return
		}
	}()

	if hShowUIEvent != 0 {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					s, err := windows.WaitForSingleObject(hShowUIEvent, 500)
					if err != nil {
						time.Sleep(500 * time.Millisecond)
						continue
					}
					if s == windows.WAIT_OBJECT_0 {
						select {
						case application.UICommandCh <- ui.UICommand{Action: "OpenWebUI"}:
							time.Sleep(200 * time.Millisecond) 
						default:
						}
					}
				}
			}
		}()
	}

	systray.Run(
		func() {
			trayMenu.Setup() 
			application.Bootstrap(ctx)
		},
		func() {
			application.SafeShutdown(cancel)
		},
	)
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
	verb, _ := syscall.UTF16PtrFromString("runas")
	exePtr, _ := syscall.UTF16PtrFromString(exe)
	cwdPtr, _ := syscall.UTF16PtrFromString(dir)
	_ = windows.ShellExecute(0, verb, exePtr, nil, cwdPtr, windows.SW_SHOWNORMAL)
}
