//go:build windows

package sys

import (
	"errors"

	"golang.org/x/sys/windows"
)

func IsAdmin() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

func IsUserCancelled(err error) bool {
	return errors.Is(err, windows.ERROR_CANCELLED)
}

func RunAsAdmin(exe, dir string, args ...string) error {
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	exePtr, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}

	var cwdPtr *uint16
	if dir != "" {
		cwdPtr, err = windows.UTF16PtrFromString(dir)
		if err != nil {
			return err
		}
	}

	var argsPtr *uint16
	if len(args) > 0 {
		argsPtr, err = windows.UTF16PtrFromString(windows.ComposeCommandLine(args))
		if err != nil {
			return err
		}
	}

	return windows.ShellExecute(0, verb, exePtr, argsPtr, cwdPtr, windows.SW_SHOWNORMAL)
}