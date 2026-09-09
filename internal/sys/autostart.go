//go:build windows

package sys

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"html"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const TaskName = "MihomoTrayTask"

func ToggleAutoStart(exePath, baseDir string, enable bool) bool {
	if key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE); err == nil {
		_ = key.DeleteValue("MihomoTray")
		key.Close()
	}

	schtasksPath := filepath.Join(os.Getenv("SystemRoot"), "System32", "schtasks.exe")

	if enable {
		slog.Debug("注册自启计划任务", "task", TaskName)

		absExe, _ := filepath.Abs(exePath)
		absBase, _ := filepath.Abs(baseDir)

		xmlContent := generateTaskXML(absExe, "--autostart", absBase)
		tempXML := filepath.Join(os.TempDir(), fmt.Sprintf("%s.xml", TaskName))

		if err := writeUTF16LE(tempXML, xmlContent); err != nil {
			slog.Error("写入计划任务临时文件失败", "err", err)
			return false
		}
		defer os.Remove(tempXML)

		cmd := exec.Command(schtasksPath, "/Create", "/TN", TaskName, "/XML", tempXML, "/F")
		cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}

		if out, err := cmd.CombinedOutput(); err != nil {
			slog.Error("导入计划任务失败", "err", err, "output", strings.TrimSpace(string(out)))
			return false
		}
		return true
	}

    slog.Debug("删除自启计划任务", "task", TaskName)
    cmd := exec.Command(schtasksPath, "/Delete", "/TN", TaskName, "/F")
    cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}

    if err := cmd.Run(); err != nil {
        slog.Debug("删除计划任务失败", "task", TaskName, "err", err)
    }
    return !CheckAutoStartStatus()
}

func CheckAutoStartStatus() bool {
	schtasksPath := filepath.Join(os.Getenv("SystemRoot"), "System32", "schtasks.exe")
	cmd := exec.Command(schtasksPath, "/Query", "/TN", TaskName)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd.Run() == nil
}

func IsTaskPathValid(currentExePath string) bool {
	schtasksPath := filepath.Join(os.Getenv("SystemRoot"), "System32", "schtasks.exe")
	cmd := exec.Command(schtasksPath, "/Query", "/TN", TaskName, "/XML")
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	
	out, err := cmd.Output()
	if err != nil {
		slog.Debug("读取计划任务配置失败", "task", TaskName, "err", err)
		return false
	}

	utf8Out := out
	if len(out) >= 2 && out[0] == 0xFF && out[1] == 0xFE {
		utf16Vals := make([]uint16, (len(out)-2)/2)
		for i := 0; i < len(utf16Vals); i++ {
			utf16Vals[i] = uint16(out[2+i*2]) | (uint16(out[2+i*2+1]) << 8)
		}
		utf8Out = []byte(windows.UTF16ToString(utf16Vals))
	}

	startTag := []byte("<Command>")
	endTag := []byte("</Command>")

	startIdx := bytes.Index(utf8Out, startTag)
	endIdx := bytes.Index(utf8Out, endTag)

	if startIdx == -1 || endIdx == -1 || startIdx >= endIdx {
		return false
	}

	rawCommand := string(bytes.TrimSpace(utf8Out[startIdx+len(startTag) : endIdx]))
	registeredPath := html.UnescapeString(rawCommand)
	registeredPath = strings.Trim(registeredPath, `"`)
	currentExePath = strings.Trim(currentExePath, `"`)

	return strings.EqualFold(filepath.Clean(registeredPath), filepath.Clean(currentExePath))
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
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
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
