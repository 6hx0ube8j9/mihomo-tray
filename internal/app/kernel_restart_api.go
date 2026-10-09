package app

import (
	"context"
	"fmt"
	"time"
)

// RestartKernelViaAPI triggers an in-place restart of the kernel process via RESTful API (POST /restart).
// Note: This bypasses the supervisor daemon and Windows Job Object lifecycle tracking.
func (a *Application) RestartKernelViaAPI(ctx context.Context) error {
	cmdCtx, cmdCancel := context.WithTimeout(ctx, 2*time.Second)
	defer cmdCancel()

	if err := a.API.RestartKernel(cmdCtx); err != nil {
		return fmt.Errorf("api restart request failed: %w", err)
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 3*time.Second)
	defer waitCancel()

	if err := a.API.WaitForReady(waitCtx); err != nil {
		return fmt.Errorf("kernel readiness timeout: %w", err)
	}

	time.Sleep(200 * time.Millisecond)
	a.syncAllConfig(ctx)
	a.ForceSyncAPI()
	return nil
}
