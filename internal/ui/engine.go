package ui

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative" // 核心修复：引入声明式宏包
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

	// --- UI 独立组件 ---
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

	// 核心修复：直接使用 MainWindow，不再带前缀
	err = MainWindow{
		AssignTo: &e.mw,
		Title:    "Mihomo Tray Host",
		Visible:  false,
	}.Create()

	if err != nil {
		return fmt.Errorf("主控窗口创建失败: %w", err)
	}

	// 初始化组件
	e.Tray = NewTray(e)
	e.Dashboard = NewDashboard(e)

	// 启动数据流监听
	go e.listenState()

	slog.Debug("UI 引擎消息循环已启动")
	app.Run() // 阻塞运行

	// 优雅释放资源
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
				// 精准分发，互不干扰
				e.Tray.UpdateState(state)
				e.Dashboard.Refresh(state)
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
