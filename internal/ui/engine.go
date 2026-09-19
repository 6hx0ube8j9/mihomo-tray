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

	"mihomo-tray/internal/domain"
)

//go:embed icons/*.ico
var iconFs embed.FS

var globalUIEngine *UIEngine

type UIEngine struct {
	ctx       context.Context
	cancel    context.CancelFunc
	commandCh chan<- domain.UICommand
	stateCh   <-chan domain.UIState

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

func NewUIEngine(ctx context.Context, cancel context.CancelFunc, cmdCh chan<- domain.UICommand, stateCh <-chan domain.UIState) *UIEngine {
	e := &UIEngine{
		ctx:       ctx,
		cancel:    cancel,
		commandCh: cmdCh,
		stateCh:   stateCh,
	}
	globalUIEngine = e
	return e
}

func (e *UIEngine) Run() error {
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

	e.ni, err = walk.NewNotifyIcon()
	if err != nil {
		return fmt.Errorf("托盘图标创建失败: %w", err)
	}
	e.ni.SetVisible(true)
	e.ni.SetToolTip("Mihomo Tray")
	e.ni.MouseUp().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			e.sendCommand("OpenWebUI", "")
		}
	})

	e.loadEmbeddedIcons()

	go e.listenState()

	slog.Debug("UI 引擎消息循环已启动")
	app.Run()

	e.ni.Dispose()
	e.mw.Dispose()

	for _, icon := range e.icons {
		if icon != nil {
			icon.Dispose()
		}
	}

	if e.iconDir != "" {
		if err := os.RemoveAll(e.iconDir); err != nil {
			slog.Error("清理临时图标失败", "err", err)
		}
	}
	return nil
}

func (e *UIEngine) loadEmbeddedIcons() {
	e.icons = make([]*walk.Icon, 5)
	iconFiles := []string{"stop.ico", "error.ico", "tun.ico", "proxy.ico", "default.ico"}

	tmpDir, err := os.MkdirTemp("", "mihomo-tray-icons-*")
	if err != nil {
		slog.Error("创建图标缓存目录失败", "err", err)
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
			slog.Error("加载嵌入图标失败", "icon", name, "err", err)
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
				e.RefreshPanelData(state.ProfileItems)
			})
		}
	}
}

func (e *UIEngine) sendCommand(action, payload string) {
	slog.Debug("发送 UI 指令", "action", action, "payload", payload)
	select {
	case e.commandCh <- domain.UICommand{Action: action, Payload: payload}:
	default:
		slog.Warn("UI 指令管道阻塞，已丢弃", "action", action)
	}
}
