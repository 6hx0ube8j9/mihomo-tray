//go:build windows

package sys

import (
	"encoding/xml"
	"fmt"
	"html"
	"log/slog"
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

var (
	kernel32DLL             = windows.NewLazySystemDLL("kernel32.dll")
	procMultiByteToWideChar = kernel32DLL.NewProc("MultiByteToWideChar")
)

func getSchtasksPath() string {
	sysRoot := os.Getenv("SystemRoot")
	if sysRoot == "" {
		sysRoot = `C:\Windows`
	}
	return filepath.Join(sysRoot, "System32", "schtasks.exe")
}

func ToggleAutoStart(appName, exePath, baseDir string, enable bool) bool {
	if key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE); err == nil {
		_ = key.DeleteValue(appName)
		key.Close()
	}

	schtasksPath := getSchtasksPath()

	if enable {
		slog.Debug("注册自启计划任务", "task", appName)

		absExe, _ := filepath.Abs(exePath)
		absBase, _ := filepath.Abs(baseDir)

		xmlContent := generateTaskXML(absExe, "--autostart", absBase)
		tempXML := filepath.Join(os.TempDir(), fmt.Sprintf("%s.xml", appName))

		if err := writeUTF16LE(tempXML, xmlContent); err != nil {
			slog.Error("写入计划任务临时文件失败", "err", err)
			return false
		}
		defer os.Remove(tempXML)

		cmd := exec.Command(schtasksPath, "/Create", "/TN", appName, "/XML", tempXML, "/F")
		cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}

		if out, err := cmd.CombinedOutput(); err != nil {
			slog.Error("导入计划任务失败", "err", err, "output", strings.TrimSpace(decodeConsole(out)))
			return false
		}
		return true
	}

	slog.Debug("删除自启计划任务", "task", appName)
	cmd := exec.Command(schtasksPath, "/Delete", "/TN", appName, "/F")
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}

	if err := cmd.Run(); err != nil {
		slog.Debug("删除计划任务失败", "task", appName, "err", err)
	}
	return !CheckAutoStartStatus(appName)
}

func CheckAutoStartStatus(appName string) bool {
	cmd := exec.Command(getSchtasksPath(), "/Query", "/TN", appName)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd.Run() == nil
}

func CheckPathExists(path string) bool {
	clean := strings.Trim(strings.TrimSpace(path), `"`)
	if clean == "" {
		return false
	}
	info, err := os.Stat(clean)
	return err == nil && !info.IsDir()
}

func GetTaskPath(appName string) string {
	cmd := exec.Command(getSchtasksPath(), "/Query", "/TN", appName, "/XML")
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}

	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	content := decodeConsole(out)

	startTag := "<Command>"
	endTag := "</Command>"

	startIdx := strings.Index(content, startTag)
	endIdx := strings.Index(content, endTag)

	if startIdx == -1 || endIdx == -1 || startIdx >= endIdx {
		return ""
	}

	rawCommand := strings.TrimSpace(content[startIdx+len(startTag) : endIdx])
	registeredPath := html.UnescapeString(rawCommand)
	return filepath.Clean(strings.Trim(registeredPath, `"`))
}

func IsTaskPathValid(appName, currentExePath string) bool {
	registered := GetTaskPath(appName)
	if registered == "" {
		return false
	}
	current := filepath.Clean(strings.Trim(currentExePath, `"`))
	return strings.EqualFold(registered, current)
}

func decodeConsole(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if utf8.Valid(b) {
		return string(b)
	}

	n, _, _ := procMultiByteToWideChar.Call(0, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 0, 0)
	if n == 0 {
		return string(b)
	}

	buf := make([]uint16, n)
	procMultiByteToWideChar.Call(0, 0, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(unsafe.Pointer(&buf[0])), n)
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

func writeUTF16LE(filename string, s string) error {
	runes := utf16.Encode([]rune(s))
	b := make([]byte, 2+len(runes)*2)
	b[0] = 0xFF
	b[1] = 0xFE
	for i, r := range runes {
		b[2+i*2] = byte(r)
		b[2+i*2+1] = byte(r >> 8)
	}
	return os.WriteFile(filename, b, 0600)
}

func RunScheduledTask(taskName string) error {
	cmd := exec.Command(getSchtasksPath(), "/Run", "/TN", taskName)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd.Run()
}
