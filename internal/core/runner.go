package core

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/sys"
)

type ProcessRunner struct {
	job          *sys.Job
	activeProc   *os.Process
	currentPid   atomic.Uint32
	isKilledByUs atomic.Bool

	mu     sync.Mutex
	killMu sync.Mutex
}

func NewProcessRunner() *ProcessRunner {
	job, _ := sys.NewKillOnCloseJob()
	return &ProcessRunner{
		job: job,
	}
}

func (r *ProcessRunner) Close() {
	r.Kill()
	if r.job != nil {
		_ = r.job.Close()
	}
}

func (r *ProcessRunner) CleanupOrphans() {
	sys.KillOtherProcessesByName(domain.KernelExeName, 0)
}

func (r *ProcessRunner) Start(workDir, targetExe, runtimeConfig string, out io.Writer) error {
	cmd := exec.Command(targetExe, "-d", ".", "-f", runtimeConfig)
	cmd.Dir = workDir
	cmd.SysProcAttr = sys.BackgroundProcAttr()
	cmd.Stdout = out
	cmd.Stderr = out

	if err := cmd.Start(); err != nil {
		return err
	}

	r.mu.Lock()
	r.activeProc = cmd.Process
	r.currentPid.Store(uint32(cmd.Process.Pid))
	r.isKilledByUs.Store(false)
	r.mu.Unlock()

	r.job.Assign(cmd.Process.Pid)
	return nil
}

func (r *ProcessRunner) Wait(ctx context.Context) error {
	r.mu.Lock()
	proc := r.activeProc
	r.mu.Unlock()

	if proc == nil {
		return nil
	}

	waitDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			r.Kill()
		case <-waitDone:
		}
	}()

	state, err := proc.Wait()
	close(waitDone)

	r.mu.Lock()
	r.activeProc = nil
	r.currentPid.Store(0)
	r.mu.Unlock()

	if err != nil {
		return err
	}
	if !state.Success() {
		return state.ExitCodeError()
	}
	return nil
}

func (r *ProcessRunner) Kill() {
	r.killMu.Lock()
	defer r.killMu.Unlock()

	r.mu.Lock()
	proc := r.activeProc
	pid := r.currentPid.Load()

	if proc == nil || pid == 0 {
		r.mu.Unlock()
		return
	}

	r.isKilledByUs.Store(true)
	r.activeProc = nil
	r.currentPid.Store(0)
	r.mu.Unlock()

	forceKill := func() {
		_ = proc.Kill()
		sys.HardKill(pid)
		sys.KillOtherProcessesByName(domain.KernelExeName, 0)
	}

	if err := sys.SendCtrlBreak(pid); err != nil {
		forceKill()
	} else {
		exited := false
		for i := 0; i < 30; i++ {
			if !sys.IsPidRunning(pid, domain.KernelExeName) {
				exited = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}

		if !exited {
			forceKill()
		}
	}
	time.Sleep(100 * time.Millisecond)
}

func (r *ProcessRunner) Pid() uint32 {
	return r.currentPid.Load()
}

func (r *ProcessRunner) IsRunning() bool {
	pid := r.currentPid.Load()
	return pid != 0 && sys.IsPidRunning(pid, domain.KernelExeName)
}

func (r *ProcessRunner) IsKilledByUs() bool {
	return r.isKilledByUs.Load()
}
