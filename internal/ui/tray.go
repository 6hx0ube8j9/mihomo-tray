package ui

import (
	"fmt"
	"github.com/tailscale/walk"
)

func (e *UIEngine) updateTrayState(state UIState) {
	if e.ni == nil {
		return
	}

	e.ni.ContextMenu().Actions().Clear()

	e.addAction("进入 Web 面板", func() { e.sendCommand("OpenWebUI", "") })
	e.addSeparator()

	e.addCheckableAction("系统代理", state.IsProxy, func() { e.sendCommand("ToggleProxy", fmt.Sprintf("%t", !state.IsProxy)) })
	e.addCheckableAction("TUN 模式", state.IsTun, func() { e.sendCommand("ToggleTun", fmt.Sprintf("%t", !state.IsTun)) })

	modeNames := map[string]string{"rule": "规则", "direct": "直连", "global": "全局"}
	currModeName := modeNames[state.Mode]
	if currModeName == "" {
		currModeName = "未知"
	}
	modeMenu := e.addSubMenu(fmt.Sprintf("路由模式: %s", currModeName))
	e.addCheckableSubAction(modeMenu, "规则", state.Mode == "rule", func() { e.sendCommand("SwitchMode", "rule") })
	e.addCheckableSubAction(modeMenu, "直连", state.Mode == "direct", func() { e.sendCommand("SwitchMode", "direct") })
	e.addCheckableSubAction(modeMenu, "全局", state.Mode == "global", func() { e.sendCommand("SwitchMode", "global") })
	
	e.addSeparator()

	e.addAction("配置面板", func() { e.sendCommand("OpenProfileManager", "") })
	
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
			if item.IsRemote {
				suffix = " (订阅)"
			}
			e.addCheckableSubAction(switchMenu, item.Name+suffix, item.IsActive, func() {
				e.sendCommand("SwitchProfile", targetPath)
			})
		}
	}
	
	e.addSeparator()
	e.addAction("打开程序目录", func() { e.sendCommand("OpenBaseDir", "") })
	e.addSeparator()

	adminText := "运行权限：普通用户"
	if state.IsAdmin {
		adminText = "运行权限：管理员"
	}
	adminMenu := e.addSubMenu(adminText)
	
	e.addCheckableSubAction(adminMenu, "开机自启（管理员）", state.AutoStart, func() {
		e.sendCommand("ToggleAutoStart", fmt.Sprintf("%t", !state.AutoStart))
	})
	runAdminAction := e.addCheckableSubAction(adminMenu, "始终以管理员身份运行", state.RunAsAdmin || state.AutoStart, func() {
		e.sendCommand("ToggleRunAsAdmin", fmt.Sprintf("%t", !state.RunAsAdmin))
	})
	runAdminAction.SetEnabled(!state.AutoStart)

	moreMenu := e.addSubMenu("更多")
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
	subAction := walk.NewAction()
	subAction.SetText(text)
	subAction.SetMenu(subMenu)
	e.ni.ContextMenu().Actions().Add(subAction)
	return subMenu
}

func (e *UIEngine) addSeparator() {
	separator := walk.NewSeparatorAction()
	e.ni.ContextMenu().Actions().Add(separator)
}
