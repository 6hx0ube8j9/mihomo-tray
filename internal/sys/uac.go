package sys

import (
	"errors"

	"golang.org/x/sys/windows"
)

func IsAdmin() bool {
	var token windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token)
	if err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

func IsUserCancelled(err error) bool {
	return errors.Is(err, windows.ERROR_CANCELLED)
}

func RunAsAdmin(exe, dir string, args ...string) error {
	verb, _ := windows.UTF16PtrFromString("runas")
	exePtr, _ := windows.UTF16PtrFromString(exe)
	cwdPtr, _ := windows.UTF16PtrFromString(dir)

	var argsPtr *uint16
	if len(args) > 0 {
		argsStr := windows.ComposeCommandLine(args)
		argsPtr, _ = windows.UTF16PtrFromString(argsStr)
	}

	return windows.ShellExecute(0, verb, exePtr, argsPtr, cwdPtr, windows.SW_SHOWNORMAL)
}
