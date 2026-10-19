package sys

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	mbYesNo         = 0x00000004
	mbIconWarning   = 0x00000030
	mbTopmost       = 0x00040000
	mbSetForeground = 0x00010000
	idYes           = 6
)

func ShowElevationPrompt(title, message string) bool {
	tPtr, _ := windows.UTF16PtrFromString(title)
	mPtr, _ := windows.UTF16PtrFromString(message)
	style := uintptr(mbYesNo | mbIconWarning | mbTopmost | mbSetForeground)
	ret, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(mPtr)), uintptr(unsafe.Pointer(tPtr)), style)
	return ret == idYes
}

func IsUserCancelled(err error) bool {
	return errors.Is(err, windows.ERROR_CANCELLED)
}

func IsAdmin() bool {
	var token windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil {
		slog.Error("获取进程 Token 失败", "err", err)
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
		title, _ := windows.UTF16PtrFromString("权限请求失败")
		msg, _ := windows.UTF16PtrFromString("操作需要管理员权限，请在 UAC 弹窗中授权运行，或检查系统组策略限制。")
		flags := uint32(windows.MB_ICONERROR | windows.MB_TOPMOST | windows.MB_SETFOREGROUND)
		windows.MessageBox(0, msg, title, flags)
	}

	return err
}
