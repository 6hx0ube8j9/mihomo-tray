package ui

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
	"mihomo-tray/internal/domain"
)

var GlobalEngine *Engine

type Engine struct {
	ctx       context.Context
	cancel    context.CancelFunc
	commandCh chan<- domain.UICommand
	stateCh   <-chan domain.UIState

	app *walk.Application
	mw  *walk.MainWindow

	Tray      *Tray
	Dashboard *Dashboard
}

func NewEngine(ctx context.Context, cancel context.CancelFunc, cmdCh chan<- domain.UICommand, stateCh <-chan domain.UIState) *Engine {
	e := &Engine{
		ctx:       ctx,
		cancel:    cancel,
		commandCh: cmdCh,
		stateCh:   stateCh,
	}
	GlobalEngine = e
	return e
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

	slog.Debug("UI 引擎消息循环已启动")
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
			e.app.Synchronize(func() { e.mw.Close() })
			return
		case state, ok := <-e.stateCh:
			if !ok {
				return
			}
			e.app.Synchronize(func() {
				e.Tray.UpdateState(state)
				e.Dashboard.BackgroundUpdate(state) 
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


func (e *Engine) ShowProfileManager(state domain.UIState) {
	if e.Dashboard != nil {
		e.app.Synchronize(func() {
			e.Dashboard.ForceInjectData(state) 
			e.Dashboard.Show()
		})
	}
}

func (e *Engine) ShowError(title, message string) {
	ShowErrorMessage(e.mw, title, message)
}

func (e *Engine) ShowInfo(title, message string) {
	ShowInfoMessage(e.mw, title, message)
}

func (e *Engine) ShowNotification(title, message string) {
	if e.Tray != nil {
		e.Tray.ShowNotification(title, message)
	}
}

func (e *Engine) ShowConfirm(title, message string) bool {
	return ShowConfirmMessage(e.mw, title, message)
}

func (e *Engine) OpenYAMLFileDialog() (string, bool) {
	return OpenYAMLFileDialog() 
}

func (e *Engine) Exit() {
	if e.cancel != nil {
		e.cancel()
	}
}
