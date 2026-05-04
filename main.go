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
	if err != nil {
		os.Exit(0)
	}
	systray.Run(onReady, onExit)
}

// 解决黑框闪过的执行函数
func runCommandSilent(cmdPath string, args ...string) {
	cmd := exec.Command(cmdPath, args...)
	// 关键：隐藏 CMD 窗口
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Start()
}

func onReady() {
	systray.SetIcon(getIcon("tray_default.ico"))

	// --- 菜单重新排序 ---
	mWeb := systray.AddMenuItem("打开控制面板", "") // 移至顶部
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

	go func() {
		for {
			syncLogic(client, mProxy, mTun, mGlobal, mRule, mDirect)
			time.Sleep(2 * time.Second)
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
			case <-mWeb.ClickedCh:
				// 使用默认浏览器打开，无黑框
				exec.Command("rundll32", "url.dll,FileProtocolHandler", API_URL+"/ui").Start()
			case <-mService.ClickedCh:
				// 修复路径逻辑：获取 EXE 所在目录的上级目录
				basePath, _ := os.Executable()
				parentDir := filepath.Dir(filepath.Dir(basePath))
				serviceExe := filepath.Join(parentDir, "mihomo-service.exe")
				
				cmd := exec.Command("cmd", "/c", "start", "", serviceExe)
				cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
				cmd.Start()
			case <-mRestart.ClickedCh:
				restartMihomo()
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

	if isTunOn {
		mTun.Check()
		systray.SetIcon(getIcon("tray_tun.ico"))
		_ = os.WriteFile(LOCK_FILE, []byte("ON"), 0644)
	} else if isProxyOn {
		mTun.Uncheck()
		systray.SetIcon(getIcon("tray_proxy.ico"))
		_ = os.Remove(LOCK_FILE)
	} else {
		mTun.Uncheck()
		systray.SetIcon(getIcon("tray_default.ico"))
		_ = os.Remove(LOCK_FILE)
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
	// 静默杀进程
	runCommandSilent("taskkill", "/f", "/t", "/im", "mihomo-run.exe")
	runCommandSilent("taskkill", "/f", "/t", "/im", "mihomo.exe")
	time.Sleep(1 * time.Second)
	
	basePath, _ := os.Executable()
	parentDir := filepath.Dir(filepath.Dir(basePath))
	serviceExe := filepath.Join(parentDir, "mihomo-service.exe")
	
	cmd := exec.Command("cmd", "/c", "start", "", serviceExe, "restart")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Start()
}

func onExit() {}
