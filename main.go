package main

import (
	"embed"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
	LOCK_FILE  = "tun_on.lock"
	TUN_NAME   = "Mihomo"
)

func getIcon(name string) []byte {
	data, _ := iconFs.ReadFile("icons/" + name)
	return data
}

func main() {
	mutexName, _ := windows.UTF16PtrFromString("Global\\MihomoTrayMutex")
	_, err := windows.CreateMutex(nil, false, mutexName)
	if err != nil { os.Exit(0) }

	checkAndStartCore()
	systray.Run(onReady, onExit)
}

func checkAndStartCore() {
	client := resty.New().SetTimeout(500 * time.Millisecond)
	_, err := client.R().Get(API_URL + "/configs")
	if err != nil {
		exePath, _ := os.Executable()
		corePath := filepath.Join(filepath.Dir(exePath), "mihomo-run.exe")
		// 拉起守护进程
		cmd := exec.Command("cmd", "/c", "start", "", corePath)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		cmd.Start()
	}
}

func onReady() {
	systray.SetIcon(getIcon("tray_default.ico"))

	mWeb := systray.AddMenuItem("打开控制面板", "")
	systray.AddSeparator()

	// 菜单排序：代理和网卡置于模式之上
	mProxy := systray.AddMenuItemCheckbox("系统代理", "", false)
	mTun := systray.AddMenuItemCheckbox("虚拟网卡 (TUN)", "", false)
	systray.AddSeparator()

	mGlobal := systray.AddMenuItemCheckbox("全局模式", "", false)
	mRule := systray.AddMenuItemCheckbox("分流模式", "", false)
	mDirect := systray.AddMenuItemCheckbox("直连模式", "", false)
	systray.AddSeparator()

	mService := systray.AddMenuItem("管理服务", "")
	mRestart := systray.AddMenuItem("重启内核", "")
	systray.AddSeparator()
	mExit := systray.AddMenuItem("退出托盘", "")

	client := resty.New().SetTimeout(1 * time.Second)

	// 状态同步
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
				if mTun.Checked() {
					os.Remove(LOCK_FILE)
					client.R().SetBody(`{"tun": {"enable": false}}`).Patch(API_URL + "/configs")
				} else {
					os.Create(LOCK_FILE)
					client.R().SetBody(`{"tun": {"enable": true}}`).Patch(API_URL + "/configs")
				}
			case <-mGlobal.ClickedCh:
				client.R().SetBody(`{"mode": "global"}`).Patch(API_URL + "/configs")
			case <-mRule.ClickedCh:
				client.R().SetBody(`{"mode": "rule"}`).Patch(API_URL + "/configs")
			case <-mDirect.ClickedCh:
				client.R().SetBody(`{"mode": "direct"}`).Patch(API_URL + "/configs")
			case <-mWeb.ClickedCh:
				exec.Command("rundll32", "url.dll,FileProtocolHandler", API_URL+"/ui").Start()
			case <-mService.ClickedCh:
				// 弹出即关闭：使用 start 开启脚本，主 CMD 进程立刻退出
				exePath, _ := os.Executable()
				serviceBat := filepath.Join(filepath.Dir(exePath), "mihomo-service", "mihomo-service.bat")
				cmd := exec.Command("cmd", "/c", "start", "/b", "", "cmd", "/c", serviceBat)
				cmd.Dir = filepath.Dir(serviceBat)
				cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
				cmd.Start()
			case <-mRestart.ClickedCh:
				// 只杀内核，由守护进程拉起
				exec.Command("taskkill", "/f", "/t", "/im", "mihomo.exe").Run()
				time.Sleep(500 * time.Millisecond)
				checkAndStartCore()
			case <-mExit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

func syncLogic(c *resty.Client, mProxy, mTun, mG, mR, mD *systray.MenuItem) {
	isProxyOn := false
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err == nil {
		regVal, _, _ := k.GetIntegerValue("ProxyEnable")
		k.Close()
		if regVal == 1 { mProxy.Check(); isProxyOn = true } else { mProxy.Uncheck() }
	}

	// 物理网卡检测 (复刻脚本灵魂)
	isPhysicalUp := false
	ifaces, _ := net.Interfaces()
	for _, i := range ifaces {
		if strings.Contains(i.Name, TUN_NAME) && i.Flags&net.FlagUp != 0 {
			isPhysicalUp = true
			break
		}
	}

	resp, err := c.R().Get(API_URL + "/configs")
	if err != nil {
		systray.SetIcon(getIcon("tray_stop.ico"))
		return
	}
	res := resp.String()
	isTunApi := strings.Contains(res, `"tun":{"enable":true`)

	if isTunApi && isPhysicalUp {
		mTun.Check()
		systray.SetIcon(getIcon("tray_tun.ico"))
	} else {
		mTun.Uncheck()
		if isProxyOn {
			systray.SetIcon(getIcon("tray_proxy.ico"))
		} else {
			systray.SetIcon(getIcon("tray_default.ico"))
		}
	}

	if strings.Contains(res, `"mode":"global"`) { mG.Check(); mR.Uncheck(); mD.Uncheck() }
	if strings.Contains(res, `"mode":"rule"`) { mG.Uncheck(); mR.Check(); mD.Uncheck() }
	if strings.Contains(res, `"mode":"direct"`) { mG.Uncheck(); mR.Uncheck(); mD.Check() }
}

func toggleProxy(enable bool) {
	k, _, _ := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.ALL_ACCESS)
	if enable {
		k.SetDWordValue("ProxyEnable", 1)
		k.SetStringValue("ProxyServer", PROXY_ADDR)
	} else {
		k.SetDWordValue("ProxyEnable", 0)
	}
	k.Close()
	user32 := windows.NewLazySystemDLL("user32.dll")
	user32.NewProc("UpdatePerUserSystemParameters").Call(0, 0, 0, 0)
}

func onExit() {}
