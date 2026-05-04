package main

import (
	"embed"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/getlantern/systray"
	"github.com/go-resty/resty/v2"
)

//go:embed icons/*.ico
var iconFs embed.FS

const (
	API_URL   = "http://127.0.0.1:9090"
	LOCK_FILE = "tun_on.lock"
)

func getIcon(name string) []byte {
	data, _ := iconFs.ReadFile("icons/" + name)
	return data
}

func main() {
	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetIcon(getIcon("tray_default.ico"))
	systray.SetTooltip("Mihomo Tray")

	mProxy := systray.AddMenuItemCheckbox("系统代理", "", false)
	mTun := systray.AddMenuItemCheckbox("TUN 模式", "", false)
	systray.AddSeparator()
	mService := systray.AddMenuItem("服务管理", "")
	mRestart := systray.AddMenuItem("重启内核", "")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出", "")

	client := resty.New().SetTimeout(2 * time.Second)

	// 核心轮询：纠偏与图标切换
	go func() {
		for {
			syncStatus(client, mProxy, mTun)
			time.Sleep(3 * time.Second)
		}
	}()

	// 菜单点击
	go func() {
		for {
			select {
			case <-mProxy.ClickedCh:
				// 切换逻辑...
			case <-mService.ClickedCh:
				exec.Command("cmd", "/c", "start", "mihomo\\mihomo-service\\mihomo-service.bat").Start()
			case <-mRestart.ClickedCh:
				exec.Command("taskkill", "/F", "/IM", "mihomo.exe").Run()
			case <-mQuit.ClickedCh:
				systray.Quit()
			}
		}
	} ()
}

func syncStatus(c *resty.Client, mProxy, mTun *systray.MenuItem) {
	resp, err := c.R().Get(API_URL + "/configs")
	
	// 1. 内核离线
	if err != nil {
		systray.SetIcon(getIcon("tray_stop.ico"))
		return
	}

	// 2. 简易解析判断 (你可以根据需要解析 JSON)
	isTun := strings.Contains(resp.String(), `"tun":{"enable":true`)
	
	// 3. Lock 文件纠偏
	_, lockErr := os.Stat(LOCK_FILE)
	hasLock := !os.IsNotExist(lockErr)

	if isTun && !hasLock {
		os.Create(LOCK_FILE)
	} else if !isTun && hasLock {
		os.Remove(LOCK_FILE)
	}

	// 4. 更新图标颜色
	if isTun {
		systray.SetIcon(getIcon("tray_tun.ico"))
	} else {
		systray.SetIcon(getIcon("tray_default.ico"))
	}
}

func onExit() {}
