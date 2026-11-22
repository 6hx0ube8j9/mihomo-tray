//go:build windows

package sys

import "golang.org/x/sys/windows"

func ExecuteSystemCommand(path string) error {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, nil, pathPtr, nil, nil, windows.SW_SHOWNORMAL)
}
