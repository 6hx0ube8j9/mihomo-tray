package ui

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"github.com/tailscale/win"
	"golang.org/x/sys/windows"

	"mihomo-tray/internal/domain"
)

const (
	wmQueryEndSession = 0x0011
	wmEndSession      = 0x0016
	gwlpWndProc       = -4
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

	onShutdown func()
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

func (e *Engine) SetOnShutdown(fn func()) {
	e.onShutdown = fn
}

func (e *Engine) Run() error {
	app, err := walk.InitApp()
	if err != nil {
		return fmt.Errorf("初始化界面框架失败: %w", err)
	}
	e.app = app

	err = MainWindow{
		AssignTo: &e.mw,
		Title:    domain.HostTitle,
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

	slog.Debug("UI 引擎已就绪")
	close(e.ReadyCh)

	app.Run()

	slog.Debug("退出 UI 消息循环，清理资源")
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

	newWndProc := windows.NewCallback(func(h win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
		switch msg {
		case wmQueryEndSession:
			return 1

		case wmEndSession:
			if wParam != 0 {
				shutdownOnce.Do(func() {
					slog.Info("捕获系统关机广播，执行清理")
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
		slog.Warn("UI 调度被取消，引擎已退出")
	}
}

func (e *Engine) listenState() {
	for {
		select {
		case <-e.ctx.Done():
			if e.app != nil {
				e.app.Synchronize(func() {
					e.disposeAll()
					e.app.Exit(0)
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
					e.Dashboard.UpdateState(state)
				}
			})
		}
	}
}

func (e *Engine) disposeAll() {
	if e.Dashboard != nil {
		e.Dashboard.Dispose()
		e.Dashboard = nil
	}
	if e.Tray != nil {
		e.Tray.Dispose()
		e.Tray = nil
	}
	if e.mw != nil {
		e.mw.Dispose()
		e.mw = nil
	}
}

func (e *Engine) SendCommand(action, payload string) {
	slog.Debug("发出界面指令", "action", action, "payload", payload)
	select {
	case e.commandCh <- domain.UICommand{Action: action, Payload: payload}:
	default:
		slog.Warn("指令通道已满，丢弃操作", "action", action)
	}
}

func (e *Engine) Exit() {
	slog.Debug("请求退出应用")
	if e.cancel != nil {
		e.cancel()
	}
}

func (e *Engine) ShowProfileManager(state domain.UIState) {
	if e.Dashboard != nil {
		e.RunOnUI(func() {
			e.Dashboard.UpdateState(state)
			e.Dashboard.Show()
		})
	}
}

func (e *Engine) RunModalEditor(key string, cfg EditorConfig) EditorResult {
	if e.modalMgr == nil {
		slog.Warn("ModalManager 未初始化，无法打开模态编辑器", "key", key)
		return EditorResult{Accepted: false}
	}

	var res EditorResult

	e.RunOnUI(func() {
		if !e.modalMgr.TryAcquire(key) {
			slog.Debug("模态窗口防抖拦截", "key", key)
			return
		}
		defer e.modalMgr.Release(key)

		originalOnReady := cfg.OnReady
		cfg.OnReady = func(dlg *walk.Dialog) {
			e.modalMgr.RegisterHWND(key, dlg.Handle())
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

func (e *Engine) ShowInfo(title, message string) {
	e.showModalAsync("info", title, message, walk.MsgBoxOK|walk.MsgBoxIconInformation)
}

func (e *Engine) showModalAsync(prefix, title, message string, style walk.MsgBoxStyle) {
	if e.app == nil || e.mw == nil || e.modalMgr == nil {
		slog.Warn("UI 未就绪，无法显示提示框", "title", title, "message", message)
		return
	}

	go e.RunOnUI(func() {
		key := prefix + "|" + title + "|" + message
		if !e.modalMgr.TryAcquire(key) {
			return
		}
		defer e.modalMgr.Release(key)

		ShowNativeMsgBox(e.activeOwner(), title, message, style, func(hwnd win.HWND) {
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
