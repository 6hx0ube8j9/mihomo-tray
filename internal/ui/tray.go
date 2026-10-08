package ui

import (
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale/walk"
	"mihomo-tray/internal/domain"
)

//go:embed icons/*.ico
var iconFs embed.FS

var trayIconAssets = []string{
	domain.IconStop:    "stop.ico",
	domain.IconError:   "error.ico",
	domain.IconTun:     "tun.ico",
	domain.IconProxy:   "proxy.ico",
	domain.IconDefault: "default.ico",
}

type Tray struct {
	engine     *Engine
	ni         *walk.NotifyIcon
	icons      []*walk.Icon
	iconDir    string
	lastIconId int

	isBuilt                bool
	latestState            domain.UIState
	lastProfileFingerprint string

	actProxy          *walk.Action
	actTun            *walk.Action
	actModeRule       *walk.Action
	actModeDirect     *walk.Action
	actModeGlobal     *walk.Action
	actModeMenu       *walk.Action
	menuSwitchProfile *walk.Menu
	actAutoStart      *walk.Action
	actRunAdmin       *walk.Action
	actAdminMenu      *walk.Action
	actSysBrowser     *walk.Action
	actRemoteWebUI    *walk.Action
	actAllowLan       *walk.Action
}

func NewTray(e *Engine) *Tray {
	ni, err := walk.NewNotifyIcon()
	if err != nil {
		slog.Error("托盘图标创建失败", "err", err)
		return nil
	}
	ni.SetVisible(true)
	ni.SetToolTip("Mihomo Tray")

	t := &Tray{
		engine:     e,
		ni:         ni,
		lastIconId: -1,
	}

	t.loadEmbeddedIcons()

	ni.MouseUp().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			t.engine.SendCommand(domain.ActionOpenWebUI, "")
		}
	})

	return t
}

func (t *Tray) loadEmbeddedIcons() {
	t.icons = make([]*walk.Icon, len(trayIconAssets))
	tmpDir, err := os.MkdirTemp("", "mihomo-tray-icons-*")
	if err != nil {
		slog.Error("创建图标缓存目录失败", "err", err)
		return
	}
	t.iconDir = tmpDir

	for id, name := range trayIconAssets {
		if name == "" {
			continue
		}
		if b, err := iconFs.ReadFile("icons/" + name); err == nil {
			tmpPath := filepath.Join(tmpDir, name)
			_ = os.WriteFile(tmpPath, b, 0644)
			if ico, err := walk.NewIconFromFile(tmpPath); err == nil {
				t.icons[id] = ico
			}
		}
	}
	if len(t.icons) > 0 && t.icons[domain.IconStop] != nil {
		t.ni.SetIcon(t.icons[domain.IconStop])
	}
}

func (t *Tray) UpdateState(state domain.UIState) {
	if t.ni == nil {
		return
	}

	stateChanged := t.latestState.Mode != state.Mode ||
		t.latestState.IsAdmin != state.IsAdmin ||
		t.latestState.AutoStart != state.AutoStart

	t.latestState = state

    iconIdx := int(state.IconState)
	if iconIdx != t.lastIconId && iconIdx >= 0 && iconIdx < len(t.icons) && t.icons[iconIdx] != nil {
		t.ni.SetIcon(t.icons[iconIdx])
		t.lastIconId = iconIdx
	}

	if !t.isBuilt {
		t.buildMenuSkeleton()
		t.isBuilt = true
		stateChanged = true
	}

	t.safelySetChecked(t.actProxy, state.IsProxy)
	t.safelySetChecked(t.actTun, state.IsTun)
	t.safelySetChecked(t.actModeRule, state.Mode == "rule")
	t.safelySetChecked(t.actModeDirect, state.Mode == "direct")
	t.safelySetChecked(t.actModeGlobal, state.Mode == "global")

	t.safelySetChecked(t.actAutoStart, state.AutoStart)
	t.safelySetChecked(t.actRunAdmin, state.RunAsAdmin || state.AutoStart)
	t.actRunAdmin.SetEnabled(!state.AutoStart)
	t.safelySetChecked(t.actSysBrowser, state.UseSystemBrowser)
	t.safelySetChecked(t.actRemoteWebUI, state.RemoteWebUI)
	t.safelySetChecked(t.actAllowLan, state.AllowLan)

	if stateChanged {
		t.actModeMenu.SetText(fmt.Sprintf("路由模式: %s", getModeName(state.Mode)))
		adminText := "运行权限：普通用户"
		if state.IsAdmin {
			adminText = "运行权限：管理员"
		}
		t.actAdminMenu.SetText(adminText)
	}

	fp := generateProfileFingerprint(state.ProfileItems)
	if fp != t.lastProfileFingerprint {
		t.rebuildProfilesMenu(state)
		t.lastProfileFingerprint = fp
	} else if t.menuSwitchProfile != nil && len(state.ProfileItems) > 0 {
		actions := t.menuSwitchProfile.Actions()
		for i, item := range state.ProfileItems {
			if i < actions.Len() {
				t.safelySetChecked(actions.At(i), item.IsActive)
			}
		}
	}
}

