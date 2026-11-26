package core

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"mihomo-tray/internal/sys"
)

const foregroundValidationTimeout = 10 * time.Second

var shortCircuitKeywords = []string{
	"download",
	"fetching",
	"updating",
	"pulling",
}

var (
	ErrValidationTimeout   = errors.New("配置校验超时")
	ErrValidationPreempted = errors.New("配置校验已中止")
)

type validationProc struct {
	cmd  *exec.Cmd
	job  *sys.Job
	done chan struct{}
}

func (p *validationProc) terminate() {
	if p == nil {
		return
	}
	if p.job != nil {
		_ = p.job.Terminate(0)
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

type Validator struct {
	mu            sync.Mutex
	active        *validationProc
	hasValidHash  bool
	lastValidHash [sha256.Size]byte
}

func NewValidator() *Validator {
	return &Validator{}
}

func (v *Validator) Stop() {
	v.mu.Lock()
	proc := v.active
	v.active = nil
	v.mu.Unlock()

	if proc == nil {
		return
	}
	proc.terminate()
	<-proc.done
}

func (v *Validator) MarkValid(yamlBytes []byte) {
	if len(yamlBytes) == 0 {
		return
	}
	hash := sha256.Sum256(yamlBytes)
	v.mu.Lock()
	v.lastValidHash = hash
	v.hasValidHash = true
	v.mu.Unlock()
}

func (v *Validator) Invalidate() {
	v.mu.Lock()
	v.hasValidHash = false
	v.lastValidHash = [sha256.Size]byte{}
	v.mu.Unlock()
}

func (v *Validator) Validate(ctx context.Context, exePath, workDir string, yamlBytes []byte) error {
	return v.validate(ctx, exePath, workDir, yamlBytes, foregroundValidationTimeout, true, true)
}

func (v *Validator) ValidateFull(ctx context.Context, exePath, workDir string, yamlBytes []byte, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = foregroundValidationTimeout
	}
	return v.validate(ctx, exePath, workDir, yamlBytes, timeout, false, false)
}

func (v *Validator) validate(ctx context.Context, exePath, workDir string, yamlBytes []byte, timeout time.Duration, allowShortCircuit, allowPreempt bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	hash := sha256.Sum256(yamlBytes)
	v.mu.Lock()
	if v.hasValidHash && v.lastValidHash == hash {
		v.mu.Unlock()
		return nil
	}
	v.mu.Unlock()

	tmpFile, err := os.CreateTemp(workDir, ".check-*.yaml")
	if err != nil {
		return fmt.Errorf("create test config: %w", err)
	}
	yamlAbsPath := tmpFile.Name()

	if _, err := tmpFile.Write(yamlBytes); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(yamlAbsPath)
		return fmt.Errorf("write test config: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(yamlAbsPath)
		return fmt.Errorf("close test config: %w", err)
	}

	var (
		proc       *validationProc
		stdoutPipe io.ReadCloser
		stderrPipe io.ReadCloser
	)

	for {
		if err := ctx.Err(); err != nil {
			_ = os.Remove(yamlAbsPath)
			return err
		}

		v.mu.Lock()
		prev := v.active
		if prev == nil {
			cmd := exec.Command(exePath, "-d", ".", "-t", "-f", yamlAbsPath)
			cmd.Dir = workDir
			cmd.SysProcAttr = sys.BackgroundProcAttr()

			stdoutPipe, err = cmd.StdoutPipe()
			if err != nil {
				v.mu.Unlock()
				_ = os.Remove(yamlAbsPath)
				return fmt.Errorf("stdout pipe: %w", err)
			}
			stderrPipe, err = cmd.StderrPipe()
			if err != nil {
				v.mu.Unlock()
				_ = os.Remove(yamlAbsPath)
				return fmt.Errorf("stderr pipe: %w", err)
			}

			job, jobErr := sys.NewKillOnCloseJob()
			if jobErr != nil {
				slog.Warn("创建校验进程作业失败", "err", jobErr)
			}

			if err := cmd.Start(); err != nil {
				if job != nil {
					_ = job.Close()
				}
				v.mu.Unlock()
				_ = os.Remove(yamlAbsPath)
				return fmt.Errorf("start validator: %w", err)
			}

			if job != nil {
				job.Assign(cmd.Process.Pid)
			}

			proc = &validationProc{
				cmd:  cmd,
				job:  job,
				done: make(chan struct{}),
			}
			v.active = proc
			v.mu.Unlock()
			break
		}

		if !allowPreempt {
			v.mu.Unlock()
			_ = os.Remove(yamlAbsPath)
			return ErrValidationPreempted
		}

		v.active = nil
		v.mu.Unlock()

		prev.terminate()
		<-prev.done
	}

	var shortCircuitCh chan struct{}
	var shortCircuitOnce sync.Once
	if allowShortCircuit {
		shortCircuitCh = make(chan struct{})
	}

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

			if shortCircuitCh == nil {
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
		defer close(proc.done)
		scanWg.Wait()
		waitErr := proc.cmd.Wait()

		if proc.job != nil {
			_ = proc.job.Terminate(0)
			_ = proc.job.Close()
		}
		_ = os.Remove(yamlAbsPath)

		v.mu.Lock()
		stillActive := (v.active == proc)
		if stillActive {
			v.active = nil
		}
		if stillActive && waitErr == nil {
			v.lastValidHash = hash
			v.hasValidHash = true
		}
		v.mu.Unlock()

		if !stillActive {
			errCh <- ErrValidationPreempted
			return
		}

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

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		proc.terminate()
		<-proc.done
		return ctx.Err()

	case <-shortCircuitCh:
		proc.terminate()
		<-proc.done
		return nil

	case err := <-errCh:
		return err

	case <-timer.C:
		proc.terminate()
		<-proc.done
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
