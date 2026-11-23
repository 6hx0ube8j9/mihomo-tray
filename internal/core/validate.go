package core

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"mihomo-tray/internal/sys"
)

const (
	foregroundValidationTimeout = 10 * time.Second
	bgValidationMaxLifetime     = 2 * time.Minute
)

var shortCircuitKeywords = []string{
	"download",
	"fetching",
	"updating",
	"pulling",
}

var ErrValidationTimeout = errors.New("配置校验超时")

type bgValidationProc struct {
	cmd  *exec.Cmd
	done chan struct{}
}

var (
	bgValMu     sync.Mutex
	activeBgVal *bgValidationProc
)

func StopBackgroundValidation() {
	bgValMu.Lock()
	proc := activeBgVal
	activeBgVal = nil
	bgValMu.Unlock()

	if proc == nil {
		return
	}
	if proc.cmd.Process != nil {
		_ = proc.cmd.Process.Kill()
	}
	<-proc.done
}

func ValidateConfig(exePath, workDir, yamlAbsPath string, removeAfterExit bool) error {
	StopBackgroundValidation()

	cmd := exec.Command(exePath, "-d", ".", "-t", "-f", yamlAbsPath)
	cmd.Dir = workDir
	cmd.SysProcAttr = sys.BackgroundProcAttr()

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		if removeAfterExit {
			_ = os.Remove(yamlAbsPath)
		}
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		if removeAfterExit {
			_ = os.Remove(yamlAbsPath)
		}
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		if removeAfterExit {
			_ = os.Remove(yamlAbsPath)
		}
		return fmt.Errorf("start validator: %w", err)
	}

	doneCh := make(chan struct{})
	shortCircuitCh := make(chan struct{})
	var shortCircuitOnce sync.Once

	errCh := make(chan error, 1)
	var lastErrorMsg string
	var msgMu sync.Mutex

	scanFunc := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()

			if errMsg, isFatal := extractFatalError(line); isFatal {
				msgMu.Lock()
				lastErrorMsg = errMsg
				msgMu.Unlock()
				continue
			}

			select {
			case <-shortCircuitCh:
				continue
			default:
			}

			lowerLine := strings.ToLower(line)
			for _, kw := range shortCircuitKeywords {
				if strings.Contains(lowerLine, kw) {
					shortCircuitOnce.Do(func() {
						close(shortCircuitCh)
					})
					break
				}
			}
		}
	}

	var scanWg sync.WaitGroup
	scanWg.Add(2)
	go func() { defer scanWg.Done(); scanFunc(stdoutPipe) }()
	go func() { defer scanWg.Done(); scanFunc(stderrPipe) }()

	go func() {
		defer close(doneCh)
		scanWg.Wait()
		waitErr := cmd.Wait()

		if removeAfterExit {
			_ = os.Remove(yamlAbsPath)
		}

		bgValMu.Lock()
		if activeBgVal != nil && activeBgVal.cmd == cmd {
			activeBgVal = nil
		}
		bgValMu.Unlock()

		msgMu.Lock()
		errMsg := lastErrorMsg
		msgMu.Unlock()

		if waitErr != nil {
			if errMsg != "" {
				errCh <- &ConfigError{Msg: errMsg}
			} else {
				errCh <- fmt.Errorf("exit status: %w", waitErr)
			}
		} else {
			errCh <- nil
		}
	}()

	timer := time.NewTimer(foregroundValidationTimeout)
	defer timer.Stop()

	select {
	case <-shortCircuitCh:
		bgValMu.Lock()
		select {
		case <-doneCh:
		default:
			activeBgVal = &bgValidationProc{cmd: cmd, done: doneCh}
		}
		bgValMu.Unlock()

		go func() {
			watchdog := time.NewTimer(bgValidationMaxLifetime)
			defer watchdog.Stop()
			select {
			case <-doneCh:
			case <-watchdog.C:
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
				<-doneCh
			}
		}()
		return nil

	case err := <-errCh:
		return err

	case <-timer.C:
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-doneCh
		return ErrValidationTimeout
	}
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