func (t *Tray) safelySetChecked(act *walk.Action, checked bool) {
	if act != nil && act.Checked() != checked {
		act.SetChecked(checked)
	}
}

func (t *Tray) buildMenuSkeleton() {
	actions := t.ni.ContextMenu().Actions()
	actions.Clear()

	t.addAction("进入 Web 面板", func() { t.engine.SendCommand(domain.ActionOpenWebUI, "") })
	t.addSeparator()

	t.actProxy = t.addCheckableAction("系统代理", t.latestState.IsProxy, func() { t.engine.SendCommand(domain.ActionToggleProxy, fmt.Sprintf("%t", !t.latestState.IsProxy)) })
	t.actTun = t.addCheckableAction("TUN 模式", t.latestState.IsTun, func() { t.engine.SendCommand(domain.ActionToggleTun, fmt.Sprintf("%t", !t.latestState.IsTun)) })

	modeMenu, modeMenuAction := t.addSubMenu(fmt.Sprintf("路由模式: %s", getModeName(t.latestState.Mode)))
	t.actModeMenu = modeMenuAction
	t.actModeRule = t.addCheckableSubAction(modeMenu, "规则", t.latestState.Mode == "rule", func() { t.engine.SendCommand(domain.ActionSwitchMode, "rule") })
	t.actModeDirect = t.addCheckableSubAction(modeMenu, "直连", t.latestState.Mode == "direct", func() { t.engine.SendCommand(domain.ActionSwitchMode, "direct") })
	t.actModeGlobal = t.addCheckableSubAction(modeMenu, "全局", t.latestState.Mode == "global", func() { t.engine.SendCommand(domain.ActionSwitchMode, "global") })

	t.addSeparator()

	t.menuSwitchProfile, _ = t.addSubMenu("切换配置文件")

	t.addAction("管理/添加配置", func() { t.engine.SendCommand(domain.ActionOpenProfileManager, "") })

	t.addSeparator()
	t.addAction("编辑当前文本", func() { t.engine.SendCommand(domain.ActionEditCurrentConfig, "") })
	t.addAction("打开程序目录", func() { t.engine.SendCommand(domain.ActionOpenBaseDir, "") })
	t.addSeparator()

	adminMenu, adminMenuAction := t.addSubMenu("运行权限")
	t.actAdminMenu = adminMenuAction
	t.actAutoStart = t.addCheckableSubAction(adminMenu, "开机自启（管理员）", t.latestState.AutoStart, func() { t.engine.SendCommand(domain.ActionToggleAutoStart, fmt.Sprintf("%t", !t.latestState.AutoStart)) })
	t.actRunAdmin = t.addCheckableSubAction(adminMenu, "始终以管理员身份运行", t.latestState.RunAsAdmin || t.latestState.AutoStart, func() { t.engine.SendCommand(domain.ActionToggleRunAsAdmin, fmt.Sprintf("%t", !t.latestState.RunAsAdmin)) })

	moreMenu, _ := t.addSubMenu("更多设置")
	t.addActionTo(moreMenu, "打开应用配置 (mihomo-tray.json)", func() { t.engine.SendCommand(domain.ActionOpenAppConfig, "") })
	t.addActionTo(moreMenu, "-", nil)
	t.addActionTo(moreMenu, "复制 Web 访问密码", func() { t.engine.SendCommand(domain.ActionCopyWebUIPassword, "") })
	t.addActionTo(moreMenu, "清理 Web 面板缓存", func() {
		t.engine.SendCommand(domain.ActionClearWebUICache, "")
	})

	t.addActionTo(moreMenu, "-", nil)
	t.actRemoteWebUI = t.addCheckableSubAction(moreMenu, "使用在线 Web 面板", t.latestState.RemoteWebUI, func() { t.engine.SendCommand(domain.ActionToggleRemoteWebUI, fmt.Sprintf("%t", !t.latestState.RemoteWebUI)) })
	t.actSysBrowser = t.addCheckableSubAction(moreMenu, "使用默认浏览器打开面板", t.latestState.UseSystemBrowser, func() { t.engine.SendCommand(domain.ActionToggleSystemBrowser, fmt.Sprintf("%t", !t.latestState.UseSystemBrowser)) })
	t.actAllowLan = t.addCheckableSubAction(moreMenu, "允许局域网代理", t.latestState.AllowLan, func() { t.engine.SendCommand(domain.ActionToggleAllowLan, fmt.Sprintf("%t", !t.latestState.AllowLan)) })
	t.addActionTo(moreMenu, "-", nil)
	t.addActionTo(moreMenu, "重载当前配置", func() { t.engine.SendCommand(domain.ActionReloadConfig, "") })
	t.addActionTo(moreMenu, "重启内核", func() { t.engine.SendCommand(domain.ActionRestartKernel, "") })

	t.addSeparator()
	t.addAction("退出程序", func() {
		t.engine.SendCommand(domain.ActionExitApp, "")
	})
}

