package sys

import (
	"errors"
	"log/slog"
	"os"
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

var (
	modUser32Exec   = windows.NewLazySystemDLL("user32.dll")
	procMessageBoxW = modUser32Exec.NewProc("MessageBoxW")
)

func ShowElevationPrompt(title, message string) bool {
	tPtr, _ := windows.UTF16PtrFromString(title)
	mPtr, _ := windows.UTF16PtrFromString(message)
	style := uintptr(mbYesNo | mbIconWarning | mbTopmost | mbSetForeground)
	ret, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(mPtr)), uintptr(unsafe.Pointer(tPtr)), style)
	return ret == idYes
}

func IsUserCancelled(err error) bool {
	return err == windows.ERROR_CANCELLED
}

func ExecuteSystemCommand(path string) error {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		slog.Error("解析路径失败", "err", err)
		return err
	}
	slog.Debug("打开目标路径", "path", path)
	return windows.ShellExecute(0, nil, pathPtr, nil, nil, windows.SW_SHOWNORMAL)
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
		safeArgs = append(safeArgs, syscall.EscapeArg(arg))
	}

	var argsPtr *uint16
	if len(safeArgs) > 0 {
		argsPtr, _ = windows.UTF16PtrFromString(strings.Join(safeArgs, " "))
	}

	err := windows.ShellExecute(0, verb, exePtr, argsPtr, cwdPtr, windows.SW_SHOWNORMAL)

	if err != nil && !errors.Is(err, windows.ERROR_CANCELLED) {
		title, _ := windows.UTF16PtrFromString("权限请求失败")
		msg, _ := windows.UTF16PtrFromString("操作需要管理员权限，请在 UAC 弹窗中授权运行，或检查系统组策略限制。")
		flags := uint32(windows.MB_ICONERROR | windows.MB_TOPMOST | windows.MB_SETFOREGROUND)
		windows.MessageBox(0, msg, title, flags)
	}

	return err
}
