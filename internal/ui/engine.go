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

const (
	MainWindowTitle = "Mihomo Tray Host"
	TrayToolTip     = "Mihomo Tray"
	TempDirPattern  = "mihomo-tray-icons-*"
)

var trayIconAssets = []string{
	domain.IconStop:    "stop.ico",
	domain.IconError:   "error.ico",
	domain.IconTun:     "tun.ico",
	domain.IconProxy:   "proxy.ico",
	domain.IconDefault: "default.ico",
}

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

	// ---- 兼容 dialog.go / editor.go 的旧指针 ----
	dashboardWindow *walk.MainWindow

	// ---- 独立的仪表盘视图句柄 ----
	dashboardView *MainWindowView

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
		Title:    MainWindowTitle,
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
	e.ni.SetToolTip(TrayToolTip)
	e.ni.MouseUp().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			e.sendCommand(domain.ActionOpenWebUI, "")
		}
	})

	e.loadEmbeddedIcons()

	// 初始化仪表盘视图
	e.initDashboard()

	go e.listenState()

	slog.Debug("UI 引擎消息循环已启动")
	app.Run()

	e.ni.Dispose()
	if e.dashboardView != nil && e.dashboardView.Window != nil {
		e.dashboardView.Window.Dispose()
	}
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

func (e *UIEngine) initDashboard() {
	view, err := NewMainWindowView(func(action, payload string) {
		e.sendCommand(action, payload)
	})
	if err != nil {
		slog.Error("初始化配置管理窗口失败", "err", err)
		return
	}
	e.dashboardView = view
	// 挂载到旧的 dashboardWindow 变量，满足 dialog.go 和 editor.go
	e.dashboardWindow = view.Window
}

func (e *UIEngine) ShowProfileManager(items []domain.UIProfileItem) {
	if e.app == nil {
		return
	}
	e.app.Synchronize(func() {
		if e.dashboardView == nil {
			e.initDashboard()
		}
		if e.dashboardView != nil {
			e.dashboardView.RefreshData(items)
			e.dashboardView.Show()
		}
	})
}

func (e *UIEngine) RefreshPanelData(items []domain.UIProfileItem) {
	if e.dashboardView != nil {
		e.dashboardView.RefreshData(items)
	}
}

func (e *UIEngine) loadEmbeddedIcons() {
	e.icons = make([]*walk.Icon, len(trayIconAssets))

	tmpDir, err := os.MkdirTemp("", TempDirPattern)
	if err != nil {
		slog.Error("创建图标缓存目录失败", "err", err)
		return
	}
	e.iconDir = tmpDir

	for id, name := range trayIconAssets {
		if name == "" {
			continue
		}
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

	if len(e.icons) > 0 && e.icons[domain.IconStop] != nil {
		e.ni.SetIcon(e.icons[domain.IconStop])
	}
}

func (e *UIEngine) listenState() {
	var lastIconId = -1

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
				if state.IconState != lastIconId && state.IconState >= 0 && state.IconState < len(e.icons) && e.icons[state.IconState] != nil {
					e.ni.SetIcon(e.icons[state.IconState])
					lastIconId = state.IconState
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