func (t *Tray) rebuildProfilesMenu(state domain.UIState) {
	if t.menuSwitchProfile == nil {
		return
	}
	actions := t.menuSwitchProfile.Actions()
	for i := actions.Len() - 1; i >= 0; i-- {
		actions.At(i).Dispose()
	}
	actions.Clear()

	if len(state.ProfileItems) == 0 {
		emptyAction := walk.NewAction()
		emptyAction.SetText("暂无配置")
		emptyAction.SetEnabled(false)
		actions.Add(emptyAction)
	} else {
		for _, item := range state.ProfileItems {
			targetPath := item.Path
			suffix := " (本地)"
			if item.IsRemote {
				suffix = " (订阅)"
			}
			t.addCheckableSubAction(t.menuSwitchProfile, item.Name+suffix, item.IsActive, func() {
				t.engine.SendCommand(domain.ActionSwitchProfile, targetPath)
			})
		}
	}
}

func (t *Tray) ShowNotification(title, message string) {
	if t.ni != nil {
		t.engine.RunOnUI(func() { _ = t.ni.ShowInfo(title, message) })
	}
}

func (t *Tray) Dispose() {
	if t.ni != nil {
		t.ni.Dispose()
	}
	for _, icon := range t.icons {
		if icon != nil {
			icon.Dispose()
		}
	}
	if t.iconDir != "" {
		_ = os.RemoveAll(t.iconDir)
	}
}

func getModeName(mode string) string {
	modeNames := map[string]string{"rule": "规则", "direct": "直连", "global": "全局"}
	if name := modeNames[mode]; name != "" {
		return name
	}
	return "未知"
}

func generateProfileFingerprint(items []domain.UIProfileItem) string {
	var sb strings.Builder
	for _, item := range items {
		sb.WriteString(item.Name + item.Path)
		if item.IsRemote {
			sb.WriteString("R")
		}
	}
	return sb.String()
}

func (t *Tray) addAction(text string, f func()) *walk.Action {
	return t.addActionTo(t.ni.ContextMenu(), text, f)
}
func (t *Tray) addActionTo(menu *walk.Menu, text string, f func()) *walk.Action {
	action := walk.NewAction()
	action.SetText(text)
	if f != nil {
		action.Triggered().Attach(f)
	}
	menu.Actions().Add(action)
	return action
}
func (t *Tray) addCheckableAction(text string, checked bool, f func()) *walk.Action {
	action := t.addAction(text, f)
	action.SetCheckable(true)
	action.SetChecked(checked)
	return action
}
func (t *Tray) addCheckableSubAction(menu *walk.Menu, text string, checked bool, f func()) *walk.Action {
	action := t.addActionTo(menu, text, f)
	action.SetCheckable(true)
	action.SetChecked(checked)
	return action
}
func (t *Tray) addSubMenu(text string) (*walk.Menu, *walk.Action) {
	subMenu, _ := walk.NewMenu()
	subAction := walk.NewMenuAction(subMenu)
	subAction.SetText(text)
	t.ni.ContextMenu().Actions().Add(subAction)
	return subMenu, subAction
}
func (t *Tray) addSeparator() {
	t.ni.ContextMenu().Actions().Add(walk.NewSeparatorAction())
}
