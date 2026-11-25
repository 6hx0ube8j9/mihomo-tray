package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

type KernelDeployer struct {
	cfg       *config.Manager
	st        *state.RuntimeState
	kernel    *core.KernelManager
	api       *core.APIClient
	validator *core.Validator
}

func NewKernelDeployer(cfg *config.Manager, st *state.RuntimeState, kernel *core.KernelManager, api *core.APIClient) *KernelDeployer {
	return &KernelDeployer{
		cfg:       cfg,
		st:        st,
		kernel:    kernel,
		api:       api,
		validator: core.NewValidator(),
	}
}

func (d *KernelDeployer) IsKernelRunning() bool {
	return d.st.GetPhase() == domain.PhaseRunning && !d.kernel.IsPaused()
}

func (d *KernelDeployer) StopValidation() {
	d.validator.Stop()
}

func (d *KernelDeployer) PrepareBootConfig() error {
	prep, err := d.prepareYAML(d.cfg.GetActivePath())
	if err != nil {
		return err
	}
	return d.commitRuntimeYAML(prep)
}

func (d *KernelDeployer) WriteMinimalConfig() error {
	d.validator.Stop()
	prep, err := d.prepareYAML("")
	if err != nil {
		return err
	}
	return d.commitRuntimeYAML(prep)
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

	d.validator.Stop()
	if err := d.api.ForceReloadKernel(ctx, string(prep.YAML)); err != nil {
		return err
	}

	d.validator.MarkValid(prep.YAML)
	if err := d.commitRuntimeYAML(prep); err != nil {
		return err
	}

	slog.Info("重载配置成功", "path", activePath)
	return nil
}

func (d *KernelDeployer) RestartKernel(ctx context.Context) error {
	slog.Info("正在重启内核")
	activePath := d.cfg.GetActivePath()
	prep, err := d.prepareYAML(activePath)
	if err != nil {
		return err
	}

	if activePath != "" {
		if err := d.validatePrepared(ctx, prep); err != nil {
			return err
		}
	}

	if err := d.commitRuntimeYAML(prep); err != nil {
		return err
	}
	d.restartProcess()
	return nil
}

func (d *KernelDeployer) DeployAndApply(ctx context.Context, targetRelPath, actionDesc string, onCommitted func()) error {
	prep, err := d.prepareYAML(targetRelPath)
	if err != nil {
		return err
	}

	if d.IsKernelRunning() {
		d.validator.Stop()
		reloadErr := d.api.ForceReloadKernel(ctx, string(prep.YAML))
		if reloadErr == nil {
			d.validator.MarkValid(prep.YAML)
			if err := d.commitRuntimeYAML(prep); err != nil {
				return err
			}
			if onCommitted != nil {
				onCommitted()
			}
			slog.Info("配置应用成功", "action", actionDesc, "path", targetRelPath)
			return nil
		}

		var apiErr *core.APIError
		if errors.As(reloadErr, &apiErr) || errors.Is(reloadErr, context.Canceled) || ctx.Err() != nil {
			return reloadErr
		}

		slog.Warn("热重载通信异常，校验后回退重启内核", "action", actionDesc, "path", targetRelPath, "err", reloadErr)
	}

	if targetRelPath != "" {
		if err := d.validatePrepared(ctx, prep); err != nil {
			return err
		}
	}

	if err := d.commitRuntimeYAML(prep); err != nil {
		return err
	}
	if onCommitted != nil {
		onCommitted()
	}
	d.restartProcess()
	slog.Info("配置应用成功", "action", actionDesc, "path", targetRelPath)
	return nil
}

func (d *KernelDeployer) restartProcess() {
	d.validator.Stop()

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

func (d *KernelDeployer) validatePrepared(ctx context.Context, prep *core.ComposeResult) error {
	return d.validator.Validate(ctx, core.GetKernelPath(d.cfg.BaseDir()), d.cfg.BaseDir(), prep.YAML)
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
