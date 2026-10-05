package core

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

var shortCircuitKeywords = []string{
	"download",
	"fetching",
	"updating",
	"pulling",
}

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
		return errors.New("无法读取校验输出: " + err.Error())
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return errors.New("无法读取校验错误: " + err.Error())
	}

	if err := cmd.Start(); err != nil {
		return errors.New("校验进程启动失败: " + err.Error())
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

			isShortCircuit := false
			for _, kw := range shortCircuitKeywords {
				if strings.Contains(lowerLine, kw) {
					isShortCircuit = true
					break
				}
			}

			if isShortCircuit {
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
				resultCh <- errors.New("内核校验进程意外退出 (请检查配置格式或内核版本兼容性)")
			}
		} else {
			resultCh <- nil
		}
	}()

	var finalErr error
	select {
	case <-ctx.Done():
		finalErr = errors.New("配置校验超时 (10秒)，内核无响应")
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
