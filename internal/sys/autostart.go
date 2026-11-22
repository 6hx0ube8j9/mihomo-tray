//go:build windows

package sys

import (
	"encoding/xml"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const cpOEMCP = 1

var procMultiByteToWideChar = modKernel32.NewProc("MultiByteToWideChar")

func getSchtasksPath() string {
	sysRoot := os.Getenv("SystemRoot")
	if sysRoot == "" {
		sysRoot = `C:\Windows`
	}
	return filepath.Join(sysRoot, "System32", "schtasks.exe")
}

func newSchtasksCmd(args ...string) *exec.Cmd {
	cmd := exec.Command(getSchtasksPath(), args...)
	cmd.SysProcAttr = &windows.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	return cmd
}

func ToggleAutoStart(appName, exePath, baseDir string, enable bool) error {
	if key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE); err == nil {
		_ = key.DeleteValue(appName)
		_ = key.Close()
	}

	if enable {
		absExe, err := filepath.Abs(exePath)
		if err != nil {
			absExe = filepath.Clean(exePath)
		}
		absBase, err := filepath.Abs(baseDir)
		if err != nil {
			absBase = filepath.Clean(baseDir)
		}

		xmlContent := generateTaskXML(absExe, "--autostart", absBase)
		tempXML, err := writeTempTaskXML(appName, xmlContent)
		if err != nil {
			return fmt.Errorf("写入计划任务临时文件失败: %w", err)
		}
		defer os.Remove(tempXML)

		cmd := newSchtasksCmd("/Create", "/TN", appName, "/XML", tempXML, "/F")
		if out, err := cmd.CombinedOutput(); err != nil {
			if msg := strings.TrimSpace(decodeConsole(out)); msg != "" {
				return fmt.Errorf("导入计划任务失败: %s", msg)
			}
			return fmt.Errorf("导入计划任务失败: %w", err)
		}
		return nil
	}

	cmd := newSchtasksCmd("/Delete", "/TN", appName, "/F")
	if out, err := cmd.CombinedOutput(); err != nil && CheckAutoStartStatus(appName) {
		if msg := strings.TrimSpace(decodeConsole(out)); msg != "" {
			return fmt.Errorf("删除计划任务失败: %s", msg)
		}
		return fmt.Errorf("删除计划任务失败: %w", err)
	}
	return nil
}

func CheckAutoStartStatus(appName string) bool {
	return newSchtasksCmd("/Query", "/TN", appName).Run() == nil
}

func QueryAutoStartTask(appName string) (exePath string, exists bool) {
	out, err := newSchtasksCmd("/Query", "/TN", appName, "/XML").Output()
	if err != nil {
		return "", false
	}

	content := decodeConsole(out)
	const (
		startTag = "<Command>"
		endTag   = "</Command>"
	)

	startIdx := strings.Index(content, startTag)
	endIdx := strings.Index(content, endTag)
	if startIdx == -1 || endIdx == -1 || startIdx >= endIdx {
		return "", true
	}

	rawCommand := strings.TrimSpace(content[startIdx+len(startTag) : endIdx])
	registeredPath := strings.Trim(html.UnescapeString(rawCommand), `"`)
	if registeredPath == "" {
		return "", true
	}
	return filepath.Clean(registeredPath), true
}

func RunScheduledTask(taskName string) error {
	return newSchtasksCmd("/Run", "/TN", taskName).Run()
}

func decodeConsole(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		u16 := make([]uint16, (len(b)-2)/2)
		for i := range u16 {
			u16[i] = uint16(b[2+i*2]) | uint16(b[2+i*2+1])<<8
		}
		return string(utf16.Decode(u16))
	}
	if utf8.Valid(b) {
		return string(b)
	}

	n, _, _ := procMultiByteToWideChar.Call(cpOEMCP, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 0, 0)
	if n == 0 {
		return string(b)
	}

	buf := make([]uint16, n)
	_, _, _ = procMultiByteToWideChar.Call(cpOEMCP, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(unsafe.Pointer(&buf[0])), n)
	return windows.UTF16ToString(buf)
}

func generateTaskXML(exePath, args, baseDir string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
      <Delay>PT6S</Delay>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>HighestAvailable</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>Parallel</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>4</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
      <WorkingDirectory>%s</WorkingDirectory>
    </Exec>
  </Actions>
</Task>`, escapeXML(exePath), escapeXML(args), escapeXML(baseDir))
}

func escapeXML(s string) string {
	var buf strings.Builder
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func writeTempTaskXML(appName, content string) (string, error) {
	f, err := os.CreateTemp("", appName+"-*.xml")
	if err != nil {
		return "", err
	}
	path := f.Name()

	runes := utf16.Encode([]rune(content))
	b := make([]byte, 2+len(runes)*2)
	b[0] = 0xFF
	b[1] = 0xFE
	for i, r := range runes {
		b[2+i*2] = byte(r)
		b[2+i*2+1] = byte(r >> 8)
	}

	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}
