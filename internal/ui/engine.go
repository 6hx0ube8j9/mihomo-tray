package ui

import (
	"context"
	"fmt"
	"log/slog"

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
	modalMgr  *ModalManager
}

func NewEngine(ctx context.Context, cancel context.CancelFunc, cmdCh chan<- domain.UICommand, notifyCh <-chan struct{}, getState func() domain.UIState) *Engine {
	return &Engine{
		ctx:           ctx,
		cancel:        cancel,
		commandCh:     cmdCh,
		stateNotifyCh: notifyCh,
		getState:      getState,
		ReadyCh:       make(chan struct{}),
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

	e.modalMgr = NewModalManager()

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

func (e *Engine) RunOnUI(fn func()) {
	if e.app == nil {
		return
	}
	if e.mw != nil && e.mw.Handle() != 0 && win.GetCurrentThreadId() == win.GetWindowThreadProcessId(e.mw.Handle(), nil) {
		fn()
		return
	}

	done := make(chan struct{})
	e.app.Synchronize(func() {
		defer close(done)
		fn()
	})

	select {
	case <-done:
	case <-e.ctx.Done():
	}
}

func (e *Engine) listenState() {
	for {
		select {
		case <-e.ctx.Done():
			if e.app != nil {
				e.app.Synchronize(func() {
					if e.mw != nil {
						e.mw.Close()
					}
				})
			}
			return

		case <-e.stateNotifyCh:
			drained := false
			for !drained {
				select {
				case <-e.stateNotifyCh:
				default:
					drained = true
				}
			}

			if e.getState == nil || e.app == nil {
				continue
			}

			state := e.getState()
			e.RunOnUI(func() {
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
	slog.Debug("UI 指令发出", "action", action, "payload", payload)
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
	if e.Dashboard != nil {
		e.RunOnUI(func() {
			e.Dashboard.ForceInjectData(state)
			e.Dashboard.Show()
		})
	}
}

func (e *Engine) ShowError(title, message string) {
	if e.app == nil || e.mw == nil || e.modalMgr == nil {
		slog.Error("UI未就绪，错误已丢弃", "title", title, "message", message)
		return
	}

	go e.RunOnUI(func() {
		key := "error|" + title + "|" + message
		if !e.modalMgr.TryAcquire(key) {
			return
		}
		defer e.modalMgr.Release(key)

		ShowNativeMsgBox(e.activeOwner(), title, message, walk.MsgBoxOK|walk.MsgBoxIconError, func(hwnd win.HWND) {
			e.modalMgr.RegisterHWND(key, hwnd)
		})
	})
}

func (e *Engine) ShowInfo(title, message string) {
	if e.app == nil || e.mw == nil || e.modalMgr == nil {
		return
	}

	go e.RunOnUI(func() {
		key := "info|" + title + "|" + message
		if !e.modalMgr.TryAcquire(key) {
			return
		}
		defer e.modalMgr.Release(key)

		ShowNativeMsgBox(e.activeOwner(), title, message, walk.MsgBoxOK|walk.MsgBoxIconInformation, func(hwnd win.HWND) {
			e.modalMgr.RegisterHWND(key, hwnd)
		})
	})
}

func (e *Engine) ShowConfirm(title, message string) bool {
	if e.modalMgr == nil {
		return false
	}
	var result bool
	e.RunOnUI(func() {
		key := "confirm|" + title + "|" + message
		if !e.modalMgr.TryAcquire(key) {
			return
		}
		defer e.modalMgr.Release(key)

		res := ShowNativeMsgBox(e.activeOwner(), title, message, walk.MsgBoxOKCancel|walk.MsgBoxIconQuestion, func(hwnd win.HWND) {
			e.modalMgr.RegisterHWND(key, hwnd)
		})
		result = (res == win.IDOK)
	})
	return result
}

func (e *Engine) OpenYAMLFileDialog() (string, bool) {
	var path string
	var ok bool
	e.RunOnUI(func() {
		path, ok = RunOpenYAMLFileDialog(e.activeOwner())
	})
	return path, ok
}

func (e *Engine) ShowNotification(title, message string) {
	if e.Tray != nil {
		e.RunOnUI(func() {
			e.Tray.ShowNotification(title, message)
		})
	}
}

func (e *Engine) activeOwner() walk.Form {
	if e.Dashboard != nil && e.Dashboard.window != nil {
		hwnd := e.Dashboard.window.Handle()
		if hwnd != 0 && win.IsWindowVisible(hwnd) && !win.IsIconic(hwnd) {
			return e.Dashboard.window
		}
	}
	return nil
}
