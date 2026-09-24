package sys

import (
	"io"
	"os/exec"
	"syscall"
)

func WriteToClipboard(text string) error {
	cmd := exec.Command("clip")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	go func() {
		defer in.Close()
		_, _ = io.WriteString(in, text)
	}()
	
	return cmd.Run()
}
