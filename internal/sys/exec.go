package sys

import (
	"errors"
	"log/slog"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

const mbErrorTopmost = windows.MB_ICONERROR | windows.MB_TOPMOST | windows.MB_SETFOREGROUND

func ExecuteSystemCommand(path string) error {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		slog.Error("解析执行路径失败", "err", err)
		return err
	}
	slog.Debug("调用 ShellExecute 打开目标", "path", path)
	return windows.ShellExecute(0, nil, pathPtr, nil, nil, windows.SW_SHOWNORMAL)
}

func RunAsAdmin(exe, dir string) {
	verb, _ := windows.UTF16PtrFromString("runas")
	exePtr, _ := windows.UTF16PtrFromString(exe)
	cwdPtr, _ := windows.UTF16PtrFromString(dir)

	var argsPtr *uint16
	if len(os.Args) > 1 {
		argsPtr, _ = windows.UTF16PtrFromString(strings.Join(os.Args[1:], " "))
	}

	err := windows.ShellExecute(0, verb, exePtr, argsPtr, cwdPtr, windows.SW_SHOWNORMAL)

	if err != nil && !errors.Is(err, windows.ERROR_CANCELLED) {
		title, _ := windows.UTF16PtrFromString("权限请求失败")
		msg, _ := windows.UTF16PtrFromString("TUN 模式及系统网络接管需要管理员权限，请授权后运行。")
		_, _ = windows.MessageBox(0, msg, title, mbErrorTopmost)
	}
}
