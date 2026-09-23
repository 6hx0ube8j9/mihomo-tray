package ui

import (
	"fmt"
	"strings"

	"github.com/tailscale/walk"
	
	"mihomo-tray/internal/domain"
)

type TrayMenuCache struct {
	isBuilt bool

	actProxy      *walk.Action
	actTun        *walk.Action

	actModeRule   *walk.Action
	actModeDirect *walk.Action
	actModeGlobal *walk.Action
	actModeMenu   *walk.Action

	menuSwitchProfile *walk.Menu

	actAutoStart *walk.Action
	actRunAdmin  *walk.Action
	actAdminMenu *walk.Action

	actSysBrowser *walk.Action
	actAllowLan   *walk.Action

	lastProfileFingerprint string
}

var trayCache = &TrayMenuCache{}

func ShowTrayNotification(title, message string) {
	if globalUIEngine != nil && globalUIEngine.ni != nil {
		globalUIEngine.app.Synchronize(func() {
			_ = globalUIEngine.ni.ShowInfo(title, message)
		})
	}
}

func ShowInfoMessage(owner walk.Form, title, message string) {
	if globalUIEngine == nil || globalUIEngine.app == nil {
		return
	}
	globalUIEngine.app.Synchronize(func() {
		RunErrorDialog(owner, title, message)
	})
}

func (e *UIEngine) updateTrayState(state domain.UIState) {
	if e.ni == nil || e.app == nil {
		return
	}

	e.app.Synchronize(func() {
		if !trayCache.isBuilt {
			buildMenuSkeleton(e, state)
			trayCache.isBuilt = true
		}

		trayCache.actProxy.SetChecked(state.IsProxy)
		trayCache.actTun.SetChecked(state.IsTun)

		trayCache.actModeRule.SetChecked(state.Mode == "rule")
		trayCache.actModeDirect.SetChecked(state.Mode == "direct")
		trayCache.actModeGlobal.SetChecked(state.Mode == "global")
		trayCache.actModeMenu.SetText(fmt.Sprintf("路由模式: %s", getModeName(state.Mode)))

		adminText := "运行权限：普通用户"
		if state.IsAdmin {
			adminText = "运行权限：管理员"
		}
		trayCache.actAdminMenu.SetText(adminText)

		trayCache.actAutoStart.SetChecked(state.AutoStart)
		trayCache.actRunAdmin.SetChecked(state.RunAsAdmin || state.AutoStart)
		trayCache.actRunAdmin.SetEnabled(!state.AutoStart)

		trayCache.actSysBrowser.SetChecked(state.UseSystemBrowser)
		trayCache.actAllowLan.SetChecked(state.AllowLan)

		fp := generateProfileFingerprint(state.ProfileItems)
		if fp != trayCache.lastProfileFingerprint {
			rebuildProfilesMenu(e, state)
			trayCache.lastProfileFingerprint = fp
		} else {
			if trayCache.menuSwitchProfile != nil && len(state.ProfileItems) > 0 {
				actions := trayCache.menuSwitchProfile.Actions()
				for i, item := range state.ProfileItems {
					if i < actions.Len() {
						actions.At(i).SetChecked(item.IsActive)
					}
				}
			}
		}
	})
}

