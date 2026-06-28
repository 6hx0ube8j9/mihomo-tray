package sys

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const taskName = "MihomoTrayTask"

func ToggleAutoStart(exePath, baseDir string, enable bool) bool {
	if key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE); err == nil {
		_ = key.DeleteValue("MihomoTray")
		key.Close()
	}

	if enable {
		safeExePath := strings.ReplaceAll(exePath, "'", "''")
		safeBaseDir := strings.ReplaceAll(baseDir, "'", "''")

		psScript := fmt.Sprintf(
			`$trigger = New-ScheduledTaskTrigger -AtLogOn; $trigger.Delay = 'PT6S'; `+
				`$action = New-ScheduledTaskAction -Execute '%s' -Argument '---autostart' -WorkingDirectory '%s'; `+
				`$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit (New-TimeSpan -Days 0 -Hours 0 -Minutes 0 -Seconds 0) -Priority 4; `+
				`Register-ScheduledTask -TaskName '%s' -Trigger $trigger -Action $action -Settings $settings -RunLevel Highest -Force`,
			safeExePath, safeBaseDir, taskName,
		)
		
		encodedScript := encodeUTF16Base64(psScript)
		cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodedScript)
		cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
		return cmd.Run() == nil
	}

	cmd := exec.Command("schtasks", "/Delete", "/TN", taskName, "/F")
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	
	if err := cmd.Run(); err == nil {
		return true
	}
	return !CheckAutoStartStatus()
}

func CheckAutoStartStatus() bool {
	cmd := exec.Command("schtasks", "/Query", "/TN", taskName)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd.Run() == nil
}

func IsTaskPathValid(currentExePath string) bool {
	cmd := exec.Command("schtasks", "/Query", "/TN", taskName, "/XML")
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.Output()
	if err != nil {
		return false
	}

	utf8Out := out
	if len(out) >= 2 && out[0] == 0xFF && out[1] == 0xFE {
		utf16Vals := make([]uint16, (len(out)-2)/2)
		for i := 0; i < len(utf16Vals); i++ {
			utf16Vals[i] = uint16(out[2+i*2]) | (uint16(out[2+i*2+1]) << 8)
		}
		utf8Out = []byte(string(windows.UTF16ToString(utf16Vals)))
	}

	startTag := []byte("<Command>")
	endTag := []byte("</Command>")
	
	startIdx := bytes.Index(utf8Out, startTag)
	endIdx := bytes.Index(utf8Out, endTag)
	
	if startIdx == -1 || endIdx == -1 || startIdx >= endIdx {
		return false
	}

	registeredPath := string(bytes.TrimSpace(utf8Out[startIdx+len(startTag) : endIdx]))
	registeredPath = strings.Trim(registeredPath, `"`)
	currentExePath = strings.Trim(currentExePath, `"`)
	return strings.EqualFold(registeredPath, currentExePath)
}

func encodeUTF16Base64(s string) string {
	uni := []rune(s)
	b := make([]byte, len(uni)*2)
	for i, v := range uni {
		b[i*2] = byte(v)
		b[i*2+1] = byte(v >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}
