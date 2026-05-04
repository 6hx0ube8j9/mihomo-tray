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
	API_URL           = "http://127.0.0.1:9090"
	PROXY_ADDR        = "127.0.0.1:7890"
	LOCK_FILE         = "../tun_on.lock"
	HKEY_CURRENT_USER = 0x80000001
)

func getIcon(name string) []byte {
	data, _ := iconFs.ReadFile("icons/" + name)
	return data
}

func main() {
	mutexName, _ := windows.UTF16PtrFromString("Global\\MihomoTrayMutex")
	_, err := windows.CreateMutex(nil, false, mutexName)
	if err != nil { os.Exit(0) }

	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetIcon(getIcon("tray_default.ico"))

	// --- 菜单定义 ---
	mProxy := systray.AddMenuItemCheckbox("系统代理", "", false)
	mTun := systray.AddMenuItemCheckbox("虚拟网卡 (TUN)", "", false)
	systray.AddSeparator()
	
	mMode := systray.AddMenuItem("分流模式", "")
	mGlobal := mMode.AddSubMenuItemCheckbox("全局模式", "", false)
	mRule := mMode.AddSubMenuItemCheckbox("分流模式", "", false)
	mDirect := mMode.AddSubMenuItemCheckbox("直连模式", "", false)
	
	systray.AddSeparator()
	mWeb := systray.AddMenuItem("打开 Web 面板", "")      // 补全 Web 面板
	mService := systray.AddMenuItem("服务管理", "")      // 补全服务管理
	mRestart := systray.AddMenuItem("重启内核", "")
	systray.AddSeparator()
	mExit := systray.AddMenuItem("退出托盘", "")

	client := resty.New().SetTimeout(1 * time.Second)

	// 核心同步逻辑
	go func() {
		for {
			syncLogic(client, mProxy, mTun, mGlobal, mRule, mDirect)
			time.Sleep(2 * time.Second)
		}
	}()

	// 监听操作
	go func() {
		for {
			select {
			case <-mProxy.ClickedCh:
				toggleProxy(!mProxy.Checked())
			case <-mTun.ClickedCh:
				v := "true"; if mTun.Checked() { v = "false" }
				client.R().SetBody(fmt.Sprintf(`{"tun": {"enable": %s}}`, v)).Patch(API_URL + "/configs")
			case <-mGlobal.ClickedCh:
				client.R().SetBody(`{"mode": "global"}`).Patch(API_URL + "/configs")
			case <-mRule.ClickedCh:
				client.R().SetBody(`{"mode": "rule"}`).Patch(API_URL + "/configs")
			case <-mDirect.ClickedCh:
				client.R().SetBody(`{"mode": "direct"}`).Patch(API_URL + "/configs")
			case <-mWeb.ClickedCh:
				exec.Command("cmd", "/c", "start", API_URL+"/ui").Start()
			case <-mService.ClickedCh:
				exec.Command("cmd", "/c", "start", "", "..\\mihomo-service.exe").Start()
			case <-mRestart.ClickedCh:
				restartMihomo()
			case <-mExit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

func syncLogic(c *resty.Client, mProxy, mTun, mG, mR, mD *systray.MenuItem) {
	// 1. 检查注册表代理状态
	isProxyOn := false
	k, err := registry.OpenKey(registry.Key(HKEY_CURRENT_USER), `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err == nil {
		regVal, _, _ := k.GetIntegerValue("ProxyEnable")
		k.Close()
		if regVal == 1 { 
			mProxy.Check()
			isProxyOn = true
		} else { 
			mProxy.Uncheck() 
		}
	}

	// 2. 检查内核状态
	resp, err := c.R().Get(API_URL + "/configs")
	if err != nil {
		systray.SetIcon(getIcon("tray_stop.ico"))
		return
	}

	res := resp.String()
	isTunOn := strings.Contains(res, `"tun":{"enable":true`)
	
	// 3. 图标切换逻辑优化
	if isTunOn {
		mTun.Check()
		systray.SetIcon(getIcon("tray_tun.ico"))
		_ = os.WriteFile(LOCK_FILE, []byte("ON"), 0644)
	} else if isProxyOn {
		mTun.Uncheck()
		systray.SetIcon(getIcon("tray_proxy.ico")) // 代理开启且非 TUN 时显示蓝色
		_ = os.Remove(LOCK_FILE)
	} else {
		mTun.Uncheck()
		systray.SetIcon(getIcon("tray_default.ico"))
		_ = os.Remove(LOCK_FILE)
	}

	// 4. 模式勾选
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
	// 刷新系统代理
	user32 := windows.NewLazySystemDLL("user32.dll")
	update := user32.NewProc("UpdatePerUserSystemParameters")
	update.Call(0, 0, 0, 0)
}

func restartMihomo() {
	_ = exec.Command("taskkill", "/f", "/t", "/im", "mihomo-run.exe").Run()
	_ = exec.Command("taskkill", "/f", "/t", "/im", "mihomo.exe").Run()
	time.Sleep(1 * time.Second)
	// 适配你之前的服务管理路径
	_ = exec.Command("cmd", "/c", "start", "", "..\\mihomo-service.exe", "restart").Start()
}

func onExit() {}
