package core

import (
	"fmt"
	"strings"

	"mihomo-tray/internal/sys"
)

func ValidateConfig(exePath, workDir, yamlAbsPath string) error {
	output, err := sys.ExecHidden(workDir, exePath, "-d", ".", "-t", "-f", yamlAbsPath)

	if err != nil {
		output = strings.TrimSpace(output)

		errMsg := output
		if idx := strings.Index(output, "msg="); idx != -1 {
			errMsg = output[idx+len("msg="):]
			errMsg = strings.Trim(errMsg, `"'`)
		} else if idx := strings.Index(output, "level="); idx != -1 {
			errMsg = output[idx:]
		}

		if errMsg == "" {
			errMsg = err.Error()
		}

		return fmt.Errorf("沙箱预检失败: %s", errMsg)
	}

	return nil
}
