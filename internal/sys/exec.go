package sys

import (
	"errors"
	"log/slog"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

const mbErrorTopmost = windows.MB_ICONERROR | windows.MB_TOPMOST | windows.MB_SETFOREGROUND

func ExecuteSystemCommand(path string) error {
    pathPtr, err := windows.UTF16PtrFromString(path)
    if err != nil {
        slog.Error("解析路径失败", "err", err)
        return err
    }
	
    slog.Debug("打开目标路径", "path", path)
    return windows.ShellExecute(0, nil, pathPtr, nil, nil, windows.SW_SHOWNORMAL)
}

func RunAsAdmin(exe, dir string) {
	verb, _ := windows.UTF16PtrFromString("runas")
	exePtr, _ := windows.UTF16PtrFromString(exe)
	cwdPtr, _ := windows.UTF16PtrFromString(dir)

	var safeArgs []string
	for _, arg := range os.Args[1:] {
		safeArgs = append(safeArgs, syscall.EscapeArg(arg))
	}

	var argsPtr *uint16
	if len(safeArgs) > 0 {
		argsPtr, _ = windows.UTF16PtrFromString(strings.Join(safeArgs, " "))
	}

	err := windows.ShellExecute(0, verb, exePtr, argsPtr, cwdPtr, windows.SW_SHOWNORMAL)

	if err != nil && !errors.Is(err, windows.ERROR_CANCELLED) {
		title, _ := windows.UTF16PtrFromString("权限请求失败")
		msg, _ := windows.UTF16PtrFromString("TUN 模式需要管理员权限，请授权后运行。")
		_, _ = windows.MessageBox(0, msg, title, mbErrorTopmost)
	}
}
