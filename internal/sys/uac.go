package sys

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

const idYes = 6 // Win32 SDK: #define IDYES 6

func ShowElevationPrompt(title, message string) bool {
	tPtr, _ := windows.UTF16PtrFromString(title)
	mPtr, _ := windows.UTF16PtrFromString(message)
	flags := uint32(windows.MB_YESNO | windows.MB_ICONWARNING | windows.MB_TOPMOST | windows.MB_SETFOREGROUND)

	ret, err := windows.MessageBox(0, mPtr, tPtr, flags)
	if err != nil {
		slog.Warn("显示提权对话框失败", "error", err)
		return false
	}
	return ret == idYes
}

func IsUserCancelled(err error) bool {
	return errors.Is(err, windows.ERROR_CANCELLED)
}

func IsAdmin() bool {
	var token windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil {
		slog.Error("打开进程访问令牌失败", "error", err)
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

func ParseRestartingPID(args []string) uint32 {
	for _, arg := range args {
		argClean := strings.ToLower(strings.TrimLeft(arg, "-"))
		if strings.HasPrefix(argClean, "restarting=") {
			pidStr := strings.TrimPrefix(argClean, "restarting=")
			if pid, err := strconv.ParseUint(pidStr, 10, 32); err == nil {
				return uint32(pid)
			}
		} else if argClean == "restarting" {
			return 1
		}
	}
	return 0
}

func RunAsAdmin(exe, dir string, extraArgs ...string) error {
	verb, _ := windows.UTF16PtrFromString("runas")
	exePtr, _ := windows.UTF16PtrFromString(exe)
	cwdPtr, _ := windows.UTF16PtrFromString(dir)

	var safeArgs []string
	for _, arg := range os.Args[1:] {
		argLower := strings.ToLower(arg)
		if !strings.Contains(argLower, "enable-tun") && !strings.Contains(argLower, "restarting") {
			safeArgs = append(safeArgs, syscall.EscapeArg(arg))
		}
	}

	for _, arg := range extraArgs {
		if strings.TrimSpace(arg) != "" {
			safeArgs = append(safeArgs, syscall.EscapeArg(arg))
		}
	}

	restartingArg := fmt.Sprintf("--restarting=%d", os.Getpid())
	safeArgs = append(safeArgs, syscall.EscapeArg(restartingArg))

	var argsPtr *uint16
	if len(safeArgs) > 0 {
		argsPtr, _ = windows.UTF16PtrFromString(strings.Join(safeArgs, " "))
	}

	err := windows.ShellExecute(0, verb, exePtr, argsPtr, cwdPtr, windows.SW_SHOWNORMAL)

	if err != nil && !IsUserCancelled(err) {
		title, _ := windows.UTF16PtrFromString("需要管理员权限")
		msg, _ := windows.UTF16PtrFromString("未能获取管理员权限。请尝试以管理员身份重新启动程序。")
		flags := uint32(windows.MB_ICONERROR | windows.MB_TOPMOST | windows.MB_SETFOREGROUND)
		_, _ = windows.MessageBox(0, msg, title, flags)
	}

	return err
}
