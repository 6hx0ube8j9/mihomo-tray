package main

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/getlantern/systray"
	"github.com/go-resty/resty/v2"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

//go:embed icons/*.ico
var iconFs embed.FS

const (
	API_URL    = "http://127.0.0.1:9090"
	PROXY_ADDR = "127.0.0.1:7890"
	LOCK_FILE  = "../tun_on.lock"
	// 直接定义常量，绕过编译器的 undefined 检查
	HKEY_CURRENT_USER = 0x80000001 
)

func getIcon(name string) []byte {
	data, _ := iconFs.ReadFile("icons/" + name)
	return data
}

func main() {
	// 防止多开
	mutexName, _ := windows.UTF16PtrFromString("Global\\MihomoTrayMutex")
	_, err := windows.CreateMutex(nil, false, mutexName)
	if err != nil {
		os.Exit(0)
	}

	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetIcon(getIcon("tray_default.ico"))
	systray.SetTooltip("Mihomo Tray")

	mProxy := systray.AddMenuItemCheckbox("系统代理", "", false)
	mTun := systray.AddMenuItemCheckbox("虚拟网卡 (TUN)", "", false)
	systray.AddSeparator()
	
	mMode := systray.AddMenuItem("分流模式", "")
	mGlobal := mMode.AddSubMenuItemCheckbox("全局模式", "", false)
	mRule := mMode.AddSubMenuItemCheckbox("分流模式", "", false)
	mDirect := mMode.AddSubMenuItemCheckbox("直连模式", "", false)
	
	systray.AddSeparator()
	mRestart := systray.AddMenuItem("重启内核", "")
	mExit := systray.AddMenuItem("退出", "")

	client := resty.New().SetTimeout(1 * time.Second)

	go func() {
		for {
			syncLogic(client, mProxy, mTun, mGlobal, mRule, mDirect)
			time.Sleep(3 * time.Second)
		}
	}()

	go func() {
		for {
			select {
			case <-mProxy.ClickedCh:
				toggleProxy(!mProxy.Checked())
			case <-mTun.ClickedCh:
				v := "true"
				if mTun.Checked() { v = "false" }
				client.R().SetBody(fmt.Sprintf(`{"tun": {"enable": %s}}`, v)).Patch(API_URL + "/configs")
			case <-mGlobal.ClickedCh:
				client.R().SetBody(`{"mode": "global"}`).Patch(API_URL + "/configs")
			case <-mRule.ClickedCh:
				client.R().SetBody(`{"mode": "rule"}`).Patch(API_URL + "/configs")
			case <-mDirect.ClickedCh:
				client.R().SetBody(`{"mode": "direct"}`).Patch(API_URL + "/configs")
			case <-mRestart.ClickedCh:
				restartMihomo()
			case <-mExit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

func syncLogic(c *resty.Client, mProxy, mTun, mG, mR, mD *systray.MenuItem) {
	// 关键：强制类型转换绕过 undefined
	k, err := registry.OpenKey(registry.Key(HKEY_CURRENT_USER), `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err == nil {
		regVal, _, _ := k.GetIntegerValue("ProxyEnable")
		k.Close()
		if regVal == 1 { mProxy.Check() } else { mProxy.Uncheck() }
	}

	resp, err := c.R().Get(API_URL + "/configs")
	if err != nil {
		systray.SetIcon(getIcon("tray_stop.ico"))
		return
	}

	res := resp.String()
	isTun := strings.Contains(res, `"tun":{"enable":true`)
	if isTun {
		mTun.Check()
		_ = os.WriteFile(LOCK_FILE, []byte("ON"), 0644)
		systray.SetIcon(getIcon("tray_tun.ico"))
	} else {
		mTun.Uncheck()
		_ = os.Remove(LOCK_FILE)
		systray.SetIcon(getIcon("tray_default.ico"))
	}

	if strings.Contains(res, `"mode":"global"`) { mG.Check(); mR.Uncheck(); mD.Uncheck() }
	if strings.Contains(res, `"mode":"rule"`) { mG.Uncheck(); mR.Check(); mD.Uncheck() }
	if strings.Contains(res, `"mode":"direct"`) { mG.Uncheck(); mR.Uncheck(); mD.Check() }
}

func toggleProxy(enable bool) {
	k, _, _ := registry.CreateKey(registry.Key(HKEY_CURRENT_USER), `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.ALL_ACCESS)
	if enable {
		_ = k.SetDWordValue("ProxyEnable", 1)
		_ = k.SetStringValue("ProxyServer", PROXY_ADDR)
	} else {
		_ = k.SetDWordValue("ProxyEnable", 0)
	}
	k.Close()

	user32 := windows.NewLazySystemDLL("user32.dll")
	update := user32.NewProc("UpdatePerUserSystemParameters")
	update.Call(0, 0, 0, 0)
}

func restartMihomo() {
	_ = exec.Command("taskkill", "/f", "/t", "/im", "mihomo-run.exe").Run()
	_ = exec.Command("taskkill", "/f", "/t", "/im", "mihomo.exe").Run()
	time.Sleep(1 * time.Second)
	_ = exec.Command("cmd", "/c", "start", "", "..\\mihomo-service.exe", "restart").Start()
}

func onExit() {}
