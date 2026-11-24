package core

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mihomo-tray/internal/sys"
)

const (
	validationTimeout = 10 * time.Second
	tempCheckPattern  = ".check-*.yaml"
)

var shortCircuitKeywords = []string{
	"download",
	"fetching",
	"updating",
	"pulling",
}

var (
	ErrValidationTimeout = errors.New("配置校验超时")
	valMu                sync.Mutex
)

// CleanupTempCheckFiles 清理因异常断电或系统强杀残留的临时校验配置文件。
func CleanupTempCheckFiles(baseDir string) {
	matches, err := filepath.Glob(filepath.Join(baseDir, tempCheckPattern))
	if err != nil {
		return
	}
	for _, path := range matches {
		_ = os.Remove(path)
	}
}

func ValidateConfig(exePath, workDir string, yamlBytes []byte) error {
	valMu.Lock()
	defer valMu.Unlock()

	tmpFile, err := os.CreateTemp(workDir, tempCheckPattern)
	if err != nil {
		return fmt.Errorf("create test config: %w", err)
	}
	yamlAbsPath := tmpFile.Name()
	defer os.Remove(yamlAbsPath)

	if _, err := tmpFile.Write(yamlBytes); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write test config: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close test config: %w", err)
	}

	cmd := exec.Command(exePath, "-d", ".", "-t", "-f", yamlAbsPath)
	cmd.Dir = workDir
	cmd.SysProcAttr = sys.BackgroundProcAttr()

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start validator: %w", err)
	}

	shortCircuitCh := make(chan struct{})
	var shortCircuitOnce sync.Once

	errCh := make(chan error, 1)
	doneCh := make(chan struct{})
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

	timer := time.NewTimer(validationTimeout)
	defer timer.Stop()

	select {
	case <-shortCircuitCh:
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-doneCh
		return nil

	case err := <-errCh:
		<-doneCh
		select {
		case <-shortCircuitCh:
			return nil
		default:
		}
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
