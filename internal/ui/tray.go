package ui

import (
	"fmt"
	"github.com/tailscale/walk"
	"mihomo-tray/internal/domain"
)

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
	if e.ni == nil {
		return
	}

	actions := e.ni.ContextMenu().Actions()
	for i := 0; i < actions.Len(); i++ {
		action := actions.At(i)
		if menu := action.Menu(); menu != nil {
			menu.Dispose()
		}
		action.Dispose()
	}
	actions.Clear()

	e.addAction("进入 Web 面板", func() { e.sendCommand("OpenWebUI", "") })
	e.addSeparator()

	e.addCheckableAction("系统代理", state.IsProxy, func() { e.sendCommand("ToggleProxy", fmt.Sprintf("%t", !state.IsProxy)) })
	e.addCheckableAction("TUN 模式", state.IsTun, func() { e.sendCommand("ToggleTun", fmt.Sprintf("%t", !state.IsTun)) })

	modeNames := map[string]string{"rule": "规则", "direct": "直连", "global": "全局"}
	currModeName := modeNames[state.Mode]
	if currModeName == "" { currModeName = "未知" }
	modeMenu := e.addSubMenu(fmt.Sprintf("路由模式: %s", currModeName))
	e.addCheckableSubAction(modeMenu, "规则", state.Mode == "rule", func() { e.sendCommand("SwitchMode", "rule") })
	e.addCheckableSubAction(modeMenu, "直连", state.Mode == "direct", func() { e.sendCommand("SwitchMode", "direct") })
	e.addCheckableSubAction(modeMenu, "全局", state.Mode == "global", func() { e.sendCommand("SwitchMode", "global") })

	e.addSeparator()

	switchMenu := e.addSubMenu("切换配置文件")
	if len(state.ProfileItems) == 0 {
		emptyAction := walk.NewAction()
		emptyAction.SetText("暂无配置")
		emptyAction.SetEnabled(false)
		switchMenu.Actions().Add(emptyAction)
	} else {
		for _, item := range state.ProfileItems {
			targetPath := item.Path
			suffix := " (本地)"
			if item.IsRemote { suffix = " (订阅)" }
			e.addCheckableSubAction(switchMenu, item.Name+suffix, item.IsActive, func() {
				e.sendCommand("SwitchProfile", targetPath)
			})
		}
	}
	
	e.addAction("编辑当前配置", func() { e.sendCommand(domain.ActionEditCurrentConfig, "") })
	e.addAction("添加配置", func() { e.sendCommand("OpenProfileManager", "") })

	e.addSeparator()
	
	e.addAction("打开程序目录", func() { e.sendCommand("OpenBaseDir", "") })
	e.addSeparator()

	adminText := "运行权限：普通用户"
	if state.IsAdmin { adminText = "运行权限：管理员" }
	adminMenu := e.addSubMenu(adminText)

	e.addCheckableSubAction(adminMenu, "开机自启（管理员）", state.AutoStart, func() {
		e.sendCommand("ToggleAutoStart", fmt.Sprintf("%t", !state.AutoStart))
	})
	runAdminAction := e.addCheckableSubAction(adminMenu, "始终以管理员身份运行", state.RunAsAdmin || state.AutoStart, func() {
		e.sendCommand("ToggleRunAsAdmin", fmt.Sprintf("%t", !state.RunAsAdmin))
	})
	runAdminAction.SetEnabled(!state.AutoStart)

	moreMenu := e.addSubMenu("更多设置")
	
	e.addActionTo(moreMenu, "复制 Web 访问密码", func() { 
		e.sendCommand(domain.ActionCopyWebUIPassword, "") 
	})
	
	e.addActionTo(moreMenu, "清理 Web 面板缓存", func() {
		go func() {
			if ShowConfirmMessage(nil, "确认清理缓存？", "清理 Web 面板缓存将同时清除本地面板配置（包含布局、主题等），且无法恢复。建议在操作前先导出备份。\n\n是否继续？") {
				e.sendCommand(domain.ActionClearWebUICache, "")
			}
		}()
	})
	
	e.addCheckableSubAction(moreMenu, "使用默认浏览器打开面板", state.UseSystemBrowser, func() {
		e.sendCommand("ToggleSystemBrowser", fmt.Sprintf("%t", !state.UseSystemBrowser))
	})

	e.addActionTo(moreMenu, "-", nil)
	e.addActionTo(moreMenu, "重载当前配置", func() { e.sendCommand("ReloadConfig", "") })
	e.addActionTo(moreMenu, "重启内核", func() { e.sendCommand("RestartKernel", "") })

	e.addSeparator()
	e.addAction("退出程序", func() {
		e.sendCommand("ExitApp", "")
		e.app.Synchronize(func() { e.mw.Close() })
	})
}

func (e *UIEngine) addAction(text string, onTriggered func()) *walk.Action {
	return e.addActionTo(e.ni.ContextMenu(), text, onTriggered)
}

func (e *UIEngine) addActionTo(menu *walk.Menu, text string, onTriggered func()) *walk.Action {
	action := walk.NewAction()
	action.SetText(text)
	action.Triggered().Attach(onTriggered)
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

func (e *UIEngine) addSubMenu(text string) *walk.Menu {
	subMenu, _ := walk.NewMenu()
	subAction := walk.NewMenuAction(subMenu)

	subAction.SetText(text)
	e.ni.ContextMenu().Actions().Add(subAction)
	return subMenu
}

func (e *UIEngine) addSeparator() {
	separator := walk.NewSeparatorAction()
	e.ni.ContextMenu().Actions().Add(separator)
}
