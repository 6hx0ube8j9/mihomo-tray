package app

import (
	"context"
	"log/slog"
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

func (d *KernelDeployer) ReloadConfig(ctx context.Context) error {
	activePath := d.cfg.GetActivePath()
	runtimeAbs, err := d.prepareRuntimeConfig(activePath)
	if err != nil {
		return err
	}

	if !d.IsKernelRunning() {
		return domain.ErrKernelNotReady
	}

	if err := d.api.ForceReloadKernel(ctx, runtimeAbs); err != nil {
		return err
	}

	slog.Debug("重载配置成功", "target", activePath)
	return nil
}

func (d *KernelDeployer) RestartKernel() error {
	slog.Info("正在重启内核")
	if _, err := d.prepareRuntimeConfig(d.cfg.GetActivePath()); err != nil {
		return err
	}
	d.restartProcess()
	return nil
}

func (d *KernelDeployer) DeployAndApply(ctx context.Context, targetRelPath, actionDesc string) error {
	runtimeAbs, err := d.prepareRuntimeConfig(targetRelPath)
	if err != nil {
		return err
	}

	if d.IsKernelRunning() {
		slog.Debug("正在应用配置", "action", actionDesc, "target", targetRelPath)
		if err := d.api.ForceReloadKernel(ctx, runtimeAbs); err == nil {
			slog.Debug("配置应用成功", "action", actionDesc, "target", targetRelPath)
			return nil
		}
		slog.Warn("重载配置失败，回退重启内核", "action", actionDesc, "target", targetRelPath, "err", err)
	}

	d.restartProcess()
	return nil
}

func (d *KernelDeployer) SyncRuntimeConfig() error {
	_, err := d.prepareRuntimeConfig(d.cfg.GetActivePath())
	return err
}

func (d *KernelDeployer) prepareRuntimeConfig(targetRelPath string) (string, error) {
	if targetRelPath != "" {
		if err := d.cfg.ValidatePhysicalFile(targetRelPath); err != nil {
			return "", err
		}
	}

	cfg := d.cfg.GetConfig()
	deployRes, err := core.DeployRuntimeConfig(cfg, targetRelPath, d.cfg.BaseDir())
	if err != nil {
		return "", err
	}

	d.st.SetActualTunDevice(deployRes.TunDevice)
	return deployRes.RuntimeAbs, nil
}

func (d *KernelDeployer) restartProcess() {
	cfg := d.cfg.GetConfig()
	slog.Debug("重启内核进程", "tun", cfg.Config.Tun.Enable)
	if cfg.Config.Tun.Enable {
		d.st.SetTunRequestedTime(time.Now())
	}
	d.st.SetPhase(domain.PhaseInitializing)
	d.kernel.HaltDaemon()
	d.kernel.WakeDaemon()
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
