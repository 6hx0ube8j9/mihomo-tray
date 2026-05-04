package main

import (
	"embed"
	"fmt"
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
	API_URL           = "http://127.0.0.1:9090"
	PROXY_ADDR        = "127.0.0.1:7890"
	LOCK_FILE         = "tun_on.lock"
	HKEY_CURRENT_USER = 0x80000001
)

func getIcon(name string) []byte {
	data, _ := iconFs.ReadFile("icons/" + name)
	return data
}

func main() {
	// 1. 防止多开
	mutexName, _ := windows.UTF16PtrFromString("Global\\MihomoTrayMutex")
	_, err := windows.CreateMutex(nil, false, mutexName)
	if err != nil {
		os.Exit(0)
	}

	// 2. 启动时的“权力交接”逻辑
	checkAndStartCore()

	systray.Run(onReady, onExit)
}

func checkAndStartCore() {
	client := resty.New().SetTimeout(500 * time.Millisecond)
	_, err := client.R().Get(API_URL + "/configs")
	if err != nil {
		// API 不通，检查并拉起同目录的 mihomo-run.exe
		exePath, _ := os.Executable()
		corePath := filepath.Join(filepath.Dir(exePath), "mihomo-run.exe")
		if _, err := os.Stat(corePath); err == nil {
			cmd := exec.Command("cmd", "/c", "start", "", corePath)
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			cmd.Start()
		}
	}
}

func onReady() {
	systray.SetIcon(getIcon("tray_default.ico"))

	// 3. 菜单排序：控制面板置顶
	mWeb := systray.AddMenuItem("打开控制面板", "")
	systray.AddSeparator()
	mProxy := systray.AddMenuItemCheckbox("系统代理", "", false)
	mTun := systray.AddMenuItemCheckbox("虚拟网卡 (TUN)", "", false)
	systray.AddSeparator()

	mMode := systray.AddMenuItem("分流模式", "")
	mGlobal := mMode.AddSubMenuItemCheckbox("全局模式", "", false)
	mRule := mMode.AddSubMenuItemCheckbox("分流模式", "", false)
	mDirect := mMode.AddSubMenuItemCheckbox("直连模式", "", false)

	systray.AddSeparator()
	mService := systray.AddMenuItem("管理服务", "")
	mRestart := systray.AddMenuItem("重启内核", "")
	systray.AddSeparator()
	mExit := systray.AddMenuItem("退出托盘", "")

	client := resty.New().SetTimeout(1 * time.Second)

	// 4. 纠偏轮询逻辑
	go func() {
		for {
			syncLogic(client, mProxy, mTun, mGlobal, mRule, mDirect)
			time.Sleep(3 * time.Second)
		}
	}()

	// 5. 菜单操作流转
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
			case <-mWeb.ClickedCh:
				exec.Command("rundll32", "url.dll,FileProtocolHandler", API_URL+"/ui").Start()
			case <-mService.ClickedCh:
				// 路径：\mihomo\mihomo-service\mihomo-service.bat
				exePath, _ := os.Executable()
				serviceBat := filepath.Join(filepath.Dir(exePath), "mihomo-service", "mihomo-service.bat")
				cmd := exec.Command("cmd", "/c", "start", "", serviceBat)
				cmd.Dir = filepath.Dir(serviceBat)
				cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
				cmd.Start()
			case <-mRestart.ClickedCh:
				// 重启逻辑：只管杀，mihomo-run 会自动拉起
				exec.Command("taskkill", "/f", "/t", "/im", "mihomo.exe").Run()
				time.Sleep(1 * time.Second)
			case <-mExit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

func syncLogic(c *resty.Client, mProxy, mTun, mG, mR, mD *systray.MenuItem) {
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

	resp, err := c.R().Get(API_URL + "/configs")
	if err != nil {
		systray.SetIcon(getIcon("tray_stop.ico"))
		return
	}

	res := resp.String()
	isTunOn := strings.Contains(res, `"tun":{"enable":true`)

	// 核心对齐纠偏
	if isTunOn {
		mTun.Check()
		systray.SetIcon(getIcon("tray_tun.ico"))
		if _, err := os.Stat(LOCK_FILE); os.IsNotExist(err) {
			os.Create(LOCK_FILE)
		}
	} else {
		mTun.Uncheck()
		if _, err := os.Stat(LOCK_FILE); err == nil {
			os.Remove(LOCK_FILE)
		}
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

func onExit() {}
