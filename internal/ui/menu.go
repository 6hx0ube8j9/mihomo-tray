package ui

import (
	"context"
	"embed"
	"fmt"
	"strconv"
	"sync"
	"time"

	"mihomo-tray/internal/sys"
)

//go:embed icons/*.ico
var iconFs embed.FS

const (
	IDOpenWebUI uint32 = 1000 + iota
	IDToggleProxy
	IDToggleTun
	IDModeRule
	IDModeDirect
	IDModeGlobal
	IDThemeAuto
	IDThemeLight
	IDThemeDark
	IDOpenBaseDir
	IDToggleAutoStart
	IDReloadConfig
	IDRestartKernel
	IDOpenConfigFile
	IDExitApp
)

type UICommand struct {
	Action  string
	Payload string
}

type UIState struct {
	IconState int
	IsTun     bool
	IsProxy   bool
	Mode      string
	AutoStart bool
	ThemeMode int
}

type TrayMenu struct {
	ctx       context.Context
	cancel    context.CancelFunc
	commandCh chan<- UICommand
	stateCh   <-chan UIState

	trayHost  *sys.TrayHost
	currState UIState
	stateMu   sync.RWMutex

	lastClick time.Time
	clickMu   sync.Mutex
}

func NewTrayMenu(ctx context.Context, cancel context.CancelFunc, cmdCh chan<- UICommand, stCh <-chan UIState) *TrayMenu {
	return &TrayMenu{
		ctx:       ctx,
		cancel:    cancel,
		commandCh: cmdCh,
		stateCh:   stCh,
	}
}

func (tm *TrayMenu) Init() {
	tm.trayHost = sys.NewTrayHost(
		"MihomoTray",
		tm.onLeftClick,
		tm.onRightClick,
		tm.onMenuItemClick,
	)

	iconFiles := []string{"stop.ico", "error.ico", "tun.ico", "proxy.ico", "default.ico"}
	for id, name := range iconFiles {
		if b, err := iconFs.ReadFile("icons/" + name); err == nil {
			tm.trayHost.CacheIcon(id, b)
		}
	}

	tm.trayHost.SetIcon(0)
}

func (tm *TrayMenu) Run() {
	go tm.ListenUIState()
	tm.trayHost.RunMessageLoop()
}

func (tm *TrayMenu) Stop() {
	if tm.trayHost != nil {
		tm.trayHost.Stop()
	}
}

func (tm *TrayMenu) ListenUIState() {
	for {
		select {
		case <-tm.ctx.Done():
			return
		case state := <-tm.stateCh:
			tm.stateMu.Lock()
			tm.currState = state
			tm.stateMu.Unlock()

			if state.IconState >= 0 && state.IconState < 5 {
				tm.trayHost.SetIcon(state.IconState)
			}
		}
	}
}

func (tm *TrayMenu) sendCommand(action, payload string) {
	select {
	case tm.commandCh <- UICommand{Action: action, Payload: payload}:
	default:
	}
}

func (tm *TrayMenu) onLeftClick() {
	tm.clickMu.Lock()
	if time.Since(tm.lastClick) < 300*time.Millisecond {
		tm.clickMu.Unlock()
		return
	}
	tm.lastClick = time.Now()
	tm.clickMu.Unlock()

	tm.sendCommand("OpenWebUI", "")
}

