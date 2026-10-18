package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

type Deployer struct {
	cfg    *config.Manager
	st     *state.RuntimeState
	kernel *core.KernelManager
	api    *core.APIClient
}

func NewDeployer(cfg *config.Manager, st *state.RuntimeState, kernel *core.KernelManager, api *core.APIClient) *Deployer {
	return &Deployer{
		cfg:    cfg,
		st:     st,
		kernel: kernel,
		api:    api,
	}
}

func (d *Deployer) IsKernelRunning() bool {
	return d.st.GetPhase() == domain.PhaseRunning && !d.kernel.IsPaused()
}

func (d *Deployer) ReloadConfig(ctx context.Context) error {
	return d.DeployAndApply(ctx, d.cfg.GetActivePath(), "配置重载")
}

func (d *Deployer) RestartKernel(ctx context.Context) error {
	slog.Info("准备重启内核")
	if err := d.SyncRuntimeConfig(); err != nil {
		return err
	}
	d.PhysicalRestart(d.cfg.GetConfig())
	return nil
}

func (d *Deployer) SyncRuntimeConfig() error {
	activePath := d.cfg.GetActivePath()
	if activePath != "" {
		if err := d.cfg.ValidatePhysicalFile(activePath); err != nil {
			return fmt.Errorf("当前配置无效 | %w", err)
		}
	}

	cfg := d.cfg.GetConfig()
	deployRes, err := core.DeployRuntimeConfig(cfg, activePath, d.cfg.BaseDir())
	if err != nil {
		return fmt.Errorf("生成运行配置失败 | %w", err)
	}

	d.st.SetActualTunDevice(deployRes.TunDevice)
	return nil
}

func (d *Deployer) DeployAndApply(ctx context.Context, targetRelPath, actionDesc string) error {
	if targetRelPath != "" {
		if err := d.cfg.ValidatePhysicalFile(targetRelPath); err != nil {
			return err
		}
	}

	cfg := d.cfg.GetConfig()
	deployRes, err := core.DeployRuntimeConfig(cfg, targetRelPath, d.cfg.BaseDir())
	if err != nil {
		name := filepath.Base(targetRelPath)
		if name == "." || name == "" {
			name = "空配置"
		}
		return fmt.Errorf("%s - %w", name, err)
	}

	d.st.SetActualTunDevice(deployRes.TunDevice)

	if d.IsKernelRunning() {
		slog.Debug("执行热重载", "action", actionDesc)
		runtimeAbs := filepath.Join(d.cfg.BaseDir(), domain.RuntimeConfigName)
		if err := d.hotReload(ctx, runtimeAbs); err == nil {
			slog.Debug("热重载成功", "target", targetRelPath)
			return nil
		}
		slog.Warn("重载配置失败，准备重启内核")
	}

	d.PhysicalRestart(cfg)
	return nil
}

func (d *Deployer) PhysicalRestart(cfg domain.TrayConfig) {
	slog.Debug("重启内核", "tun", cfg.Config.Tun.Enable)
	if cfg.Config.Tun.Enable {
		d.st.SetTunRequestedTime(time.Now())
	}
	d.st.SetPhase(domain.PhaseInitializing)
	d.kernel.HaltDaemon()
	d.kernel.WakeDaemon()
}

func (d *Deployer) hotReload(ctx context.Context, runtimeAbs string) error {
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if err := d.api.ForceReloadKernel(reqCtx, runtimeAbs); err != nil {
		return err
	}

	time.Sleep(200 * time.Millisecond)
	cfg := d.cfg.GetConfig()
	_ = d.api.SyncAllRuntime(
		ctx,
		cfg.Config.Mode,
		d.cfg.GetEffectiveAllowLan(),
		cfg.Config.Tun.Enable,
		d.st.GetActualTunDevice(),
	)
	return nil
}
