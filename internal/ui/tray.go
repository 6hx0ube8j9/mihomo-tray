package ui

import (
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
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
		slog.Error("创建系统托盘图标失败", "err", err)
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
		slog.Error("创建托盘图标临时目录失败", "err", err)
		return
	}
	t.iconDir = tmpDir

	for id, name := range trayIconAssets {
		if name == "" {
			continue
		}
		data, err := iconFs.ReadFile("icons/" + name)
		if err != nil {
			slog.Error("读取内置图标失败", "name", name, "err", err)
			continue
		}

		tmpPath := filepath.Join(tmpDir, name)
		if err := os.WriteFile(tmpPath, data, 0644); err != nil {
			slog.Error("写入图标缓存文件失败", "path", tmpPath, "err", err)
			continue
		}

		ico, err := walk.NewIconFromFile(tmpPath)
		if err != nil {
			slog.Error("解析图标文件失败", "path", tmpPath, "err", err)
			continue
		}
		t.icons[id] = ico
	}

	idx := int(domain.IconStop)
	if idx < len(t.icons) && t.icons[idx] != nil {
		t.ni.SetIcon(t.icons[idx])
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

	safelySetChecked(t.actProxy, state.IsProxy)
	safelySetChecked(t.actTun, state.IsTun)

	safelySetChecked(t.actModeRule, state.Mode == domain.ModeRule)
	safelySetChecked(t.actModeDirect, state.Mode == domain.ModeDirect)
	safelySetChecked(t.actModeGlobal, state.Mode == domain.ModeGlobal)

	safelySetChecked(t.actAutoStart, state.AutoStart)
	safelySetChecked(t.actRunAdmin, state.RunAsAdmin || state.AutoStart)
	safelySetEnabled(t.actRunAdmin, !state.AutoStart)
	safelySetChecked(t.actSysBrowser, state.UseSystemBrowser)
	safelySetChecked(t.actRemoteWebUI, state.RemoteWebUI)
	safelySetChecked(t.actAllowLan, state.AllowLan)

	if stateChanged {
		safelySetText(t.actModeMenu, fmt.Sprintf("路由模式: %s", getModeName(state.Mode)))
		adminText := "运行权限：普通用户"
		if state.IsAdmin {
			adminText = "运行权限：管理员"
		}
		safelySetText(t.actAdminMenu, adminText)
	}

	fp := generateProfileFingerprint(state.ProfileItems)
	if fp != t.lastProfileFingerprint {
		t.rebuildProfilesMenu(state)
		t.lastProfileFingerprint = fp
	} else if t.menuSwitchProfile != nil && len(state.ProfileItems) > 0 {
		actions := t.menuSwitchProfile.Actions()
		for i, item := range state.ProfileItems {
			if i < actions.Len() {
				safelySetChecked(actions.At(i), item.IsActive)
			}
		}
	}
}

