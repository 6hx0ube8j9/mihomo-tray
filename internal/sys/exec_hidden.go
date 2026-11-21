//go:build windows

package sys

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

func ExecHidden(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.SysProcAttr = &windows.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
	}

	out, err := cmd.CombinedOutput()
	return string(out), err
}