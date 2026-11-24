package app

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

type KernelDeployer struct {
	cfg    *config.Manager
	st     *state.RuntimeState
	kernel *core.KernelManager
	api    *core.APIClient

	reloadAborted atomic.Bool
}

func NewKernelDeployer(cfg *config.Manager, st *state.RuntimeState, kernel *core.KernelManager, api *core.APIClient) *KernelDeployer {
	return &KernelDeployer{
		cfg:    cfg,
		st:     st,
		kernel: kernel,
		api:    api,
	}
}

func (d *KernelDeployer) IsKernelRunning() bool {
	return d.st.GetPhase() == domain.PhaseRunning && !d.kernel.IsPaused()
}

func (d *KernelDeployer) HasAbortedReload() bool {
	return d.reloadAborted.Load()
}

func (d *KernelDeployer) ReloadConfig(ctx context.Context) error {
	if !d.IsKernelRunning() {
		return errKernelNotReady
	}

	activePath := d.cfg.GetActivePath()
	prep, err := d.prepareYAML(activePath)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if d.reloadAborted.Swap(false) {
		if err := d.commitRuntimeYAML(prep); err != nil {
			return err
		}
		d.restartProcess()
		slog.Info("上次热重载已中断，已改为重启内核重载配置", "path", activePath)
		return nil
	}

	if err := d.api.ForceReloadKernel(ctx, string(prep.YAML)); err != nil {
		if errors.Is(err, context.Canceled) && !d.st.IsExiting() {
			d.reloadAborted.Store(true)
		}
		return err
	}

	if err := d.commitRuntimeYAML(prep); err != nil {
		return err
	}

	slog.Info("重载配置成功", "path", activePath)
	return nil
}

func (d *KernelDeployer) RestartKernel() error {
	slog.Info("正在重启内核")
	d.reloadAborted.Store(false)
	if err := d.SyncRuntimeConfig(); err != nil {
		return err
	}
	d.restartProcess()
	return nil
}

func (d *KernelDeployer) DeployAndApply(ctx context.Context, targetRelPath, actionDesc string) error {
	prep, err := d.prepareYAML(targetRelPath)
	if err != nil {
		return err
	}

	if d.IsKernelRunning() && !d.reloadAborted.Swap(false) {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		reloadErr := d.api.ForceReloadKernel(ctx, string(prep.YAML))
		if reloadErr == nil {
			if err := d.commitRuntimeYAML(prep); err != nil {
				return err
			}
			slog.Info("配置应用成功", "action", actionDesc, "path", targetRelPath)
			return nil
		}

		if errors.Is(reloadErr, context.Canceled) {
			if !d.st.IsExiting() {
				d.reloadAborted.Store(true)
			}
			return reloadErr
		}

		var apiErr *core.APIError
		if errors.As(reloadErr, &apiErr) {
			return reloadErr
		}

		slog.Warn("热重载通信异常，回退重启内核", "action", actionDesc, "path", targetRelPath, "err", reloadErr)
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	if err := d.commitRuntimeYAML(prep); err != nil {
		return err
	}
	d.restartProcess()
	return nil
}

func (d *KernelDeployer) SyncRuntimeConfig() error {
	prep, err := d.prepareYAML(d.cfg.GetActivePath())
	if err != nil {
		return err
	}
	return d.commitRuntimeYAML(prep)
}

func (d *KernelDeployer) restartProcess() {
	cfg := d.cfg.GetConfig()
	if cfg.Config.Tun.Enable {
		d.st.SetTunRequestedTime(time.Now())
	}
	d.st.SetPhase(domain.PhaseInitializing)
	d.kernel.Restart()
}

func (d *KernelDeployer) prepareYAML(targetRelPath string) (*core.ComposeResult, error) {
	if targetRelPath != "" {
		if err := d.cfg.ValidatePhysicalFile(targetRelPath); err != nil {
			return nil, err
		}
	}
	return core.PrepareRuntimeYAML(d.cfg.GetConfig(), targetRelPath, d.cfg.BaseDir())
}

func (d *KernelDeployer) commitRuntimeYAML(prep *core.ComposeResult) error {
	if _, _, err := core.WriteRuntimeYAML(prep.YAML, d.cfg.BaseDir()); err != nil {
		return err
	}
	d.st.SetActualTunDevice(prep.TunDevice)
	return nil
}

// KEEP: DO NOT REMOVE. Reserved for future in-process kernel restart via REST API (POST /restart).
// Note: Bypasses supervisor daemon and Windows Job Object tracking.
// func (d *KernelDeployer) RestartKernelViaAPI(ctx context.Context) error {
// 	if !d.IsKernelRunning() {
// 		return domain.ErrKernelNotReady
// 	}
// 	if err := d.api.RestartKernel(ctx); err != nil {
// 		return err
// 	}
// 	if err := d.kernel.WaitForReady(ctx); err != nil {
// 		return err
// 	}
// 	slog.Debug("内核热重启成功")
// 	return nil
// }
