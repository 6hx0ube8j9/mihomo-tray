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
	TUN_NAME   = "Mihomo" // 必须与你 config.yaml 中的 device-name 一致
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
		cmd := exec.Command("cmd", "/c", "start", "", corePath)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		cmd.Start()
	}
}

func onReady() {
	systray.SetIcon(getIcon("tray_default.ico"))

	mWeb := systray.AddMenuItem("打开控制面板", "")
	systray.AddSeparator()
	
	// 3. 模式扁平化：直接放在主菜单
	mGlobal := systray.AddMenuItemCheckbox("全局模式", "", false)
	mRule := systray.AddMenuItemCheckbox("分流模式", "", false)
	mDirect := systray.AddMenuItemCheckbox("直连模式", "", false)
	systray.AddSeparator()

	mProxy := systray.AddMenuItemCheckbox("系统代理", "", false)
	mTun := systray.AddMenuItemCheckbox("虚拟网卡 (TUN)", "", false)
	systray.AddSeparator()
	
	mService := systray.AddMenuItem("管理服务 (脚本)", "")
	mRestart := systray.AddMenuItem("重启内核", "")
	systray.AddSeparator()
	mExit := systray.AddMenuItem("退出托盘", "")

	client := resty.New().SetTimeout(1 * time.Second)

	// 1 & 2. 状态同步与网卡检测轮询
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
				v := "true"; if mTun.Checked() { v = "false" }
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
				// 4. 管理服务脚本：带工作目录启动
				exePath, _ := os.Executable()
				serviceBat := filepath.Join(filepath.Dir(exePath), "mihomo-service", "mihomo-service.bat")
				cmd := exec.Command("cmd", "/c", "start", "", serviceBat)
				cmd.Dir = filepath.Dir(serviceBat)
				cmd.Start()
			case <-mRestart.ClickedCh:
				// 5. 重启内核重构逻辑
				restartCore(mTun.Checked())
			case <-mExit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

func isInterfaceUp(name string) bool {
	ifaces, err := net.Interfaces()
	if err != nil { return false }
	for _, i := range ifaces {
		if strings.Contains(i.Name, name) {
			return i.Flags&net.FlagUp != 0
		}
	}
	return false
}

func syncLogic(c *resty.Client, mProxy, mTun, mG, mR, mD *systray.MenuItem) {
	// 系统代理检测
	isProxyOn := false
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err == nil {
		regVal, _, _ := k.GetIntegerValue("ProxyEnable")
		k.Close()
		if regVal == 1 { mProxy.Check(); isProxyOn = true } else { mProxy.Uncheck() }
	}

	// API 状态获取
	resp, err := c.R().Get(API_URL + "/configs")
	if err != nil {
		systray.SetIcon(getIcon("tray_stop.ico"))
		return
	}

	res := resp.String()
	isTunInApi := strings.Contains(res, `"tun":{"enable":true`)
	// 1. 物理网卡检测
	isPhysicalUp := isInterfaceUp(TUN_NAME)

	// 综合判定 TUN 状态
	if isTunInApi && isPhysicalUp {
		mTun.Check()
		systray.SetIcon(getIcon("tray_tun.ico"))
		if _, err := os.Stat(LOCK_FILE); os.IsNotExist(err) { os.Create(LOCK_FILE) }
	} else {
		mTun.Uncheck()
		if !isTunInApi { // 只有 API 说没开，才删锁，防止网卡抖动导致删锁
			if _, err := os.Stat(LOCK_FILE); err == nil { os.Remove(LOCK_FILE) }
		}
		if isProxyOn { systray.SetIcon(getIcon("tray_proxy.ico")) } else { systray.SetIcon(getIcon("tray_default.ico")) }
	}

	// 模式单选同步
	if strings.Contains(res, `"mode":"global"`) { mG.Check(); mR.Uncheck(); mD.Uncheck() }
	if strings.Contains(res, `"mode":"rule"`) { mG.Uncheck(); mR.Check(); mD.Uncheck() }
	if strings.Contains(res, `"mode":"direct"`) { mG.Uncheck(); mR.Uncheck(); mD.Check() }
}

func restartCore(shouldStayTun bool) {
	// 1. 杀掉内核
	exec.Command("taskkill", "/f", "/t", "/im", "mihomo.exe").Run()
	
	// 2. 状态重置：如果之前是开启 TUN 的，为了稳妥，先删锁再补锁
	os.Remove(LOCK_FILE)
	time.Sleep(500 * time.Millisecond)
	
	if shouldStayTun {
		os.Create(LOCK_FILE)
	}
	
	// 3. 唤醒守护进程 (如果它不小心被杀了)
	exePath, _ := os.Executable()
	corePath := filepath.Join(filepath.Dir(exePath), "mihomo-run.exe")
	exec.Command("cmd", "/c", "start", "", corePath).Start()
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
