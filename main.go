package main

import (
	"embed"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/getlantern/systray"
	"github.com/go-resty/resty/v2"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
	"golang.org/x/sys/windows/registry"
)

//go:embed icons/*.ico
var iconFs embed.FS

func getIcon(name string) []byte {
	data, _ := iconFs.ReadFile("icons/" + name)
	return data
}

func main() {
	systray.Run(onReady, onExit)
}

func onReady() {
	// 初始化图标
	systray.SetIcon(getIcon("tray_default.ico"))
	systray.SetTooltip("Mihomo Tray")

	mProxy := systray.AddMenuItemCheckbox("系统代理", "", false)
	mTun := systray.AddMenuItemCheckbox("TUN 模式", "", false)
	systray.AddSeparator()
	mService := systray.AddMenuItem("服务管理", "")
	mRestart := systray.AddMenuItem("重启内核", "")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出托盘", "")

	client := resty.New().SetTimeout(2 * time.Second)

	// 逻辑循环
	go func() {
		for {
			// 这里根据你的逻辑调用接口和检测网卡
			// 示例：简单切换图标测试
			resp, err := client.R().Get("http://127.0.0.1:9090/configs")
			if err != nil {
				systray.SetIcon(getIcon("tray_stop.ico"))
			} else if strings.Contains(resp.String(), "true") {
				systray.SetIcon(getIcon("tray_tun.ico"))
			}
			time.Sleep(5 * time.Second)
		}
	}()

	go func() {
		for {
			select {
			case <-mProxy.ClickedCh:
				// 处理代理逻辑
			case <-mService.ClickedCh:
				exec.Command("cmd", "/c", "start", "mihomo\\mihomo-service\\mihomo-service.bat").Start()
			case <-mRestart.ClickedCh:
				exec.Command("taskkill", "/F", "/IM", "mihomo.exe").Run()
			case <-mQuit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

func onExit() {}
