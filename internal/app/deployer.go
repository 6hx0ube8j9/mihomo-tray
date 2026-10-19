package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"mihomo-tray/internal/config"
	"mihomo-tray/internal/core"
	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/state"
)

var (
	ErrKernelNotRunning = errors.New("内核未运行")
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
	activePath := d.cfg.GetActivePath()
	runtimeAbs, err := d.prepare(activePath)
	if err != nil {
		return err
	}

	if !d.IsKernelRunning() {
		return ErrKernelNotRunning
	}

	if err := d.hotReload(ctx, runtimeAbs); err != nil {
		return fmt.Errorf("内核拒绝热重载: %w", err)
	}

	slog.Debug("配置纯热重载完成", "target", activePath)
	return nil
}

func (d *Deployer) RestartKernel(ctx context.Context) error {
	slog.Info("准备重启内核")
	if _, err := d.prepare(d.cfg.GetActivePath()); err != nil {
		return err
	}
	d.PhysicalRestart(d.cfg.GetConfig())
	return nil
}

func (d *Deployer) DeployAndApply(ctx context.Context, targetRelPath, actionDesc string) error {
	runtimeAbs, err := d.prepare(targetRelPath)
	if err != nil {
		return err
	}

	if d.IsKernelRunning() {
		slog.Debug("执行配置热重载", "action", actionDesc, "target", targetRelPath)
		if err := d.hotReload(ctx, runtimeAbs); err == nil {
			slog.Debug("配置热重载完成", "action", actionDesc, "target", targetRelPath)
			return nil
		}
		slog.Warn("配置热重载受阻，降级物理重启", "action", actionDesc, "target", targetRelPath, "err", err)
	}

	if targetRelPath != "" {
		d.cfg.SetActiveProfile(targetRelPath)
	}

	d.PhysicalRestart(d.cfg.GetConfig())
	return nil
}

func (d *Deployer) SyncRuntimeConfig() error {
	_, err := d.prepare(d.cfg.GetActivePath())
	return err
}

func (d *Deployer) prepare(targetRelPath string) (string, error) {
	if targetRelPath != "" {
		if err := d.cfg.ValidatePhysicalFile(targetRelPath); err != nil {
			return "", fmt.Errorf("配置文件无效: %w", err)
		}
	}

	cfg := d.cfg.GetConfig()
	deployRes, err := core.DeployRuntimeConfig(cfg, targetRelPath, d.cfg.BaseDir())
	if err != nil {
		name := filepath.Base(targetRelPath)
		if name == "." || name == "" {
			name = "空配置"
		}
		return "", fmt.Errorf("%s 生成失败: %w", name, err)
	}

	d.st.SetActualTunDevice(deployRes.TunDevice)
	return filepath.Join(d.cfg.BaseDir(), domain.RuntimeConfigName), nil
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
	return d.api.ForceReloadKernel(ctx, runtimeAbs)
}
