package ui

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"mihomo-tray/internal/wintray"
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
	IDOpenBaseDir
	IDToggleAutoStart
	IDRunAsAdmin
	IDReloadConfig
	IDRestartKernel
	IDOpenConfigFile
	IDExitApp
	IDAdminStatus
)

const (
	IDProfileSwitchBase uint32 = 2000
	IDProfileRemoveBase uint32 = 2010
	IDProfileAddLocal   uint32 = 2020
)

type UICommand struct {
	Action  string
	Payload string
}

type ProfileItem struct {
	Name     string
	Path     string
	IsActive bool
}

type UIState struct {
	IconState     int
	IsTun         bool
	IsProxy       bool
	Mode          string
	AutoStart     bool
	IsAdmin       bool
	RunAsAdmin    bool
	ProfileItems  []ProfileItem
	CanAddProfile bool
}

type TrayMenu struct {
	ctx       context.Context
	cancel    context.CancelFunc
	commandCh chan<- UICommand
	stateCh   <-chan UIState

	trayHost  *wintray.TrayHost
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
	tm.trayHost = wintray.NewTrayHost(
		"MihomoTray",
		tm.onLeftClick,
		tm.onRightClick,
		tm.onMenuItemClick,
	)

	iconFiles := []string{"stop.ico", "error.ico", "tun.ico", "proxy.ico", "default.ico"}
	for id, name := range iconFiles {
		if b, err := iconFs.ReadFile("icons/" + name); err == nil {
			tm.trayHost.CacheIcon(id, b)
		} else {
			slog.Error("加载托盘图标失败", "icon", name, "err", err)
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
		slog.Debug("正在退出托盘消息循环")
		tm.trayHost.Stop()
	}
}

func (tm *TrayMenu) ListenUIState() {
	for {
		select {
		case <-tm.ctx.Done():
			return
		case state, ok := <-tm.stateCh:
			if !ok {
				return
			}
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
	slog.Debug("发送 UI 命令", "action", action, "payload", payload)
	
	select {
	case tm.commandCh <- UICommand{Action: action, Payload: payload}:
	case <-time.After(500 * time.Millisecond):
		slog.Warn("发送 UI 命令超时", "action", action)
	case <-tm.ctx.Done():
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
	tm.sendCommand("ForceSyncAPI", "")
	tm.stateMu.RLock()
	st := tm.currState
	tm.stateMu.RUnlock()

	modeNames := map[string]string{"rule": "规则", "direct": "直连", "global": "全局"}
	currModeName := modeNames[st.Mode]
	if currModeName == "" {
		currModeName = "未知"
	}

	adminText := "运行权限：普通用户"
	if st.IsAdmin {
		adminText = "运行权限：管理员"
	}

	var switchSubItems []wintray.MenuItem
	var removeSubItems []wintray.MenuItem
	activeProfileName := "config.yaml"

	for i, item := range st.ProfileItems {
		if item.IsActive {
			activeProfileName = item.Name
		}
		
		switchSubItems = append(switchSubItems, wintray.MenuItem{
			ID:      IDProfileSwitchBase + uint32(i),
			Text:    item.Name,
			Checked: item.IsActive,
		})
		
		removeSubItems = append(removeSubItems, wintray.MenuItem{
			ID:       IDProfileRemoveBase + uint32(i),
			Text:     item.Name,
			Disabled: i == 0 || item.IsActive,
		})
	}

	items := []wintray.MenuItem{
		{ID: IDOpenWebUI, Text: "进入 Web 面板"},
		{IsSeparator: true},
		{
			Text: fmt.Sprintf("切换配置 (%d/5)", len(st.ProfileItems)),
			SubMenuItems: switchSubItems,
		},
		{
			ID: IDProfileAddLocal,
			Text: fmt.Sprintf("添加本地配置 (%d/5)", len(st.ProfileItems)),
			Disabled: !st.CanAddProfile,
		},
		{
			Text: "🗑️ 移除配置",
			SubMenuItems: removeSubItems,
		},
		{IsSeparator: true},
		{ID: IDToggleProxy, Text: "系统代理", Checked: st.IsProxy},
		{ID: IDToggleTun, Text: "虚拟网卡 (TUN)", Checked: st.IsTun},
		{IsSeparator: true},
		{
			Text: fmt.Sprintf("当前模式: %s", currModeName),
			SubMenuItems: []wintray.MenuItem{
				{ID: IDModeRule, Text: "规则", Checked: st.Mode == "rule"},
				{ID: IDModeDirect, Text: "直连", Checked: st.Mode == "direct"},
				{ID: IDModeGlobal, Text: "全局", Checked: st.Mode == "global"},
			},
		},
		{IsSeparator: true},
		{ID: IDOpenBaseDir, Text: "打开程序目录"},
		{IsSeparator: true},
		{
			Text: adminText,
			SubMenuItems: []wintray.MenuItem{
				{ID: IDToggleAutoStart, Text: "开机自启（管理员）", Checked: st.AutoStart},
				{ID: IDRunAsAdmin, Text: "始终以管理员身份运行", Checked: st.RunAsAdmin || st.AutoStart, Disabled: st.AutoStart},
			},
		},
		{IsSeparator: true},
		{
			Text: "更多",
			SubMenuItems: []wintray.MenuItem{
				{ID: IDReloadConfig, Text: "重载当前配置"},
				{ID: IDRestartKernel, Text: "重启核心进程"},
				{ID: IDOpenConfigFile, Text: fmt.Sprintf("📝 编辑 (%s)", activeProfileName)},
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

	if id >= IDProfileSwitchBase && id < IDProfileSwitchBase+5 {
		idx := int(id - IDProfileSwitchBase)
		if idx < len(st.ProfileItems) {
			tm.sendCommand("SwitchProfile", st.ProfileItems[idx].Path)
		}
		return
	}

	if id >= IDProfileRemoveBase && id < IDProfileRemoveBase+5 {
		idx := int(id - IDProfileRemoveBase)
		if idx < len(st.ProfileItems) {
			tm.sendCommand("RemoveProfile", st.ProfileItems[idx].Path)
		}
		return
	}

	switch id {
	case IDProfileAddLocal:
		if st.CanAddProfile {
			tm.sendCommand("RequestAddLocalProfile", "")
		}
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
	case IDOpenBaseDir:
		tm.sendCommand("OpenBaseDir", "")
	case IDToggleAutoStart:
		tm.sendCommand("ToggleAutoStart", strconv.FormatBool(!st.AutoStart))
	case IDRunAsAdmin:
		tm.sendCommand("ToggleRunAsAdmin", strconv.FormatBool(!st.RunAsAdmin))
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
