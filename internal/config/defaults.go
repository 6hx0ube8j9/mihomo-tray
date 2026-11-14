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

	applyGeneralDefaults(&cfg.General, &isTainted)
	applyKernelDefaults(&cfg.Config, &isTainted)
	applyProfileDefaults(&cfg.Profiles, &isTainted)

	return isTainted
}

func applyGeneralDefaults(gen *domain.GeneralConfig, isTainted *bool) {
	setIfNil(&gen.Autostart, domain.DefaultAutostart, isTainted)
	setIfNil(&gen.RemoteWebUI, domain.DefaultRemoteWebUI, isTainted)
	setIfNil(&gen.CustomBrowserEnabled, domain.DefaultCustomBrowserEnabled, isTainted)
	setIfNil(&gen.SystemProxy, domain.DefaultSystemProxy, isTainted)

	if gen.WebOpenMode == nil || !domain.IsValidWebOpenMode(*gen.WebOpenMode) {
		mode := domain.DefaultWebOpenMode
		gen.WebOpenMode = &mode
		*isTainted = true
	}

	sanitizeTrimmedString(&gen.CustomBrowserPath, "", isTainted)

	if !isValidTrayLogLevel(gen.TrayLogLevel) {
		gen.TrayLogLevel = domain.DefaultTrayLogLevel
		*isTainted = true
	}

	if gen.RemoteWebUIURL == nil || !netutil.IsValidHTTPURL(*gen.RemoteWebUIURL) {
		url := domain.DefaultRemoteWebUIURL
		gen.RemoteWebUIURL = &url
		*isTainted = true
	}
}

func applyKernelDefaults(kcfg *domain.KernelConfig, isTainted *bool) {
	sanitizePort(&kcfg.MixedPort, domain.DefaultMixedPort, isTainted)
	sanitizePort(&kcfg.Port, domain.DefaultPort, isTainted)
	sanitizePort(&kcfg.SocksPort, domain.DefaultSocksPort, isTainted)

	setIfNil(&kcfg.AllowLan, domain.DefaultAllowLan, isTainted)
	setIfNil(&kcfg.UnifiedDelay, domain.DefaultUnifiedDelay, isTainted)

	if !isValidMode(kcfg.Mode) {
		kcfg.Mode = domain.DefaultMode
		*isTainted = true
	}

	if normalizedLevel, ok := normalizeKernelLogLevel(kcfg.LogLevel); ok {
		if kcfg.LogLevel != normalizedLevel {
			kcfg.LogLevel = normalizedLevel
			*isTainted = true
		}
	} else {
		kcfg.LogLevel = domain.DefaultLogLevel
		*isTainted = true
	}

	if !netutil.IsValidHostPort(kcfg.ExternalController) {
		slog.Warn("检测到非法的 ExternalController 监听地址，已自动恢复默认值", "invalid", kcfg.ExternalController)
		kcfg.ExternalController = domain.DefaultExternalController
		*isTainted = true
	}

	setIfEmpty(&kcfg.ExternalUI, domain.DefaultExternalUI, isTainted)
	setIfEmpty(&kcfg.ExternalUIName, domain.DefaultExternalUIName, isTainted)
	setIfNil(&kcfg.ExternalUIURL, domain.DefaultExternalUIURL, isTainted)

	if kcfg.ExternalControllerPipe != domain.IPCNamedPipe {
		kcfg.ExternalControllerPipe = domain.IPCNamedPipe
		*isTainted = true
	}

	setIfNil(&kcfg.ExternalControllerCors.AllowPrivateNetwork, domain.DefaultAllowPrivateNetwork, isTainted)
	if kcfg.ExternalControllerCors.AllowOrigins == nil {
		kcfg.ExternalControllerCors.AllowOrigins = append([]string(nil), domain.DefaultAllowOrigins...)
		*isTainted = true
	}

	if kcfg.Secret == nil {
		s := random.String(domain.DefaultSecretLength)
		kcfg.Secret = &s
		*isTainted = true
	} else if netutil.IsPublicAddress(kcfg.ExternalController) && strings.TrimSpace(*kcfg.Secret) == "" {
		s := random.String(domain.DefaultSecretLength)
		kcfg.Secret = &s
		*isTainted = true
		slog.Warn("安全拦截：已阻止外网无密码监听，系统强制生成随机访问密码")
	}
}

func applyProfileDefaults(prof *domain.ProfileManager, isTainted *bool) {
	var validItems []domain.ProfileItem
	activeFound := false
	for _, item := range prof.Items {
		if filepath.Dir(filepath.ToSlash(item.Path)) != domain.ProfilesDir {
			*isTainted = true
			continue
		}
		validItems = append(validItems, item)
		if prof.Active == item.Path {
			activeFound = true
		}
	}
	prof.Items = validItems

	if !activeFound && prof.Active != "" {
		prof.Active = ""
		*isTainted = true
	}
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

func sanitizeTrimmedString(ptr **string, def string, tainted *bool) {
	if *ptr == nil {
		val := def
		*ptr = &val
		*tainted = true
		return
	}
	trimmed := strings.TrimSpace(**ptr)
	if trimmed != **ptr {
		*ptr = &trimmed
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