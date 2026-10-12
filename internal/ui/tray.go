package ui

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
	"mihomo-tray/internal/domain"
)

type Tray struct {
	engine      *Engine
	ni          *walk.NotifyIcon
	iconMgr     *IconManager
	lastIconId  int
	stopWatcher chan struct{}

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

var trayGUID = windows.GUID{
	Data1: 0x6a1b2c3d,
	Data2: 0x4e5f,
	Data3: 0x7a8b,
	Data4: [8]byte{0x9c, 0x0d, 0x1e, 0x2f, 0x3a, 0x4b, 0x5c, 0x6d},
}

func NewTray(e *Engine) *Tray {
	slog.Debug("[Tray] 开始初始化 NewTray")

	ni, err := walk.NewNotifyIconWithGUID(trayGUID)
	if err != nil {
		slog.Warn("[Tray] 带 GUID 初始化失败，回退至普通模式", "err", err)
		ni, err = walk.NewNotifyIcon()
		if err != nil {
			slog.Error("[Tray] 创建系统托盘图标失败", "err", err)
			return nil
		}
	}
	
	slog.Debug("[Tray] walk.NewNotifyIcon 句柄创建成功")

	if err := ni.SetVisible(true); err != nil {
		slog.Warn("[Tray] 设置托盘可见性失败", "err", err)
	}
	if err := ni.SetToolTip("Mihomo Tray"); err != nil {
		slog.Warn("[Tray] 设置 ToolTip 失败", "err", err)
	}

	t := &Tray{
		engine:                 e,
		ni:                     ni,
		iconMgr:                NewIconManager(),
		lastIconId:             -1,
		stopWatcher:            make(chan struct{}),
		lastProfileFingerprint: "__INIT__",
	}

	slog.Debug("[Tray] 加载初始托盘图标 (IconStop)")
	t.applyIcon(int(domain.IconStop))

	ni.MouseUp().Attach(func(x, y int, button walk.MouseButton) {
		slog.Debug("[Tray] 捕获鼠标点击事件", "button", button, "x", x, "y", y)
		if button == walk.LeftButton {
			t.engine.SendCommand(domain.ActionOpenWebUI, "")
		}
	})

	t.startDpiWatcher()

	slog.Debug("[Tray] NewTray 初始化流程全部完成")
	return t
}

func (t *Tray) applyIcon(iconId int) {
	slog.Debug("[Tray] applyIcon 触发", "targetIconId", iconId, "lastIconId", t.lastIconId)
	if t.ni == nil {
		slog.Warn("[Tray] applyIcon 终止: t.ni 为 nil")
		return
	}

	ico := t.iconMgr.Get(iconId)
	if ico == nil {
		slog.Warn("[Tray] applyIcon 终止: 获取到的图标为 nil", "iconId", iconId)
		return
	}

	if err := t.ni.SetIcon(ico); err != nil {
		slog.Warn("[Tray] 调用 t.ni.SetIcon 失败", "iconId", iconId, "err", err)
		return
	}

	t.lastIconId = iconId
	slog.Debug("[Tray] t.ni.SetIcon 成功生效", "iconId", iconId)
}

func (t *Tray) startDpiWatcher() {
	slog.Debug("[Tray] 启动 DPI 监控协程 (每 1.5 秒检查一次)")
	go func() {
		ticker := time.NewTicker(1500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-t.stopWatcher:
				slog.Debug("[Tray] 收到退出信号，终止 DPI 监控协程")
				return
			case <-ticker.C:
				if t.ni == nil {
					slog.Debug("[Tray] 托盘句柄为空，退出 DPI 监控协程")
					return
				}

				if t.iconMgr.HasDpiChanged() {
					slog.Warn("[Tray] [DPI-Watcher] 检测到缩放比例变化，切入 UI 线程热重载图标")
					walk.App().Synchronize(func() {
						slog.Debug("[Tray] [UI-Thread] 同步执行 applyIcon 热刷新")
						t.applyIcon(t.lastIconId)
					})
				}
			}
		}
	}()
}

func (t *Tray) UpdateState(state domain.UIState) {
	slog.Debug("[Tray] UpdateState 收到状态推送",
		"iconState", state.IconState,
		"isProxy", state.IsProxy,
		"isTun", state.IsTun,
		"mode", state.Mode,
		"isAdmin", state.IsAdmin,
		"autoStart", state.AutoStart,
		"profilesCount", len(state.ProfileItems),
	)

	if t.ni == nil {
		slog.Warn("[Tray] UpdateState 终止: t.ni 为 nil")
		return
	}

	stateChanged := t.latestState.Mode != state.Mode ||
		t.latestState.IsAdmin != state.IsAdmin ||
		t.latestState.AutoStart != state.AutoStart

	t.latestState = state

	iconIdx := int(state.IconState)
	if iconIdx != t.lastIconId || t.iconMgr.HasDpiChanged() {
		t.applyIcon(iconIdx)
	}

	if !t.isBuilt {
		slog.Debug("[Tray] 首次进入 UpdateState，构建菜单骨架")
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
		slog.Debug("[Tray] 状态变更，刷新菜单文本", "mode", state.Mode, "isAdmin", state.IsAdmin)
		safelySetText(t.actModeMenu, fmt.Sprintf("路由模式: %s", getModeName(state.Mode)))
		adminText := "运行权限：普通用户"
		if state.IsAdmin {
			adminText = "运行权限：管理员"
		}
		safelySetText(t.actAdminMenu, adminText)
	}

	fp := generateProfileFingerprint(state.ProfileItems)
	if fp != t.lastProfileFingerprint {
		slog.Debug("[Tray] 配置项指纹发生变化，重建配置菜单", "oldFp", t.lastProfileFingerprint, "newFp", fp)
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
	slog.Debug("[Tray] 开始构建托盘菜单骨架")
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
	slog.Debug("[Tray] 托盘菜单骨架构建完毕")
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
	slog.Debug("[Tray] Dispose 释放托盘所有资源")
	if t.stopWatcher != nil {
		close(t.stopWatcher)
		t.stopWatcher = nil
	}
	if t.ni != nil {
		t.ni.Dispose()
		t.ni = nil
	}
	if t.iconMgr != nil {
		t.iconMgr.Dispose()
		t.iconMgr = nil
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
