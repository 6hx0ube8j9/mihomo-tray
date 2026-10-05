package core

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

func ValidateConfig(exePath, workDir, yamlAbsPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, exePath, "-d", ".", "-t", "-f", yamlAbsPath)
	cmd.Dir = workDir

	const CREATE_DEFAULT_ERROR_MODE = 0x04000000
	cmd.SysProcAttr = &windows.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | CREATE_DEFAULT_ERROR_MODE,
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("无法建立输出管道: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("无法建立错误管道: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("沙盒进程启动失败: %w", err)
	}

	resultCh := make(chan error, 1)

	var lastErrorMsg string
	var msgMu sync.Mutex

	scanCtx, scanCancel := context.WithCancel(context.Background())
	defer scanCancel()

	scanFunc := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			select {
			case <-scanCtx.Done():
				return
			default:
			}

			line := scanner.Text()
			lowerLine := strings.ToLower(line)

			if strings.Contains(lowerLine, "download") {
				select {
				case resultCh <- nil:
				default:
				}
				scanCancel()
				return
			}

			if errMsg, isFatal := extractFatalError(line); isFatal {
				msgMu.Lock()
				lastErrorMsg = errMsg
				msgMu.Unlock()
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); scanFunc(stdoutPipe) }()
	go func() { defer wg.Done(); scanFunc(stderrPipe) }()

	go func() {
		wg.Wait()
		err := cmd.Wait()

		select {
		case <-scanCtx.Done():
			return
		default:
		}

		msgMu.Lock()
		errMsg := lastErrorMsg
		msgMu.Unlock()

		if err != nil {
			if errMsg != "" {
				resultCh <- errors.New(errMsg)
			} else {
				resultCh <- fmt.Errorf("内核预检测异常闪退 (可能存在协议或格式不兼容)")
			}
		} else {
			resultCh <- nil
		}
	}()

	var finalErr error
	select {
	case <-ctx.Done():
		finalErr = fmt.Errorf("内核检测超时(10s)，可能遭遇系统 I/O 死锁")
	case err := <-resultCh:
		finalErr = err
	}

	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}

	return finalErr
}

func extractFatalError(line string) (string, bool) {
	lowerLine := strings.ToLower(line)

	isErrorLevel := strings.Contains(lowerLine, "level=error") ||
		strings.Contains(lowerLine, "level=fatal") ||
		strings.Contains(lowerLine, "fata[") ||
		strings.Contains(lowerLine, "err[")

	if !isErrorLevel {
		return "", false
	}
	cleanMsg := extractLogMsg(line)
	if cleanMsg != "" {
		return cleanMsg, true
	}
	
	return line, true
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
