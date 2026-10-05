package ui

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"syscall"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
	"mihomo-tray/internal/domain"
)

type Engine struct {
	ctx           context.Context
	cancel        context.CancelFunc
	commandCh     chan<- domain.UICommand
	stateNotifyCh <-chan struct{}
	getState      func() domain.UIState
	ReadyCh       chan struct{}

	app *walk.Application
	mw  *walk.MainWindow

	Tray      *Tray
	Dashboard *Dashboard

	activeAlertMu sync.Mutex
	activeAlerts  map[string]*walk.Dialog
}

func NewEngine(ctx context.Context, cancel context.CancelFunc, cmdCh chan<- domain.UICommand, notifyCh <-chan struct{}, getState func() domain.UIState) *Engine {
	return &Engine{
		ctx:           ctx,
		cancel:        cancel,
		commandCh:     cmdCh,
		stateNotifyCh: notifyCh,
		getState:      getState,
		ReadyCh:       make(chan struct{}),
		activeAlerts:  make(map[string]*walk.Dialog),
	}
}

func (e *Engine) Run() error {
	app, err := walk.InitApp()
	if err != nil {
		return fmt.Errorf("Walk 引擎初始化失败: %w", err)
	}
	e.app = app

	err = MainWindow{
		AssignTo: &e.mw,
		Title:    "Mihomo Tray Host",
		Visible:  false,
	}.Create()

	if err != nil {
		return fmt.Errorf("主控窗口创建失败: %w", err)
	}

	e.Tray = NewTray(e)
	e.Dashboard = NewDashboard(e)

	go e.listenState()

	slog.Debug("UI 引擎内存与句柄已分配完毕，释放启动屏障")
	close(e.ReadyCh)

	app.Run()

	e.Tray.Dispose()
	e.Dashboard.Dispose()
	e.mw.Dispose()

	return nil
}

func (e *Engine) listenState() {
	for {
		select {
		case <-e.ctx.Done():
			e.app.Synchronize(func() {
				if e.mw != nil {
					e.mw.Close()
				}
			})
			return
		case <-e.stateNotifyCh:
			if e.getState == nil || e.app == nil {
				continue
			}

			state := e.getState()

			e.app.Synchronize(func() {
				if e.Tray != nil {
					e.Tray.UpdateState(state)
				}
				if e.Dashboard != nil {
					e.Dashboard.BackgroundUpdate(state)
				}
			})
		}
	}
}

func (e *Engine) SendCommand(action, payload string) {
	slog.Debug("UI 指令", "action", action, "payload", payload)
	select {
	case e.commandCh <- domain.UICommand{Action: action, Payload: payload}:
	default:
		slog.Warn("UI 指令管道阻塞，已丢弃", "action", action)
	}
}

func (e *Engine) Exit() {
	if e.cancel != nil {
		e.cancel()
	}
}

func (e *Engine) ShowProfileManager(state domain.UIState) {
	if e.Dashboard != nil && e.app != nil {
		e.app.Synchronize(func() {
			e.Dashboard.ForceInjectData(state)
			e.Dashboard.Show()
		})
	}
}

func getDialogKey(title, message string) string {
	return title + "|" + message
}

func (e *Engine) tryAcquireAlertFocus(key string) bool {
	e.activeAlertMu.Lock()
	defer e.activeAlertMu.Unlock()
	
	if dlg, exists := e.activeAlerts[key]; exists && dlg != nil {
		hwnd := dlg.Handle()
		if hwnd != 0 && win.IsWindowVisible(hwnd) && !win.IsIconic(hwnd) {
			win.SetForegroundWindow(hwnd)
			dlg.SetFocus()
			return true
		}
	}
	return false
}

func (e *Engine) executeGuardedDialog(title, message string, icon *walk.Icon, beep uint32, isConfirm bool) bool {
	key := getDialogKey(title, message)

	if e.tryAcquireAlertFocus(key) {
		return false
	}

	res := runBaseDialog(e.activeOwner(), title, message, icon, beep, isConfirm, func(dlg *walk.Dialog) {
		e.activeAlertMu.Lock()
		e.activeAlerts[key] = dlg
		e.activeAlertMu.Unlock()
	})

	e.activeAlertMu.Lock()
	delete(e.activeAlerts, key)
	e.activeAlertMu.Unlock()

	return res
}

func (e *Engine) showAsyncDialog(title, message string, walkIcon *walk.Icon, beep uint32, fallbackIcon uint32) {
	if e.app == nil || e.mw == nil {
		slog.Error("严重错误 (UI尚未就绪/已销毁)", "title", title, "message", message)
		go func() {
			titlePtr, _ := syscall.UTF16PtrFromString(title)
			msgPtr, _ := syscall.UTF16PtrFromString(message)
			win.MessageBox(0, msgPtr, titlePtr, fallbackIcon|win.MB_SYSTEMMODAL)
		}()
		return
	}

	go e.app.Synchronize(func() {
		e.executeGuardedDialog(title, message, walkIcon, beep, false)
	})
}

func (e *Engine) ShowError(title, message string) {
	e.showAsyncDialog(title, message, walk.IconWarning(), win.MB_ICONWARNING, win.MB_ICONERROR)
}

func (e *Engine) ShowInfo(title, message string) {
	e.showAsyncDialog(title, message, walk.IconInformation(), win.MB_ICONINFORMATION, win.MB_ICONINFORMATION)
}

func (e *Engine) ShowConfirm(title, message string) bool {
	if e.app == nil || e.mw == nil {
		return false
	}
	
	resultCh := make(chan bool, 1)

	e.app.Synchronize(func() {
		resultCh <- e.executeGuardedDialog(title, message, walk.IconQuestion(), win.MB_ICONQUESTION, true)
	})

	select {
	case res := <-resultCh:
		return res
	case <-e.ctx.Done():
		return false
	}
}

func (e *Engine) ShowNotification(title, message string) {
	if e.Tray != nil && e.app != nil {
		e.app.Synchronize(func() {
			e.Tray.ShowNotification(title, message)
		})
	}
}

func (e *Engine) OpenYAMLFileDialog() (string, bool) {
	if e.app == nil || e.mw == nil {
		return "", false
	}

	type fileResult struct {
		Path string
		OK   bool
	}
	resultCh := make(chan fileResult, 1)

	e.app.Synchronize(func() {
		path, ok := RunOpenYAMLFileDialog(e.activeOwner())
		resultCh <- fileResult{Path: path, OK: ok}
	})

	select {
	case res := <-resultCh:
		return res.Path, res.OK
	case <-e.ctx.Done(): 
		return "", false
	}
}

func (e *Engine) activeOwner() walk.Form {
	if e.Dashboard != nil && e.Dashboard.window != nil {
		hwnd := e.Dashboard.window.Handle()
		if win.IsWindowVisible(hwnd) && !win.IsIconic(hwnd) {
			return e.Dashboard.window
		}
	}
	return nil
}
