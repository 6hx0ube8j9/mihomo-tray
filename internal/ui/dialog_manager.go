package ui

import (
	"sync"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
)

type DialogManager struct {
	app      *walk.Application
	mainHwnd win.HWND

	activeMu sync.Mutex
	active   map[string]*walk.Dialog
}

func NewDialogManager(app *walk.Application, mw *walk.MainWindow) *DialogManager {
	var hwnd win.HWND
	if mw != nil {
		hwnd = mw.Handle()
	}
	return &DialogManager{
		app:      app,
		mainHwnd: hwnd,
		active:   make(map[string]*walk.Dialog),
	}
}

func (dm *DialogManager) RunOnUI(fn func()) {
	if dm.app == nil {
		return
	}
	if dm.mainHwnd != 0 && win.GetCurrentThreadId() == win.GetWindowThreadProcessId(dm.mainHwnd, nil) {
		fn()
		return
	}

	done := make(chan struct{})
	dm.app.Synchronize(func() {
		defer close(done)
		fn()
	})
	<-done
}

func (dm *DialogManager) TryAcquire(key string) bool {
	dm.activeMu.Lock()
	defer dm.activeMu.Unlock()

	if dlg, exists := dm.active[key]; exists {
		if dlg != nil {
			hwnd := dlg.Handle()
			if hwnd != 0 && win.IsWindowVisible(hwnd) {
				if win.IsIconic(hwnd) {
					win.ShowWindow(hwnd, win.SW_RESTORE)
				}
				win.SetForegroundWindow(hwnd)
				dlg.SetFocus()
			}
		}
		return false
	}

	dm.active[key] = nil
	return true
}

func (dm *DialogManager) Register(key string, dlg *walk.Dialog) {
	dm.activeMu.Lock()
	defer dm.activeMu.Unlock()
	if _, exists := dm.active[key]; exists {
		dm.active[key] = dlg
	}
}

func (dm *DialogManager) Release(key string) {
	dm.activeMu.Lock()
	defer dm.activeMu.Unlock()
	delete(dm.active, key)
}