func (tm *TrayMenu) onRightClick() {
	tm.stateMu.RLock()
	st := tm.currState
	tm.stateMu.RUnlock()

	modeNames := map[string]string{
		"rule":   "规则",
		"direct": "直连",
		"global": "全局",
	}
	currModeName := modeNames[st.Mode]
	if currModeName == "" {
		currModeName = "未知"
	}

	themeMode := st.ThemeMode
	if themeMode == 0 {
		themeMode = sys.AppModeAuto
	}

	themeNames := map[int]string{
		sys.AppModeAuto:       "跟随系统",
		sys.AppModeForceLight: "浅色",
		sys.AppModeForceDark:  "深色",
	}
	currThemeName := themeNames[themeMode]
	if currThemeName == "" {
		currThemeName = "跟随系统"
	}

	items := []sys.MenuItem{
		{ID: IDOpenWebUI, Text: "进入 Web 面板"},
		{IsSeparator: true},
		{
			ID:      IDToggleProxy,
			Text:    "系统代理",
			Checked: st.IsProxy,
		},
		{
			ID:      IDToggleTun,
			Text:    "虚拟网卡 (TUN)",
			Checked: st.IsTun,
		},
		{IsSeparator: true},
		{
			Text: fmt.Sprintf("当前模式: %s", currModeName),
			SubMenuItems: []sys.MenuItem{
				{ID: IDModeRule, Text: "规则", Checked: st.Mode == "rule"},
				{ID: IDModeDirect, Text: "直连", Checked: st.Mode == "direct"},
				{ID: IDModeGlobal, Text: "全局", Checked: st.Mode == "global"},
			},
		},
		{IsSeparator: true},
		{ID: IDOpenBaseDir, Text: "打开程序目录"},
		{
			Text: "更多",
			SubMenuItems: []sys.MenuItem{
				{ID: IDToggleAutoStart, Text: "开机启动", Checked: st.AutoStart},
				{ID: IDReloadConfig, Text: "重载配置文件"},
				{ID: IDRestartKernel, Text: "重启核心"},
				{ID: IDOpenConfigFile, Text: "编辑 config.yaml"},
			},
		},
		{
			Text: fmt.Sprintf("主题模式: %s", currThemeName),
			SubMenuItems: []sys.MenuItem{
				{ID: IDThemeAuto, Text: "跟随系统", Checked: themeMode == sys.AppModeAuto},
				{ID: IDThemeLight, Text: "浅色", Checked: themeMode == sys.AppModeForceLight},
				{ID: IDThemeDark, Text: "深色", Checked: themeMode == sys.AppModeForceDark},
			},
		},
		{IsSeparator: true},
		{ID: IDExitApp, Text: "退出程序"},
	}

	tm.trayHost.ShowContextMenu(items)
}

func (tm *TrayMenu) onMenuItemClick(id uint32) {
	tm.stateMu.RLock()
	st := tm.currState
	tm.stateMu.RUnlock()

	switch id {
	case IDOpenWebUI:
		tm.sendCommand("OpenWebUI", "")
	case IDToggleProxy:
		tm.sendCommand("ToggleProxy", strconv.FormatBool(!st.IsProxy))
	case IDToggleTun:
		tm.sendCommand("ToggleTun", strconv.FormatBool(!st.IsTun))
	case IDModeRule:
		tm.sendCommand("SwitchMode", "rule")
	case IDModeDirect:
		tm.sendCommand("SwitchMode", "direct")
	case IDModeGlobal:
		tm.sendCommand("SwitchMode", "global")
	case IDThemeAuto:
		sys.SetMenuTheme(sys.AppModeAuto)
		tm.sendCommand("SetThemeMode", strconv.Itoa(sys.AppModeAuto))
	case IDThemeLight:
		sys.SetMenuTheme(sys.AppModeForceLight)
		tm.sendCommand("SetThemeMode", strconv.Itoa(sys.AppModeForceLight))
	case IDThemeDark:
		sys.SetMenuTheme(sys.AppModeForceDark)
		tm.sendCommand("SetThemeMode", strconv.Itoa(sys.AppModeForceDark))
	case IDOpenBaseDir:
		tm.sendCommand("OpenBaseDir", "")
	case IDToggleAutoStart:
		tm.sendCommand("ToggleAutoStart", strconv.FormatBool(!st.AutoStart))
	case IDReloadConfig:
		tm.sendCommand("ReloadConfig", "")
	case IDRestartKernel:
		tm.sendCommand("RestartKernel", "")
	case IDOpenConfigFile:
		tm.sendCommand("OpenConfigFile", "")
	case IDExitApp:
		tm.sendCommand("ExitApp", "")
		tm.trayHost.Stop()
	}
}
