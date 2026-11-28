package ui

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"

	"mihomo-tray/internal/domain"
)

type Engine struct {
	ctx           context.Context
	cancel        context.CancelFunc
	baseDir       string
	commandCh     chan<- domain.UICommand
	stateNotifyCh <-chan struct{}
	getState      func() domain.UIState
	ReadyCh       chan struct{}

	uiThreadID         uint32
	app                *walk.Application
	mw                 *walk.MainWindow
	preferScreenAnchor atomic.Bool

	Tray      *Tray
	Dashboard *Dashboard
	modalMgr  *ModalManager

	onShutdown  func()
	disposeOnce sync.Once
}

func NewEngine(ctx context.Context, cancel context.CancelFunc, baseDir string, cmdCh chan<- domain.UICommand, notifyCh <-chan struct{}, getState func() domain.UIState) *Engine {
	e := &Engine{
		ctx:           ctx,
		cancel:        cancel,
		baseDir:       baseDir,
		commandCh:     cmdCh,
		stateNotifyCh: notifyCh,
		getState:      getState,
		ReadyCh:       make(chan struct{}),
	}
	e.preferScreenAnchor.Store(true)
	return e
}

func (e *Engine) SetOnShutdown(fn func()) {
	e.onShutdown = fn
}

func (e *Engine) Run() error {
	app, err := walk.InitApp()
	if err != nil {
		return fmt.Errorf("初始化界面框架失败: %w", err)
	}
	e.uiThreadID = win.GetCurrentThreadId()
	e.app = app

	err = MainWindow{
		AssignTo: &e.mw,
		Title:    domain.HostTitle,
		Icon:     1,
		Visible:  false,
	}.Create()
	if err != nil {
		return fmt.Errorf("创建后台宿主窗口失败: %w", err)
	}

	e.hookShutdown()

	e.modalMgr = NewModalManager()
	e.Tray = NewTray(e)
	e.Dashboard = NewDashboard(e)

	go e.listenState()
	close(e.ReadyCh)

	app.Run()
	e.disposeAll()

	return nil
}

func (e *Engine) hookShutdown() {
	if e.mw == nil || e.mw.Handle() == 0 {
		return
	}

	hwnd := e.mw.Handle()
	var prevWndProc uintptr
	var shutdownOnce sync.Once

	newWndProc := syscall.NewCallback(func(h win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
		switch msg {
		case wmQueryEndSession:
			return 1

		case wmEndSession:
			if wParam != 0 {
				shutdownOnce.Do(func() {
					slog.Info("收到系统关机信号，执行清理")
					if e.onShutdown != nil {
						e.onShutdown()
					}
				})
				win.PostQuitMessage(0)
			}
			return 0
		}

		if prevWndProc != 0 {
			return win.CallWindowProc(prevWndProc, h, msg, wParam, lParam)
		}
		return win.DefWindowProc(h, msg, wParam, lParam)
	})

	prevWndProc = win.SetWindowLongPtr(hwnd, gwlpWndProc, newWndProc)
}

func (e *Engine) RunOnUI(fn func()) {
	if e.app == nil {
		return
	}

	if e.uiThreadID != 0 && win.GetCurrentThreadId() == e.uiThreadID {
		fn()
		return
	}

	if e.ctx.Err() != nil {
		return
	}

	done := make(chan struct{})
	e.app.Synchronize(func() {
		defer close(done)
		if e.ctx.Err() != nil {
			return
		}
		fn()
	})

	select {
	case <-done:
	case <-e.ctx.Done():
	}
}

func (e *Engine) PostUI(fn func()) {
	if e.app == nil || e.ctx.Err() != nil {
		return
	}

	if e.uiThreadID != 0 && win.GetCurrentThreadId() == e.uiThreadID {
		fn()
		return
	}

	e.app.Synchronize(func() {
		if e.ctx.Err() != nil {
			return
		}
		fn()
	})
}

func (e *Engine) listenState() {
	shutdownUI := func() {
		if e.app != nil {
			e.app.Synchronize(func() {
				e.disposeAll()
				e.app.Exit(0)
			})
		}
	}

	for {
		select {
		case <-e.ctx.Done():
			shutdownUI()
			return

		case <-e.stateNotifyCh:
			if e.ctx.Err() != nil {
				shutdownUI()
				return
			}

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
					e.Dashboard.UpdateState(state)
				}
			})
		}
	}
}

