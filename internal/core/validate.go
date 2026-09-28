package core

import (
	"errors"
	"strings"

	"mihomo-tray/internal/sys"
)

func ValidateConfig(exePath, workDir, yamlAbsPath string) error {
	output, err := sys.ExecHidden(workDir, exePath, "-d", ".", "-t", "-f", yamlAbsPath)
	if err == nil {
		return nil
	}

	output = strings.TrimSpace(output)
	errMsg := extractLogMsg(output)

	if errMsg == "" {
		if output != "" {
			errMsg = output
		} else {
			errMsg = err.Error()
		}
	}

	return errors.New(errMsg)
}

func extractLogMsg(output string) string {
	if _, after, ok := strings.Cut(output, "msg="); ok {
		after = strings.TrimSpace(after)
		if strings.HasPrefix(after, `"`) {
			if msg, _, ok := strings.Cut(after[1:], `"`); ok {
				return msg
			}
		}
		
		msg, _, _ := strings.Cut(after, "\n")
		msg = strings.TrimRight(msg, "\r") 
		return strings.TrimSpace(msg)
	}

	if _, after, ok := strings.Cut(output, "level="); ok {
		msg, _, _ := strings.Cut(after, "\n")
		msg = strings.TrimRight(msg, "\r")
		return strings.TrimSpace(msg)
	}

	return ""
}
