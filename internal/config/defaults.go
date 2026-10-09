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

	if !isValidTrayLogLevel(cfg.General.TrayLogLevel) {
		cfg.General.TrayLogLevel = domain.DefaultTrayLogLevel
		isTainted = true
	}

	if cfg.General.RemoteWebUIURL == nil || !netutil.IsValidHTTPURL(*cfg.General.RemoteWebUIURL) {
		url := domain.DefaultRemoteWebUIURL
		cfg.General.RemoteWebUIURL = &url
		isTainted = true
	}

	sanitizePort(&cfg.Config.MixedPort, domain.DefaultMixedPort, &isTainted)
	sanitizePort(&cfg.Config.Port, domain.DefaultPort, &isTainted)
	sanitizePort(&cfg.Config.SocksPort, domain.DefaultSocksPort, &isTainted)

	setIfNil(&cfg.Config.AllowLan, domain.DefaultAllowLan, &isTainted)
	setIfNil(&cfg.Config.UnifiedDelay, domain.DefaultUnifiedDelay, &isTainted)

	if !isValidMode(cfg.Config.Mode) {
		cfg.Config.Mode = domain.DefaultMode
		isTainted = true
	}

	if normalizedLevel, ok := normalizeKernelLogLevel(cfg.Config.LogLevel); ok {
		if cfg.Config.LogLevel != normalizedLevel {
			cfg.Config.LogLevel = normalizedLevel
			isTainted = true
		}
	} else {
		cfg.Config.LogLevel = domain.DefaultLogLevel
		isTainted = true
	}

	if !netutil.IsValidHostPort(cfg.Config.ExternalController) {
		slog.Warn("检测到非法的 ExternalController 监听地址，已自动恢复默认值", "invalid", cfg.Config.ExternalController)
		cfg.Config.ExternalController = domain.DefaultExternalController
		isTainted = true
	}

	setIfEmpty(&cfg.Config.ExternalUI, domain.DefaultExternalUI, &isTainted)
	setIfEmpty(&cfg.Config.ExternalUIName, domain.DefaultExternalUIName, &isTainted)
	setIfNil(&cfg.Config.ExternalUIURL, domain.DefaultExternalUIURL, &isTainted)

	if cfg.Config.ExternalControllerPipe != domain.IPCNamedPipe {
		cfg.Config.ExternalControllerPipe = domain.IPCNamedPipe
		isTainted = true
	}

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

func sanitizePort(portPtr **int, defPort int, tainted *bool) {
	if *portPtr == nil {
		p := defPort
		*portPtr = &p
		*tainted = true
		return
	}
	val := **portPtr
	if !netutil.IsValidPort(val) {
		slog.Warn("端口配置超出合法范围 (0-65535)，已恢复为默认端口", "invalid", val, "default", defPort)
		p := defPort
		*portPtr = &p
		*tainted = true
	}
}

func isValidMode(mode string) bool {
	switch strings.ToLower(mode) {
	case "rule", "global", "direct":
		return true
	default:
		return false
	}
}

func isValidTrayLogLevel(level string) bool {
	switch strings.ToLower(level) {
	case "debug", "info", "warn", "warning", "error", "silent":
		return true
	default:
		return false
	}
}

func normalizeKernelLogLevel(level string) (string, bool) {
	switch strings.ToLower(level) {
	case "debug", "info", "error", "silent":
		return strings.ToLower(level), true
	case "warn", "warning":
		return "warning", true
	default:
		return "", false
	}
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
