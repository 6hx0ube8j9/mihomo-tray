package core

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"mihomo-tray/internal/sys"
)

const (
	pipeWaitDelay      = 2 * time.Second
	killWaitExitBudget = 3 * time.Second
)

type ProcessRunner struct {
	exeName string

	activeJob    *sys.Job
	activeCmd    *exec.Cmd
	waitDone     chan struct{}
	currentPid   atomic.Uint32
	isKilledByUs atomic.Bool

	mu     sync.Mutex
	killMu sync.Mutex
}

func NewProcessRunner(exeName string) *ProcessRunner {
	return &ProcessRunner{exeName: exeName}
}

func (r *ProcessRunner) Close() {
	r.Kill()
	r.mu.Lock()
	job := r.activeJob
	r.activeJob = nil
	r.mu.Unlock()
	if job != nil {
		_ = job.Terminate(0)
		_ = job.Close()
	}
}

func (r *ProcessRunner) CleanupOrphans() {
	if r.exeName == "" {
		return
	}
	sys.KillOtherProcessesByName(r.exeName, 0)
}

func (r *ProcessRunner) Start(workDir, targetExe string, args []string, out io.Writer) error {
	cmd := exec.Command(targetExe, args...)
	cmd.Dir = workDir
	cmd.SysProcAttr = sys.BackgroundProcAttr()
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = pipeWaitDelay

	job, err := sys.NewKillOnCloseJob()
	if err != nil {
		slog.Warn("创建进程保护作业失败", "err", err)
	}

	if err := cmd.Start(); err != nil {
		if job != nil {
			_ = job.Close()
		}
		return err
	}

	if job != nil {
		job.Assign(cmd.Process.Pid)
	}

	r.mu.Lock()
	r.activeCmd = cmd
	r.activeJob = job
	r.waitDone = nil
	r.currentPid.Store(uint32(cmd.Process.Pid))
	r.isKilledByUs.Store(false)
	r.mu.Unlock()
	return nil
}

func (r *ProcessRunner) Wait(ctx context.Context) error {
	r.mu.Lock()
	cmd := r.activeCmd
	if cmd == nil {
		r.mu.Unlock()
		return nil
	}
	waitDone := make(chan struct{})
	r.waitDone = waitDone
	r.mu.Unlock()

	cancelWatch := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			r.Kill()
		case <-cancelWatch:
		}
	}()

	err := cmd.Wait()
	close(cancelWatch)

	r.mu.Lock()
	job := r.activeJob
	r.activeCmd = nil
	r.activeJob = nil
	r.waitDone = nil
	r.currentPid.Store(0)
	r.mu.Unlock()

	if job != nil {
		_ = job.Terminate(0)
		_ = job.Close()
	} else if errors.Is(err, exec.ErrWaitDelay) {
		r.CleanupOrphans()
	}

	close(waitDone)

	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0 {
		slog.Info("检测到内核自行退出或完成升级，准备重新接管")
		return nil
	}

	return err
}

func (r *ProcessRunner) Kill() {
	r.killMu.Lock()
	defer r.killMu.Unlock()

	r.mu.Lock()
	cmd := r.activeCmd
	job := r.activeJob
	waitDone := r.waitDone
	pid := r.currentPid.Load()

	if cmd == nil || cmd.Process == nil || pid == 0 {
		r.mu.Unlock()
		return
	}

	r.isKilledByUs.Store(true)
	r.mu.Unlock()

	forceKill := func() {
		if job != nil {
			_ = job.Terminate(0)
		}
		_ = cmd.Process.Kill()
		if sys.IsPidRunning(pid, r.exeName) {
			sys.HardKill(pid)
		}
	}

	if !sys.IsPidRunning(pid, r.exeName) {
		forceKill()
	} else if err := sys.SendCtrlBreak(pid); err != nil {
		forceKill()
	} else {
		exited := false
		for i := 0; i < 30; i++ {
			if !sys.IsPidRunning(pid, r.exeName) {
				exited = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}

		if !exited {
			forceKill()
		} else if job != nil {
			_ = job.Terminate(0)
		}
	}

	if waitDone != nil {
		select {
		case <-waitDone:
		case <-time.After(killWaitExitBudget):
		}
	} else {
		time.Sleep(100 * time.Millisecond)
	}
}

func (r *ProcessRunner) Pid() uint32 {
	return r.currentPid.Load()
}

func (r *ProcessRunner) IsRunning() bool {
	pid := r.currentPid.Load()
	return pid != 0 && sys.IsPidRunning(pid, r.exeName)
}

func (r *ProcessRunner) IsKilledByUs() bool {
	return r.isKilledByUs.Load()
}
