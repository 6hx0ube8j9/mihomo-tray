package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"mihomo-tray/internal/app"
	"mihomo-tray/internal/applog"
	"mihomo-tray/internal/config"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
	"mihomo-tray/internal/sys"
	"mihomo-tray/internal/ui"
)

const (
	AppMutex    = "Local\\Mihomo_Tray_Mutex"
	ShowUIEvent = "Local\\Mihomo_Tray_Mutex_ShowUI"
)

func getPermissiveSecAttr() *windows.SecurityAttributes {
	sd, err := windows.SecurityDescriptorFromString("D:(A;;GA;;;WD)S:(ML;;NW;;;LW)")
	if err != nil {
		return nil
	}
	var sa windows.SecurityAttributes
	sa.Length = uint32(unsafe.Sizeof(sa))
	sa.SecurityDescriptor = sd
	return &sa
}

func main() {
	runtime.LockOSThread()

	exePath, err := os.Executable()
	if err != nil {
		return
	}
	baseDir := filepath.Dir(exePath)
	_ = os.Chdir(baseDir)

	isAutostart := false
	isRestarting := false
	for _, arg := range os.Args[1:] {
		argClean := strings.ToLower(strings.TrimLeft(arg, "-"))
		if argClean == "autostart" {
			isAutostart = true
		} else if strings.Contains(argClean, "restarting") {
			isRestarting = true
		}
	}

	sa := getPermissiveSecAttr()
	mName, _ := windows.UTF16PtrFromString(AppMutex)

	var hM windows.Handle
	var isAlreadyExist bool

	maxRetries := 1
	if isRestarting {
		maxRetries = 50
	}

	for i := 0; i < maxRetries; i++ {
		hM, err = windows.CreateMutex(sa, false, mName)
		isAlreadyExist = errors.Is(err, windows.ERROR_ALREADY_EXISTS) ||
			errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
			err == windows.ERROR_ALREADY_EXISTS ||
			err == windows.ERROR_ACCESS_DENIED

		if !isAlreadyExist {
			break
		}
		if hM != 0 {
			_ = windows.CloseHandle(hM)
			hM = 0
		}
		time.Sleep(200 * time.Millisecond)
	}

	if isAlreadyExist {
		if hM != 0 {
			_ = windows.CloseHandle(hM)
		}
		
		eName, _ := windows.UTF16PtrFromString(ShowUIEvent)
		hEvent, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, eName)
		if err == nil && hEvent != 0 {
			
			sys.GrantForegroundPrivilege()
		
			_ = windows.SetEvent(hEvent)
			_ = windows.CloseHandle(hEvent)
			
			time.Sleep(50 * time.Millisecond)
		}
		return
	}

	logWriter := applog.Init(baseDir)
	if logWriter != nil {
		defer logWriter.Close()
	}

	admin := sys.IsAdmin()
	cfgMgr := config.NewManager(baseDir, exePath, admin)
	cfgMgr.LoadAndInitMemory()

	applog.SyncLogLevel(cfgMgr.GetConfig().General.TrayLogLevel)

	slog.Info("程序启动", "pid", os.Getpid(), "dir", baseDir, "admin", admin)

	cfg := cfgMgr.GetConfig()

	osTaskExists := sys.CheckAutoStartStatus(domain.AppTaskName)
	isMine := false
	if osTaskExists {
		isMine = sys.IsTaskPathValid(domain.AppTaskName, exePath)
	}

	cfgAutostart := *cfg.General.Autostart
	finalAutostart := cfgAutostart

	if osTaskExists {
		if isMine {
			if !cfgAutostart {
				slog.Info("自启配置为禁用，清除系统残留任务")
				sys.ToggleAutoStart(domain.AppTaskName, exePath, baseDir, false)
				finalAutostart = false
			}
		} else {
			if cfgAutostart {
				slog.Warn("计划任务指向其他路径，跳过同步")
				finalAutostart = false
			}
		}
	} else {
		if cfgAutostart {
			slog.Info("自启配置为启用，重新注册系统计划任务")
			sys.ToggleAutoStart(domain.AppTaskName, exePath, baseDir, true)
		}
	}

	if cfgAutostart != finalAutostart {
		cfgMgr.Update(func(c *domain.TrayConfig) {
			b := finalAutostart
			c.General.Autostart = &b
		})
	}

	isRunAsAdminConfig := cfg.General.RunAsAdmin
	if !admin && !isAutostart {
		if cfgAutostart || isRunAsAdminConfig {
			slog.Info("准备提权环境")

			if cfgAutostart && osTaskExists && isMine {
				slog.Debug("尝试计划任务静默提权")
				schtasksPath := filepath.Join(os.Getenv("SystemRoot"), "System32", "schtasks.exe")
				cmd := exec.Command(schtasksPath, "/Run", "/TN", domain.AppTaskName)
				cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}

				if err := cmd.Run(); err == nil {
					slog.Info("静默唤起成功，当前实例退出")
					if hM != 0 {
						windows.CloseHandle(hM)
					}
					os.Exit(0)
				} else {
					slog.Warn("静默唤起失败，回退 UAC", "err", err)
				}
			}

			err := sys.RunAsAdmin(exePath, baseDir, "--restarting")
			if sys.IsUserCancelled(err) {
				slog.Info("用户取消提权，程序退出")
				if hM != 0 {
					windows.CloseHandle(hM)
				}
				os.Exit(0)
			} else if err == nil {
				slog.Info("UAC 提权成功，当前实例退出")
				if hM != 0 {
					windows.CloseHandle(hM)
				}
				os.Exit(0)
			} else {
				slog.Error("UAC 启动失败", "err", err)
			}
		}
	}

	if hM != 0 {
		defer windows.CloseHandle(hM)
	}

	eName, _ := windows.UTF16PtrFromString(ShowUIEvent)
	hShowUIEvent, _ := windows.CreateEvent(sa, 0, 0, eName)
	if hShowUIEvent != 0 {
		defer windows.CloseHandle(hShowUIEvent)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runtimeState := state.NewRuntimeState()
	application := app.NewApplication(cfgMgr, runtimeState)
	
	slog.Debug("挂载 UI 引擎")	
	uiEngine := ui.NewEngine(ctx, cancel, application.UICommandCh, application.UIStateCh)
	
	application.SetUIPort(uiEngine)

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(sigCh)
		select {
		case sig := <-sigCh:
			slog.Info("收到系统停止信号", "signal", sig)
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	if hShowUIEvent != 0 {
		go func() {
			slog.Debug("监听进程唤醒事件")
			for {
				s, _ := windows.WaitForSingleObject(hShowUIEvent, windows.INFINITE)
				if s != windows.WAIT_OBJECT_0 || ctx.Err() != nil {
					return
				}
				slog.Info("捕获唤醒信号")
				
				application.UICommandCh <- domain.UICommand{Action: domain.ActionOpenWebUI}
			}
		}()
	}

	slog.Debug("启动后端服务")
	go application.Bootstrap(ctx)

	slog.Debug("进入主线程事件循环")
	if err := uiEngine.Run(); err != nil {
		slog.Error("UI 引擎启动失败", "err", err)
	}

	slog.Debug("UI 循环终止，释放系统资源")
	cancel()
	if hShowUIEvent != 0 {
		_ = windows.SetEvent(hShowUIEvent)
	}

	runtimeState.ForceExitPhase()
	application.SafeShutdown(cancel)
	slog.Info("程序退出完毕")
}
