package config

import (
	"log/slog"
	"path/filepath"
	"strings"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/netutil"
	"mihomo-tray/internal/random"
)

func applyDefaults(cfg *domain.TrayConfig) bool {
	isTainted := false

	setIfNil(&cfg.General.Autostart, domain.DefaultAutostart, &isTainted)
	setIfNil(&cfg.General.SystemBrowser, domain.DefaultSystemBrowser, &isTainted)
	setIfNil(&cfg.General.RemoteWebUI, domain.DefaultRemoteWebUI, &isTainted)
	setIfNil(&cfg.General.SystemProxy, domain.DefaultSystemProxy, &isTainted)
	setIfEmpty(&cfg.General.TrayLogLevel, domain.DefaultTrayLogLevel, &isTainted)

	if cfg.General.RemoteWebUIURL == nil || *cfg.General.RemoteWebUIURL == "" {
		url := domain.DefaultRemoteWebUIURL
		cfg.General.RemoteWebUIURL = &url
		isTainted = true
	}

	setIfNil(&cfg.Config.MixedPort, domain.DefaultMixedPort, &isTainted)
	setIfNil(&cfg.Config.Port, domain.DefaultPort, &isTainted)
	setIfNil(&cfg.Config.SocksPort, domain.DefaultSocksPort, &isTainted)
	setIfNil(&cfg.Config.AllowLan, domain.DefaultAllowLan, &isTainted)
	setIfNil(&cfg.Config.UnifiedDelay, domain.DefaultUnifiedDelay, &isTainted)

	setIfEmpty(&cfg.Config.Mode, domain.DefaultMode, &isTainted)
	setIfEmpty(&cfg.Config.LogLevel, domain.DefaultLogLevel, &isTainted)
	setIfEmpty(&cfg.Config.ExternalController, domain.DefaultExternalController, &isTainted)
	setIfEmpty(&cfg.Config.ExternalUI, domain.DefaultExternalUI, &isTainted)
	setIfEmpty(&cfg.Config.ExternalUIName, domain.DefaultExternalUIName, &isTainted)

	setIfNil(&cfg.Config.ExternalUIURL, domain.DefaultExternalUIURL, &isTainted)

	cfg.Config.ExternalControllerPipe = domain.IPCNamedPipe
	setIfNil(&cfg.Config.ExternalControllerCors.AllowPrivateNetwork, domain.DefaultAllowPrivateNetwork, &isTainted)
	if cfg.Config.ExternalControllerCors.AllowOrigins == nil {
		cfg.Config.ExternalControllerCors.AllowOrigins = domain.DefaultAllowOrigins
		isTainted = true
	}

	if cfg.Config.Secret == nil {
		s := random.String(domain.DefaultSecretLength)
		cfg.Config.Secret = &s
		isTainted = true
	} else if netutil.IsPublicAddress(cfg.Config.ExternalController) && strings.TrimSpace(*cfg.Config.Secret) == "" {
		s := random.String(domain.DefaultSecretLength)
		cfg.Config.Secret = &s
		isTainted = true
		slog.Warn("安全拦截：已阻止外网无密码监听，系统强制生成随机访问密码")
	}

	var validItems []domain.ProfileItem
	activeFound := false
	for _, item := range cfg.Profiles.Items {
		if filepath.Dir(filepath.ToSlash(item.Path)) != domain.ProfilesDir {
			isTainted = true
			continue
		}
		validItems = append(validItems, item)
		if cfg.Profiles.Active == item.Path {
			activeFound = true
		}
	}
	cfg.Profiles.Items = validItems

	if !activeFound && cfg.Profiles.Active != "" {
		cfg.Profiles.Active = ""
		isTainted = true
	}

	return isTainted
}

func setIfNil[T any](ptr **T, def T, tainted *bool) {
	if *ptr == nil {
		val := def
		*ptr = &val
		*tainted = true
	}
}

func setIfEmpty(val *string, def string, tainted *bool) {
	if *val == "" {
		*val = def
		*tainted = true
	}
}
