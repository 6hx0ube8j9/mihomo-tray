package sys

import (
	"log/slog"

	"golang.org/x/sys/windows"
)

func ExecuteSystemCommand(path string) error {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		slog.Error("解析路径失败", "err", err)
		return err
	}
	slog.Debug("调用系统外壳打开路径", "path", path)
	return windows.ShellExecute(0, nil, pathPtr, nil, nil, windows.SW_SHOWNORMAL)
}
