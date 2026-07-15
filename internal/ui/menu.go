package ui

import (
	"context"
	"embed"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/energye/systray"
)

//go:embed icons/*.ico
var iconFs embed.FS

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
	ErrorMsg  string
}

type TrayMenu struct {
	ctx       context.Context
	cancel    context.CancelFunc
	commandCh chan<- UICommand
	stateCh   <-chan UIState

	mTun      *systray.MenuItem
	mProxy    *systray.MenuItem
	mModeRoot *systray.MenuItem
	mModes    map[string]*systray.MenuItem
	mAuto     *systray.MenuItem

	lastClick time.Time
	clickMu   sync.Mutex

	wasPanic bool
}

func NewTrayMenu(ctx context.Context, cancel context.CancelFunc, cmdCh chan<- UICommand, stCh <-chan UIState) *TrayMenu {
	return &TrayMenu{
		ctx:       ctx,
		cancel:    cancel,
		commandCh: cmdCh,
		stateCh:   stCh,
		mModes:    make(map[string]*systray.MenuItem),
	}
}

func (tm *TrayMenu) sendCommand(action, payload string) {
	tm.clickMu.Lock()
	if time.Since(tm.lastClick) < 300*time.Millisecond {
		tm.clickMu.Unlock()
		return
	}
	tm.lastClick = time.Now()
	tm.clickMu.Unlock()
	select {
	case tm.commandCh <- UICommand{Action: action, Payload: payload}:
	default:
	}
}

func (tm *TrayMenu) Setup() {
	tm.UpdateIcon("stop.ico")
	systray.SetTooltip("MihomoTray")
	systray.SetOnClick(func(_ systray.IMenu) {
		tm.sendCommand("OpenWebUI", "")
	})

	mWeb := systray.AddMenuItem("进入 Web 面板", "")
	mWeb.Click(func() { tm.sendCommand("OpenWebUI", "") })
	systray.AddSeparator()

	tm.mProxy = systray.AddMenuItemCheckbox("系统代理", "", false)
	tm.mProxy.Click(func() {
		tm.sendCommand("ToggleProxy", strconv.FormatBool(!tm.mProxy.Checked()))
	})

	tm.mTun = systray.AddMenuItemCheckbox("虚拟网卡 (TUN)", "", false)
	tm.mTun.Click(func() {
		tm.sendCommand("ToggleTun", strconv.FormatBool(!tm.mTun.Checked()))
	})
	systray.AddSeparator()

	tm.mModeRoot = systray.AddMenuItem("当前模式: 规则 ", "")

	setupMode := func(key, label string) {
		tm.mModes[key] = tm.mModeRoot.AddSubMenuItemCheckbox(label, "", false)
		tm.mModes[key].Click(func() {
			tm.sendCommand("SwitchMode", key)
		})
	}

	setupMode("rule", "规则")
	setupMode("direct", "直连")
	setupMode("global", "全局")
	systray.AddSeparator()

	mDir := systray.AddMenuItem("打开程序目录", "")
	mDir.Click(func() {
		tm.sendCommand("OpenBaseDir", "")
	})
	mMoreRoot := systray.AddMenuItem("更多", "")
	tm.mAuto = mMoreRoot.AddSubMenuItemCheckbox("开机启动", "", false)
	tm.mAuto.Click(func() {
		tm.sendCommand("ToggleAutoStart", strconv.FormatBool(!tm.mAuto.Checked()))
	})

	mReload := mMoreRoot.AddSubMenuItem("重载配置文件", "")
	mReload.Click(func() { tm.sendCommand("ReloadConfig", "") })

	mRestart := mMoreRoot.AddSubMenuItem("重启核心", "")
	mRestart.Click(func() { tm.sendCommand("RestartKernel", "") })

	mEdit := mMoreRoot.AddSubMenuItem("编辑 config.yaml", "")
	mEdit.Click(func() { tm.sendCommand("OpenConfigFile", "") })

	systray.AddSeparator()
	mExit := systray.AddMenuItem("退出程序", "")
	mExit.Click(func() {
		tm.sendCommand("ExitApp", "")
		systray.Quit()
	})

	go tm.ListenUIState()
}

func (tm *TrayMenu) ListenUIState() {
	for {
		select {
		case <-tm.ctx.Done():
			return
		case state := <-tm.stateCh:
			tm.render(state)
		}
	}
}

func (tm *TrayMenu) render(state UIState) {
	files := []string{"stop.ico", "error.ico", "tun.ico", "proxy.ico", "default.ico"}
	if state.IconState >= 0 && state.IconState < len(files) {
		tm.UpdateIcon(files[state.IconState])
	}

	if state.IconState == 1 {
		if !tm.wasPanic {
			tm.wasPanic = true
			go showWindowsAlert("内核异常提示", state.ErrorMsg)
		}
	} else {
		tm.wasPanic = false
	}

	if state.IsProxy && !tm.mProxy.Checked() {
		tm.mProxy.Check()
	} else if !state.IsProxy && tm.mProxy.Checked() {
		tm.mProxy.Uncheck()
	}

	if state.IsTun && !tm.mTun.Checked() {
		tm.mTun.Check()
	} else if !state.IsTun && tm.mTun.Checked() {
		tm.mTun.Uncheck()
	}

	if state.AutoStart && !tm.mAuto.Checked() {
		tm.mAuto.Check()
	} else if !state.AutoStart && tm.mAuto.Checked() {
		tm.mAuto.Uncheck()
	}

	modeNames := map[string]string{
		"rule":   "规则",
		"direct": "直连",
		"global": "全局",
	}
	if name, exists := modeNames[state.Mode]; exists {
		tm.mModeRoot.SetTitle("当前模式: " + name + " ")
	}

	for k, m := range tm.mModes {
		if k == state.Mode {
			if !m.Checked() {
				m.Check()
			}
		} else {
			if m.Checked() {
				m.Uncheck()
			}
		}
	}
}

func (tm *TrayMenu) UpdateIcon(filename string) {
	if b, err := iconFs.ReadFile("icons/" + filename); err == nil {
		systray.SetIcon(b)
	}
}

func showWindowsAlert(title, message string) {
	if runtime.GOOS == "windows" {
		user32 := syscall.NewLazyDLL("user32.dll")
		messageBoxW := user32.NewProc("MessageBoxW")

		lpText, _ := syscall.UTF16PtrFromString(message)
		lpCaption, _ := syscall.UTF16PtrFromString(title)
		_, _, _ = messageBoxW.Call(0, uintptr(unsafe.Pointer(lpText)), uintptr(unsafe.Pointer(lpCaption)), 0x00040030)
	}
}
