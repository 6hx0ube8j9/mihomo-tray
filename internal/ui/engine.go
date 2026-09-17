package ui

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tailscale/walk"
	. "github.com/tailscale/walk/declarative"
)

//go:embed icons/*.ico
var iconFs embed.FS

type UIEngine struct {
	ctx       context.Context
	cancel    context.CancelFunc
	commandCh chan<- UICommand
	stateCh   <-chan UIState

	app *walk.Application
	mw  *walk.MainWindow
	ni  *walk.NotifyIcon

	icons   []*walk.Icon
	iconDir string

	panelWindow *walk.MainWindow
	tableView   *walk.TableView
	panelModel  *ProfileModel

	lastClick time.Time
	clickMu   sync.Mutex
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

	e.ni, err = walk.NewNotifyIcon(e.mw)
	if err != nil {
		return fmt.Errorf("创建托盘图标失败: %w", err)
	}
	e.ni.SetVisible(true)
	e.ni.SetToolTip("Mihomo Tray")

	e.ni.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			e.clickMu.Lock()
			if time.Since(e.lastClick) > 300*time.Millisecond {
				e.lastClick = time.Now()
				e.clickMu.Unlock()
				e.sendCommand("OpenWebUI", "")
			} else {
				e.clickMu.Unlock()
			}
		}
	})

	e.loadEmbeddedIcons()

	go e.listenState()

	slog.Debug("全局 UI 引擎消息循环已启动")
	app.Run()

	e.ni.Dispose()
	e.mw.Dispose()
	if e.iconDir != "" {
		os.RemoveAll(e.iconDir)
	}
	return nil
}

func (e *UIEngine) loadEmbeddedIcons() {
	e.icons = make([]*walk.Icon, 5)
	iconFiles := []string{"stop.ico", "error.ico", "tun.ico", "proxy.ico", "default.ico"}
	
	tmpDir, err := os.MkdirTemp("", "mihomo-tray-icons-*")
	if err != nil {
		slog.Error("创建临时图标目录失败", "err", err)
		return
	}
	e.iconDir = tmpDir

	for id, name := range iconFiles {
		if b, err := iconFs.ReadFile("icons/" + name); err == nil {
			tmpPath := filepath.Join(tmpDir, name)
			_ = os.WriteFile(tmpPath, b, 0644)
			if ico, err := walk.NewIconFromFile(tmpPath); err == nil {
				e.icons[id] = ico
			}
		} else {
			slog.Error("加载嵌入式图标失败", "icon", name, "err", err)
		}
	}
	
	if e.icons[0] != nil {
		e.ni.SetIcon(e.icons[0])
	}
}

func (e *UIEngine) listenState() {
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
				if state.IconState >= 0 && state.IconState < len(e.icons) && e.icons[state.IconState] != nil {
					e.ni.SetIcon(e.icons[state.IconState])
				}
				e.updateTrayState(state)
			})
		}
	}
}

func (e *UIEngine) sendCommand(action, payload string) {
	slog.Debug("UI 发送指令", "action", action, "payload", payload)
	select {
	case e.commandCh <- UICommand{Action: action, Payload: payload}:
	default:
		slog.Warn("UI 发送指令阻塞，已静默丢弃", "action", action)
	}
}
