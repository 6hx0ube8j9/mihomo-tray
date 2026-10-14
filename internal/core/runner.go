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
	return &ProcessRunner{job: job}
}

func (r *ProcessRunner) Close() {
	r.Kill()
	if r.job != nil {
		_ = r.job.Close()
	}
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
