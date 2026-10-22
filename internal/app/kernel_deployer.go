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

var ErrKernelNotRunning = errors.New("内核未运行")

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

func (d *Deployer) IsKernelRunning() bool {
	return d.st.GetPhase() == domain.PhaseRunning && !d.kernel.IsPaused()
}

func (d *Deployer) ReloadConfig(ctx context.Context) error {
	activePath := d.cfg.GetActivePath()
	runtimeAbs, err := d.prepareRuntimeConfig(activePath)
	if err != nil {
		return err
	}

	if !d.IsKernelRunning() {
		return ErrKernelNotRunning
	}

	if err := d.api.ForceReloadKernel(ctx, runtimeAbs); err != nil {
		return fmt.Errorf("内核配置重载失败: %w", err)
	}

	slog.Debug("内核配置重载成功", "target", activePath)
	return nil
}

func (d *Deployer) RestartKernel() error {
	slog.Info("正在重启内核")
	if _, err := d.prepareRuntimeConfig(d.cfg.GetActivePath()); err != nil {
		return err
	}
	d.restartProcess()
	return nil
}

func (d *Deployer) DeployAndApply(ctx context.Context, targetRelPath, actionDesc string) error {
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
		slog.Warn("热重载失败，正在通过重启内核应用配置", "action", actionDesc, "target", targetRelPath, "err", err)
	}

	d.restartProcess()
	return nil
}

func (d *Deployer) SyncRuntimeConfig() error {
	_, err := d.prepareRuntimeConfig(d.cfg.GetActivePath())
	return err
}

func (d *Deployer) prepareRuntimeConfig(targetRelPath string) (string, error) {
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
	return deployRes.RuntimeAbs, nil
}

func (d *Deployer) restartProcess() {
	cfg := d.cfg.GetConfig()
	slog.Debug("重启内核进程", "tun", cfg.Config.Tun.Enable)
	if cfg.Config.Tun.Enable {
		d.st.SetTunRequestedTime(time.Now())
	}
	d.st.SetPhase(domain.PhaseInitializing)
	d.kernel.HaltDaemon()
	d.kernel.WakeDaemon()
}