func (e *Engine) disposeAll() {
	e.disposeOnce.Do(func() {
		if e.Dashboard != nil {
			e.Dashboard.Dispose()
			e.Dashboard = nil
		}
		if e.Tray != nil {
			e.Tray.Dispose()
		}
		if e.mw != nil {
			e.mw.Dispose()
			e.mw = nil
		}
	})
}

func (e *Engine) SendTrayCommand(action, payload string) {
	e.preferScreenAnchor.Store(true)
	e.sendCommand(domain.UICommand{Action: action, Payload: payload})
}

func (e *Engine) SendDashboardCommand(action, payload string) {
	e.preferScreenAnchor.Store(false)
	e.sendCommand(domain.UICommand{Action: action, Payload: payload})
}

func (e *Engine) sendCommand(cmd domain.UICommand) {
	select {
	case e.commandCh <- cmd:
	default:
		slog.Warn("指令队列已满，忽略操作", "action", cmd.Action)
	}
}

func (e *Engine) Exit() {
	if e.cancel != nil {
		e.cancel()
	}
}

func (e *Engine) ShowProfileManager(state domain.UIState) {
	e.preferScreenAnchor.Store(false)
	e.RunOnUI(func() {
		if e.Dashboard != nil {
			e.Dashboard.UpdateState(state)
			e.Dashboard.Show()
		}
	})
}

func (e *Engine) RunModalEditor(key string, cfg EditorConfig) EditorResult {
	if e.ctx.Err() != nil {
		return EditorResult{Accepted: false}
	}
	if e.modalMgr == nil {
		slog.Warn("UI 未就绪，无法打开编辑器", "key", key)
		return EditorResult{Accepted: false}
	}

	var res EditorResult
	e.RunOnUI(func() {
		if !e.modalMgr.TryAcquire(key) {
			return
		}
		defer e.modalMgr.Release(key)

		originalOnReady := cfg.OnReady
		cfg.OnReady = func(dlg *walk.Dialog) {
			e.modalMgr.RegisterHWND(key, uintptr(dlg.Handle()))
			if originalOnReady != nil {
				originalOnReady(dlg)
			}
		}

		res = RunEditor(e.activeOwner(), cfg)
	})

	return res
}

func (e *Engine) ShowError(title, message string) {
	e.showModalAsync("error", title, message, walk.MsgBoxOK|walk.MsgBoxIconError)
}

func (e *Engine) showModalAsync(prefix, title, message string, style walk.MsgBoxStyle) {
	if e.ctx.Err() != nil {
		return
	}
	if e.app == nil || e.modalMgr == nil {
		slog.Warn("UI 未就绪，跳过提示框", "title", title)
		return
	}

	go e.RunOnUI(func() {
		key := prefix + "|" + title + "|" + message
		if prefix == "error" {
			key = prefix + "|" + message
		}
		if !e.modalMgr.TryAcquire(key) {
			return
		}
		defer e.modalMgr.Release(key)

		ShowNativeMsgBox(e.activeOwner(), title, message, style, func(hwnd win.HWND) {
			e.modalMgr.RegisterHWND(key, uintptr(hwnd))
		})
	})
}

func (e *Engine) ShowConfirm(title, message string) bool {
	if e.modalMgr == nil || e.ctx.Err() != nil {
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
			e.modalMgr.RegisterHWND(key, uintptr(hwnd))
		})
		result = (res == win.IDOK)
	})
	return result
}

func (e *Engine) OpenYAMLFileDialog() (string, bool) {
	if e.modalMgr == nil || e.ctx.Err() != nil {
		return "", false
	}

	const modalKey = "open_yaml_file_dialog"
	var path string
	var ok bool

	e.RunOnUI(func() {
		if !e.modalMgr.TryAcquire(modalKey) {
			return
		}
		defer e.modalMgr.Release(modalKey)

		path, ok = RunOpenYAMLFileDialog(e.activeOwner())
	})

	return path, ok
}

func (e *Engine) ShowNotification(title, message string) {
	if e.ctx.Err() != nil || e.Tray == nil {
		return
	}
	e.Tray.ShowNotification(title, message)
}

func (e *Engine) activeOwner() walk.Form {
	if e.preferScreenAnchor.Load() {
		return nil
	}
	if e.Dashboard != nil && e.Dashboard.window != nil {
		hwnd := e.Dashboard.window.Handle()
		if hwnd != 0 && win.IsWindowVisible(hwnd) && !win.IsIconic(hwnd) {
			return e.Dashboard.window
		}
	}
	return nil
}
