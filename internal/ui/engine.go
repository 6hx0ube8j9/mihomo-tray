package ui

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

type UIEngine struct {
	ctx       context.Context
	cancel    context.CancelFunc
	commandCh chan<- UICommand
	stateCh   <-chan UIState

	app       *walk.Application
	mw        *walk.MainWindow
	ni        *walk.NotifyIcon
}

func NewUIEngine(ctx context.Context, cancel context.CancelFunc, cmdCh chan<- UICommand, stateCh <-chan UIState) *UIEngine {
	return &UIEngine{
		ctx:       ctx,
		cancel:    cancel,
		commandCh: cmdCh,
		stateCh:   stateCh,
	}
}

func (e *UIEngine) Run() error {
	app, err := walk.InitApp()
	if err != nil {
		return fmt.Errorf("walk 引擎初始化失败: %w", err)
	}
	e.app = app

	err = MainWindow{
		AssignTo: &e.mw,
		Title:    "Mihomo Tray Host",
		Visible:  false,
	}.Create()
	
	if err != nil {
		return fmt.Errorf("创建母体窗口失败: %w", err)
	}

	ni, err := walk.NewNotifyIcon(e.mw)
	if err != nil {
		return fmt.Errorf("创建托盘图标失败: %w", err)
	}
	e.ni = ni
	e.ni.SetVisible(true)
	e.ni.SetToolTip("Mihomo Tray")

	go e.listenState()

	slog.Debug("全局 UI 引擎消息循环已启动")
	app.Run()

	e.ni.Dispose()
	e.mw.Dispose()
	return nil
}

func (e *UIEngine) listenState() {
	for {
		select {
		case <-e.ctx.Done():
			e.app.Synchronize(func() {
				e.mw.Close()
			})
			return
		case state, ok := <-e.stateCh:
			if !ok {
				return
			}
			e.app.Synchronize(func() {
				e.updateTrayState(state)
			})
		}
	}
}

func (e *UIEngine) updateTrayState(state UIState) {
}

func (e *UIEngine) sendCommand(action, payload string) {
	slog.Debug("UI 发送指令", "action", action, "payload", payload)
	select {
	case e.commandCh <- UICommand{Action: action, Payload: payload}:
	default:
		slog.Warn("UI 发送指令阻塞 (大总管过载)，已静默丢弃", "action", action)
	}
}
