package ui

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"syscall"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
	"mihomo-tray/internal/domain"
)

var (
	trayClassName = syscall.StringToUTF16Ptr("Shell_TrayWnd")

	trayIconResourceNames = []string{
		domain.IconStop:    "ICON_STOP",
		domain.IconError:   "ICON_ERROR",
		domain.IconTun:     "ICON_TUN",
		domain.IconProxy:   "ICON_PROXY",
		domain.IconDefault: "ICON_DEFAULT",
	}
)

type Tray struct {
	engine *Engine
	ni     *walk.NotifyIcon

	currentIconSize int
	lastIconId      int
	iconCache       map[int]*walk.Icon

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

	t := &Tray{
		engine:                 e,
		ni:                     ni,
		lastIconId:             -1,
		iconCache:              make(map[int]*walk.Icon),
		lastProfileFingerprint: "__INIT__",
	}

	t.applyIcon(int(domain.IconStop))

	t.buildMenuSkeleton()

	ni.SetToolTip("Mihomo Tray")
	ni.MouseUp().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			t.engine.SendCommand(domain.ActionOpenWebUI, "")
		}
	})

	ni.SetVisible(true)
	return t
}

func (t *Tray) UpdateState(state domain.UIState) {
	if t.ni == nil {
		return
	}

	stateChanged := t.latestState.Mode != state.Mode ||
		t.latestState.IsAdmin != state.IsAdmin ||
		t.latestState.AutoStart != state.AutoStart

	t.latestState = state
	t.applyIcon(int(state.IconState))

	t.actProxy.SetChecked(state.IsProxy)
	t.actTun.SetChecked(state.IsTun)

	t.actModeRule.SetChecked(state.Mode == domain.ModeRule)
	t.actModeDirect.SetChecked(state.Mode == domain.ModeDirect)
	t.actModeGlobal.SetChecked(state.Mode == domain.ModeGlobal)

	t.actAutoStart.SetChecked(state.AutoStart)
	t.actRunAdmin.SetChecked(state.RunAsAdmin || state.AutoStart)
	t.actRunAdmin.SetEnabled(!state.AutoStart)
	t.actSysBrowser.SetChecked(state.UseSystemBrowser)
	t.actRemoteWebUI.SetChecked(state.RemoteWebUI)
	t.actAllowLan.SetChecked(state.AllowLan)

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
				actions.At(i).SetChecked(item.IsActive)
			}
		}
	}
}

func (t *Tray) applyIcon(stateId int) {
	if stateId < 0 || stateId >= len(trayIconResourceNames) {
		return
	}

	targetSize := getTrayIconMetricSize()
	sizeChanged := targetSize != t.currentIconSize
	idChanged := stateId != t.lastIconId

	if !sizeChanged && !idChanged {
		return
	}

	if sizeChanged {
		t.disposeIcons()
		t.currentIconSize = targetSize
	}

	ico, ok := t.iconCache[stateId]
	if !ok {
		resName := trayIconResourceNames[stateId]
		if resName == "" {
			return
		}
		var err error
		ico, err = walk.NewIconFromResourceWithSize(resName, walk.Size{
			Width:  targetSize,
			Height: targetSize,
		})
		if err != nil {
			slog.Error("从程序资源加载对应尺寸图标失败", "resource", resName, "size", targetSize, "err", err)
			return
		}
		t.iconCache[stateId] = ico
	}

	if err := t.ni.SetIcon(ico); err != nil {
		slog.Warn("更新托盘图标失败", "stateId", stateId, "size", targetSize, "err", err)
		return
	}

	t.lastIconId = stateId
}

func (t *Tray) disposeIcons() {
	for _, ico := range t.iconCache {
		if ico != nil {
			ico.Dispose()
		}
	}
	t.iconCache = make(map[int]*walk.Icon)
}

func (t *Tray) Dispose() {
	if t.ni != nil {
		t.ni.Dispose()
	}
	t.disposeIcons()
}

func getTrayIconMetricSize() int {
	hwnd := win.FindWindow(trayClassName, nil)
	dpi := win.GetDpiForWindow(hwnd)
	if dpi == 0 {
		dpi = 96
	}

	if size := int(win.GetSystemMetricsForDpi(win.SM_CXSMICON, dpi)); size > 0 {
		return size
	}

	return walk.IntFrom96DPI(16, int(dpi))
}

func (t *Tray) buildMenuSkeleton() {
	t.addAction("进入 Web 面板", func() { t.engine.SendCommand(domain.ActionOpenWebUI, "") })
	t.addSeparator()

	t.actProxy = t.addCheckableAction("系统代理", false, func() {
		t.toggle(domain.ActionToggleProxy, !t.latestState.IsProxy)
	})
	t.actTun = t.addCheckableAction("TUN 模式", false, func() {
		t.toggle(domain.ActionToggleTun, !t.latestState.IsTun)
	})

	modeMenu, modeMenuAction := t.addSubMenu("路由模式: 规则")
	t.actModeMenu = modeMenuAction
	t.actModeRule = t.addCheckableSubAction(modeMenu, "规则", true, func() {
		t.engine.SendCommand(domain.ActionSwitchMode, domain.ModeRule)
	})
	t.actModeDirect = t.addCheckableSubAction(modeMenu, "直连", false, func() {
		t.engine.SendCommand(domain.ActionSwitchMode, domain.ModeDirect)
	})
	t.actModeGlobal = t.addCheckableSubAction(modeMenu, "全局", false, func() {
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
	t.actAutoStart = t.addCheckableSubAction(adminMenu, "开机自启（管理员）", false, func() {
		t.toggle(domain.ActionToggleAutoStart, !t.latestState.AutoStart)
	})
	t.actRunAdmin = t.addCheckableSubAction(adminMenu, "始终以管理员身份运行", false, func() {
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
	t.actRemoteWebUI = t.addCheckableSubAction(moreMenu, "使用在线 Web 面板", false, func() {
		t.toggle(domain.ActionToggleRemoteWebUI, !t.latestState.RemoteWebUI)
	})
	t.actSysBrowser = t.addCheckableSubAction(moreMenu, "使用默认浏览器打开面板", false, func() {
		t.toggle(domain.ActionToggleSystemBrowser, !t.latestState.UseSystemBrowser)
	})
	t.actAllowLan = t.addCheckableSubAction(moreMenu, "允许局域网代理", false, func() {
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
