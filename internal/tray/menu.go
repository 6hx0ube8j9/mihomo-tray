package tray

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
	IDExitApp
	IDAdminStatus
	
	IDOpenProfileManager
)

const (
	IDProfileSwitchBase uint32 = 2000 // 2000-2009
)

type UICommand struct {
	Action  string
	Payload string
}

type ProfileItem struct {
	Name       string
	Path       string
	IsActive   bool
	IsRemote   bool
	Interval   int
	LastUpdate string
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

	var switchSubMenus []wintray.MenuItem
	for i, item := range st.ProfileItems {
		suffix := " (本地)"
		if item.IsRemote {
			suffix = " (订阅)"
		}
		switchSubMenus = append(switchSubMenus, wintray.MenuItem{
			ID:      IDProfileSwitchBase + uint32(i),
			Text:    item.Name + suffix,
			Checked: item.IsActive,
		})
	}

	if len(switchSubMenus) == 0 {
		switchSubMenus = append(switchSubMenus, wintray.MenuItem{
			Text: "暂无配置", Disabled: true,
		})
	}

	items := []wintray.MenuItem{
		{ID: IDOpenWebUI, Text: "进入 Web 面板"},
		{IsSeparator: true},
		{ID: IDToggleProxy, Text: "系统代理", Checked: st.IsProxy},
		{ID: IDToggleTun, Text: "TUN 模式", Checked: st.IsTun},
		{
			Text: fmt.Sprintf("路由模式: %s", currModeName),
			SubMenuItems: []wintray.MenuItem{
				{ID: IDModeRule, Text: "规则", Checked: st.Mode == "rule"},
				{ID: IDModeDirect, Text: "直连", Checked: st.Mode == "direct"},
				{ID: IDModeGlobal, Text: "全局", Checked: st.Mode == "global"},
			},
		},
		{IsSeparator: true},
		{ID: IDOpenProfileManager, Text: "配置面板"},
		{
			Text: "切换配置文件",
			SubMenuItems: switchSubMenus,
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
		{
			Text: "更多",
			SubMenuItems: []wintray.MenuItem{
				{ID: IDReloadConfig, Text: "重载当前配置"},
				{ID: IDRestartKernel, Text: "重启内核"},
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

	if id >= IDProfileSwitchBase && id < IDProfileSwitchBase+10 {
		idx := int(id - IDProfileSwitchBase)
		if idx < len(st.ProfileItems) {
			tm.sendCommand("SwitchProfile", st.ProfileItems[idx].Path)
		}
		return
	}

	switch id {
	case IDOpenProfileManager:
		tm.sendCommand("OpenProfileManager", "")
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
	case IDExitApp:
		tm.sendCommand("ExitApp", "")
		tm.trayHost.Stop()
	}
}