func buildMenuSkeleton(e *UIEngine, state domain.UIState) {
	actions := e.ni.ContextMenu().Actions()
	actions.Clear()

	e.addAction("进入 Web 面板", func() { e.sendCommand(domain.ActionOpenWebUI, "") })
	e.addSeparator()

	trayCache.actProxy = e.addCheckableAction("系统代理", state.IsProxy, func() { e.sendCommand(domain.ActionToggleProxy, fmt.Sprintf("%t", !trayCache.actProxy.Checked())) })
	trayCache.actTun = e.addCheckableAction("TUN 模式", state.IsTun, func() { e.sendCommand(domain.ActionToggleTun, fmt.Sprintf("%t", !trayCache.actTun.Checked())) })

	modeMenu, modeMenuAction := e.addSubMenu(fmt.Sprintf("路由模式: %s", getModeName(state.Mode)))
	trayCache.actModeMenu = modeMenuAction

	trayCache.actModeRule = e.addCheckableSubAction(modeMenu, "规则", state.Mode == "rule", func() { e.sendCommand(domain.ActionSwitchMode, "rule") })
	trayCache.actModeDirect = e.addCheckableSubAction(modeMenu, "直连", state.Mode == "direct", func() { e.sendCommand(domain.ActionSwitchMode, "direct") })
	trayCache.actModeGlobal = e.addCheckableSubAction(modeMenu, "全局", state.Mode == "global", func() { e.sendCommand(domain.ActionSwitchMode, "global") })

	e.addSeparator()

	trayCache.menuSwitchProfile, _ = e.addSubMenu("切换配置文件")

	e.addAction("编辑当前配置", func() { e.sendCommand(domain.ActionEditCurrentConfig, "") })
	e.addAction("添加配置", func() { e.sendCommand(domain.ActionOpenProfileManager, "") })

	e.addSeparator()

	e.addAction("打开程序目录", func() { e.sendCommand(domain.ActionOpenBaseDir, "") })
	e.addSeparator()

	adminMenu, adminMenuAction := e.addSubMenu("运行权限")
	trayCache.actAdminMenu = adminMenuAction

	trayCache.actAutoStart = e.addCheckableSubAction(adminMenu, "开机自启（管理员）", state.AutoStart, func() {
		e.sendCommand(domain.ActionToggleAutoStart, fmt.Sprintf("%t", !trayCache.actAutoStart.Checked()))
	})
	trayCache.actRunAdmin = e.addCheckableSubAction(adminMenu, "始终以管理员身份运行", state.RunAsAdmin || state.AutoStart, func() {
		e.sendCommand(domain.ActionToggleRunAsAdmin, fmt.Sprintf("%t", !trayCache.actRunAdmin.Checked()))
	})

	moreMenu, _ := e.addSubMenu("更多设置")

	e.addActionTo(moreMenu, "复制 Web 访问密码", func() {
		e.sendCommand(domain.ActionCopyWebUIPassword, "")
	})

	e.addActionTo(moreMenu, "清理 Web 面板缓存", func() {
		go func() {
			if ShowConfirmMessage(nil, "确认清理缓存？", "清理 Web 面板缓存将同时清除面板配置（包含布局、主题等），且无法恢复。建议在操作前先导出备份。\n\n是否继续？") {
				e.sendCommand(domain.ActionClearWebUICache, "")
			}
		}()
	})

	trayCache.actSysBrowser = e.addCheckableSubAction(moreMenu, "使用默认浏览器打开面板", state.UseSystemBrowser, func() {
		e.sendCommand(domain.ActionToggleSystemBrowser, fmt.Sprintf("%t", !trayCache.actSysBrowser.Checked()))
	})

	trayCache.actAllowLan = e.addCheckableSubAction(moreMenu, "允许局域网代理", state.AllowLan, func() {
		e.sendCommand(domain.ActionToggleAllowLan, fmt.Sprintf("%t", !trayCache.actAllowLan.Checked()))
	})

	e.addActionTo(moreMenu, "-", nil)
	e.addActionTo(moreMenu, "重载当前配置", func() { e.sendCommand(domain.ActionReloadConfig, "") })
	e.addActionTo(moreMenu, "重启内核", func() { e.sendCommand(domain.ActionRestartKernel, "") })

	e.addSeparator()
	e.addAction("退出程序", func() {
		e.sendCommand(domain.ActionExitApp, "")
		e.app.Synchronize(func() { e.mw.Close() })
	})
}

func rebuildProfilesMenu(e *UIEngine, state domain.UIState) {
	if trayCache.menuSwitchProfile == nil {
		return
	}

	actions := trayCache.menuSwitchProfile.Actions()
	for i := 0; i < actions.Len(); i++ {
		action := actions.At(i)
		action.Dispose()
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
			e.addCheckableSubAction(trayCache.menuSwitchProfile, item.Name+suffix, item.IsActive, func() {
				e.sendCommand(domain.ActionSwitchProfile, targetPath)
			})
		}
	}
}

func getModeName(mode string) string {
	modeNames := map[string]string{"rule": "规则", "direct": "直连", "global": "全局"}
	name := modeNames[mode]
	if name == "" {
		return "未知"
	}
	return name
}

func generateProfileFingerprint(items []domain.UIProfileItem) string {
	var sb strings.Builder
	for _, item := range items {
		sb.WriteString(item.Name)
		sb.WriteString(item.Path)
		if item.IsActive {
			sb.WriteString("Y")
		}
		if item.IsRemote {
			sb.WriteString("R")
		}
	}
	return sb.String()
}

func (e *UIEngine) addAction(text string, onTriggered func()) *walk.Action {
	return e.addActionTo(e.ni.ContextMenu(), text, onTriggered)
}

func (e *UIEngine) addActionTo(menu *walk.Menu, text string, onTriggered func()) *walk.Action {
	action := walk.NewAction()
	action.SetText(text)
	if onTriggered != nil {
		action.Triggered().Attach(onTriggered)
	}
	menu.Actions().Add(action)
	return action
}

func (e *UIEngine) addCheckableAction(text string, checked bool, onTriggered func()) *walk.Action {
	action := e.addAction(text, onTriggered)
	action.SetCheckable(true)
	action.SetChecked(checked)
	return action
}

func (e *UIEngine) addCheckableSubAction(menu *walk.Menu, text string, checked bool, onTriggered func()) *walk.Action {
	action := e.addActionTo(menu, text, onTriggered)
	action.SetCheckable(true)
	action.SetChecked(checked)
	return action
}

func (e *UIEngine) addSubMenu(text string) (*walk.Menu, *walk.Action) {
	subMenu, _ := walk.NewMenu()
	subAction := walk.NewMenuAction(subMenu)
	subAction.SetText(text)
	e.ni.ContextMenu().Actions().Add(subAction)
	return subMenu, subAction
}

func (e *UIEngine) addSeparator() {
	separator := walk.NewSeparatorAction()
	e.ni.ContextMenu().Actions().Add(separator)
}
