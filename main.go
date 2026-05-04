package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/getlantern/systray"
	"github.com/go-resty/resty/v2"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
	"golang.org/x/sys/windows/registry"
)

const (
	API_URL      = "http://127.0.0.1:9090"
	LOCK_FILE    = "tun_on.lock"
	RUN_EXE      = "mihomo-run.exe"
	SERVICE_BAT  = `mihomo\mihomo-service\mihomo-service.bat`
	IFACE_KEYWORD = "wintun" // 虚拟网卡关键词
)

type ConfigResp struct {
	Tun struct {
		Enable bool `json:"enable"`
	} `json:"tun"`
	Mode string `json:"mode"`
}

func main() {
	systray.Run(onReady, onExit)
}

func onReady() {
	// 初始状态
	systray.SetTemplateIcon(IconDefault, IconDefault)
	systray.SetTooltip("Mihomo Tray - Loading...")

	// 菜单构建
	mProxy := systray.AddMenuItemCheckbox("系统代理", "Toggle System Proxy", false)
	mTun := systray.AddMenuItemCheckbox("TUN 模式", "Toggle TUN Mode", false)
	systray.AddSeparator()
	
	// 模式单选组
	mMode := systray.AddMenuItem("分流模式", "Mode Selection")
	mRule := mMode.AddSubMenuItemCheckbox("规则分流", "", false)
	mGlobal := mMode.AddSubMenuItemCheckbox("全局模式", "", false)
	mDirect := mMode.AddSubMenuItemCheckbox("直连模式", "", false)

	systray.AddSeparator()
	mService := systray.AddMenuItem("服务管理", "运行服务批处理")
	mRestart := systray.AddMenuItem("重启内核", "强制杀掉内核并重连")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出托盘", "仅退出 UI 界面")

	// 核心轮询协程 (3秒一次)
	go func() {
		client := resty.New().SetTimeout(2 * time.Second)
		for {
			syncLogic(client, mProxy, mTun, mRule, mGlobal, mDirect)
			time.Sleep(3 * time.Second)
		}
	}()

	// 菜单处理
	go func() {
		for {
			select {
			case <-mProxy.ClickedCh:
				toggleProxy(mProxy.Checked())
			case <-mTun.ClickedCh:
				sendConfig(map[string]interface{}{"tun": map[string]bool{"enable": !mTun.Checked()}})
			case <-mRule.ClickedCh:
				sendConfig(map[string]string{"mode": "rule"})
			case <-mGlobal.ClickedCh:
				sendConfig(map[string]string{"mode": "global"})
			case <-mDirect.ClickedCh:
				sendConfig(map[string]string{"mode": "direct"})
			case <-mService.ClickedCh:
				exec.Command("cmd", "/c", "start", "", SERVICE_BAT).Start()
			case <-mRestart.ClickedCh:
				killProcess("mihomo.exe")
			case <-mQuit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

// --- 核心逻辑：API + 网卡 -> 对齐纠偏 ---
func syncLogic(c *resty.Client, mProxy, mTun, mRule, mGlobal, mDirect *systray.MenuItem) {
	var cfg ConfigResp
	resp, err := c.R().SetResult(&cfg).Get(API_URL + "/configs")

	// 1. 检查 API 连通性
	if err != nil || resp.StatusCode() != 200 {
		systray.SetIcon(IconStop) // 变红
		checkAndLaunchRunExe()
		return
	}

	// 2. 检查虚拟网卡
	hasTunIface := false
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if strings.Contains(strings.ToLower(iface.Description), IFACE_KEYWORD) {
			hasTunIface = true
			break
		}
	}

	// 3. 纠偏 Lock 文件
	isTunOn := cfg.Tun.Enable || hasTunIface
	if isTunOn && !fileExists(LOCK_FILE) {
		os.Create(LOCK_FILE)
	} else if !isTunOn && fileExists(LOCK_FILE) {
		os.Remove(LOCK_FILE)
	}

	// 4. 更新 UI 勾选状态
	// 系统代理勾选
	regProxy, _ := checkRegistryProxy()
	updateCheck(mProxy, regProxy)
	updateCheck(mTun, isTunOn)
	
	// 模式勾选
	updateCheck(mRule, cfg.Mode == "rule")
	updateCheck(mGlobal, cfg.Mode == "global")
	updateCheck(mDirect, cfg.Mode == "direct")

	// 5. 设置正常图标
	if regProxy || isTunOn {
		systray.SetIcon(IconProxy) // 蓝色或绿色
	} else {
		systray.SetIcon(IconDefault) // 灰色
	}
}

// --- 工具函数 ---

func checkAndLaunchRunExe() {
	found := false
	ps, _ := process.Processes()
	for _, p := range ps {
		name, _ := p.Name()
		if name == RUN_EXE {
			found = true
			break
		}
	}
	if !found {
		exec.Command("cmd", "/c", "start", RUN_EXE).Start()
	}
}

func toggleProxy(current bool) {
	k, _, _ := registry.CreateKey(registry.HKEY_CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.ALL_ACCESS)
	defer k.Close()
	val := uint32(1)
	if current { val = 0 }
	k.SetDWordValue("ProxyEnable", val)
}

func sendConfig(body interface{}) {
	resty.New().R().SetBody(body).Patch(API_URL + "/configs")
}

func updateCheck(m *systray.MenuItem, state bool) {
	if state { m.Check() } else { m.Uncheck() }
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

func killProcess(name string) {
	exec.Command("taskkill", "/F", "/IM", name).Run()
}

func checkRegistryProxy() (bool, error) {
	k, err := registry.OpenKey(registry.HKEY_CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil { return false, err }
	defer k.Close()
	val, _, err := k.GetIntegerValue("ProxyEnable")
	return val == 1, err
}

func onExit() {}
