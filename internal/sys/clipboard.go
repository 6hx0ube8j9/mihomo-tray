package sys

import (
	"io"
	"os/exec"
)

func WriteToClipboard(text string) error {
	cmd := exec.Command("clip")
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
