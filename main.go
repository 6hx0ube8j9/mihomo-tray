package main

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/getlantern/systray"
	"github.com/go-resty/resty/v2"
	"golang.org/x/sys/windows/registry"
)

//go:embed icons/*.ico
var iconFs embed.FS

const (
	API_URL      = "http://127.0.0.1:9090"
	PROXY_ADDR   = "127.0.0.1:7890"
	LOCK_FILE    = "../tun_on.lock"
	WINDOW_TITLE = "MihomoTrayInstance"
)

// WinAPI 用于刷新系统设置
var (
	user32           = syscall.NewLazyDLL("user32.dll")
	updateParameters = user32.NewProc("UpdatePerUserSystemParameters")
)

func getIcon(name string) []byte {
	data, _ := iconFs.ReadFile("icons/" + name)
	return data
}

func main() {
	// --- 防止多开逻辑 ---
	_, err := syscall.CreateMutex(nil, false, syscall.StringToUTF16Ptr("Global\\MihomoTrayMutex"))
	if err != nil {
		os.Exit(0)
	}

	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetIcon(getIcon("tray_default.ico"))
	
	// 菜单项
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

	// 核心同步协程
	go func() {
		for {
			syncLogic(client, mProxy, mTun, mGlobal, mRule, mDirect)
			time.Sleep(3 * time.Second)
		}
	}()

	// 动作处理
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
	resp, err := c.R().Get(API_URL + "/configs")
	
	// 系统代理注册表检查
	k, _ := registry.OpenKey(registry.HKEY_CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	regVal, _, _ := k.GetIntegerValue("ProxyEnable")
	k.Close()
	if regVal == 1 { mProxy.Check() } else { mProxy.Uncheck() }

	if err != nil {
		systray.SetIcon(getIcon("tray_stop.ico"))
		return
	}

	res := resp.String()
	// TUN 状态对齐与 Lock 文件 (复刻脚本逻辑)
	isTun := strings.Contains(res, `"tun":{"enable":true`)
	if isTun {
		mTun.Check()
		os.WriteFile(LOCK_FILE, []byte("ON"), 0644)
		systray.SetIcon(getIcon("tray_tun.ico"))
	} else {
		mTun.Uncheck()
		os.Remove(LOCK_FILE)
		systray.SetIcon(getIcon("tray_default.ico"))
	}

	// 模式对齐
	if strings.Contains(res, `"mode":"global"`) { mG.Check(); mR.Uncheck(); mD.Uncheck() }
	if strings.Contains(res, `"mode":"rule"`) { mG.Uncheck(); mR.Check(); mD.Uncheck() }
	if strings.Contains(res, `"mode":"direct"`) { mG.Uncheck(); mR.Uncheck(); mD.Check() }
}

func toggleProxy(enable bool) {
	k, _, _ := registry.CreateKey(registry.HKEY_CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.ALL_ACCESS)
	defer k.Close()
	if enable {
		k.SetDWordValue("ProxyEnable", 1)
		k.SetStringValue("ProxyServer", PROXY_ADDR)
	} else {
		k.SetDWordValue("ProxyEnable", 0)
	}
	// 关键：通知系统刷新代理设置
	updateParameters.Call(0, 0, 0, 0)
}

func restartMihomo() {
	exec.Command("taskkill", "/f", "/t", "/im", "mihomo-run.exe").Run()
	exec.Command("taskkill", "/f", "/t", "/im", "mihomo.exe").Run()
	time.Sleep(1 * time.Second)
	// 调用你上层目录的服务管理
	exec.Command("cmd", "/c", "start", "", "..\\mihomo-service.exe", "restart").Start()
}

func onExit() {}