func (t *Tray) buildMenuSkeleton() {
	actions := t.ni.ContextMenu().Actions()
	actions.Clear()

	t.addAction("进入 Web 面板", func() { t.engine.SendCommand(domain.ActionOpenWebUI, "") })
	t.addSeparator()

	t.actProxy = t.addCheckableAction("系统代理", t.latestState.IsProxy, func() {
		t.toggle(domain.ActionToggleProxy, !t.latestState.IsProxy)
	})
	t.actTun = t.addCheckableAction("TUN 模式", t.latestState.IsTun, func() {
		t.toggle(domain.ActionToggleTun, !t.latestState.IsTun)
	})

	modeMenu, modeMenuAction := t.addSubMenu(fmt.Sprintf("路由模式: %s", getModeName(t.latestState.Mode)))
	t.actModeMenu = modeMenuAction
	t.actModeRule = t.addCheckableSubAction(modeMenu, "规则", t.latestState.Mode == domain.ModeRule, func() {
		t.engine.SendCommand(domain.ActionSwitchMode, domain.ModeRule)
	})
	t.actModeDirect = t.addCheckableSubAction(modeMenu, "直连", t.latestState.Mode == domain.ModeDirect, func() {
		t.engine.SendCommand(domain.ActionSwitchMode, domain.ModeDirect)
	})
	t.actModeGlobal = t.addCheckableSubAction(modeMenu, "全局", t.latestState.Mode == domain.ModeGlobal, func() {
		t.engine.SendCommand(domain.ActionSwitchMode, domain.ModeGlobal)
	})

	t.addSeparator()

	t.menuSwitchProfile, _ = t.addSubMenu("切换配置文件")
	t.addAction("管理/添加配置", func() { t.engine.SendCommand(domain.ActionOpenProfileManager, "") })

	t.addSeparator()
	t.addAction("编辑当前文本", func() { t.engine.SendCommand(domain.ActionEditCurrentConfig, "") })
	t.addAction("打开程序目录", func() { t.engine.SendCommand(domain.ActionOpenBaseDir, "") })
	t.addSeparator()

	adminMenu, adminMenuAction := t.addSubMenu("运行权限")
	t.actAdminMenu = adminMenuAction
	t.actAutoStart = t.addCheckableSubAction(adminMenu, "开机自启（管理员）", t.latestState.AutoStart, func() {
		t.toggle(domain.ActionToggleAutoStart, !t.latestState.AutoStart)
	})
	t.actRunAdmin = t.addCheckableSubAction(adminMenu, "始终以管理员身份运行", t.latestState.RunAsAdmin || t.latestState.AutoStart, func() {
		t.toggle(domain.ActionToggleRunAsAdmin, !t.latestState.RunAsAdmin)
	})

	moreMenu, _ := t.addSubMenu("更多设置")
	t.addActionTo(moreMenu, fmt.Sprintf("打开应用配置 (%s)", domain.TrayConfigName), func() {
		t.engine.SendCommand(domain.ActionOpenAppConfig, "")
	})
	t.addSeparatorTo(moreMenu)
	t.addActionTo(moreMenu, "复制 Web 访问密码", func() {
		t.engine.SendCommand(domain.ActionCopyWebUIPassword, "")
	})
	t.addActionTo(moreMenu, "清理 Web 面板缓存", func() {
		t.engine.SendCommand(domain.ActionClearWebUICache, "")
	})

	t.addSeparatorTo(moreMenu)
	t.actRemoteWebUI = t.addCheckableSubAction(moreMenu, "使用在线 Web 面板", t.latestState.RemoteWebUI, func() {
		t.toggle(domain.ActionToggleRemoteWebUI, !t.latestState.RemoteWebUI)
	})
	t.actSysBrowser = t.addCheckableSubAction(moreMenu, "使用默认浏览器打开面板", t.latestState.UseSystemBrowser, func() {
		t.toggle(domain.ActionToggleSystemBrowser, !t.latestState.UseSystemBrowser)
	})
	t.actAllowLan = t.addCheckableSubAction(moreMenu, "允许局域网代理", t.latestState.AllowLan, func() {
		t.toggle(domain.ActionToggleAllowLan, !t.latestState.AllowLan)
	})
	t.addSeparatorTo(moreMenu)
	t.addActionTo(moreMenu, "重载当前配置", func() {
		t.engine.SendCommand(domain.ActionReloadConfig, "")
	})
	t.addActionTo(moreMenu, "重启内核", func() {
		t.engine.SendCommand(domain.ActionRestartKernel, "")
	})

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
		return
	}

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

func (t *Tray) ShowNotification(title, message string) {
	if t.ni == nil {
		return
	}
	if err := t.ni.ShowInfo(title, message); err != nil {
		slog.Warn("弹出系统托盘通知失败", "title", title, "err", err)
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
		if err := os.RemoveAll(t.iconDir); err != nil {
			slog.Warn("清理图标临时目录失败", "dir", t.iconDir, "err", err)
		}
	}
}

func (t *Tray) toggle(action string, nextVal bool) {
	t.engine.SendCommand(action, strconv.FormatBool(nextVal))
}

func getModeName(mode string) string {
	switch mode {
	case domain.ModeRule:
		return "规则"
	case domain.ModeDirect:
		return "直连"
	case domain.ModeGlobal:
		return "全局"
	default:
		return "未知"
	}
}

func generateProfileFingerprint(items []domain.UIProfileItem) string {
	var sb strings.Builder
	for _, item := range items {
		sb.WriteString(item.Name)
		sb.WriteString(item.Path)
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

func (t *Tray) addSeparatorTo(menu *walk.Menu) {
	menu.Actions().Add(walk.NewSeparatorAction())
}
