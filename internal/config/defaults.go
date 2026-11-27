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

	if gen.WebOpenMode == nil {
		mode := domain.DefaultWebOpenMode
		gen.WebOpenMode = &mode
		*isTainted = true
	} else {
		normalized := domain.NormalizeWebOpenMode(*gen.WebOpenMode)
		if normalized != *gen.WebOpenMode {
			gen.WebOpenMode = &normalized
			*isTainted = true
		}
	}

	sanitizeTrimmedString(&gen.CustomBrowserPath, "", isTainted)

	if *gen.CustomBrowserEnabled && *gen.CustomBrowserPath == "" {
		*gen.CustomBrowserEnabled = false
		*isTainted = true
	}

	if normalizedLevel, ok := normalizeTrayLogLevel(gen.TrayLogLevel); ok {
		if gen.TrayLogLevel != normalizedLevel {
			gen.TrayLogLevel = normalizedLevel
			*isTainted = true
		}
	} else {
		gen.TrayLogLevel = domain.DefaultTrayLogLevel
		*isTainted = true
	}

	if gen.RemoteWebUIURL == nil || !netutil.IsValidHTTPURL(strings.TrimSpace(*gen.RemoteWebUIURL)) {
		url := domain.DefaultRemoteWebUIURL
		gen.RemoteWebUIURL = &url
		*isTainted = true
	} else if trimmed := strings.TrimSpace(*gen.RemoteWebUIURL); trimmed != *gen.RemoteWebUIURL {
		gen.RemoteWebUIURL = &trimmed
		*isTainted = true
	}
}

func applyKernelDefaults(kcfg *domain.KernelConfig, isTainted *bool) {
	sanitizePort(&kcfg.MixedPort, domain.DefaultMixedPort, isTainted)
	sanitizePort(&kcfg.Port, domain.DefaultPort, isTainted)
	sanitizePort(&kcfg.SocksPort, domain.DefaultSocksPort, isTainted)

	setIfNil(&kcfg.AllowLan, domain.DefaultAllowLan, isTainted)
	setIfNil(&kcfg.UnifiedDelay, domain.DefaultUnifiedDelay, isTainted)

	setIfNil(&kcfg.GeoAutoUpdate, domain.DefaultGeoAutoUpdate, isTainted)
	sanitizeGeoUpdateInterval(&kcfg.GeoUpdateInterval, domain.DefaultGeoUpdateInterval, isTainted)
	sanitizeGeoURL(&kcfg.GeoXURL.MMDB, domain.DefaultGeoXURLMMDB, isTainted)
	sanitizeGeoURL(&kcfg.GeoXURL.ASN, domain.DefaultGeoXURLASN, isTainted)
	sanitizeGeoURL(&kcfg.GeoXURL.GeoIP, domain.DefaultGeoXURLGeoIP, isTainted)
	sanitizeGeoURL(&kcfg.GeoXURL.GeoSite, domain.DefaultGeoXURLGeoSite, isTainted)

	if normalizedMode, ok := normalizeMode(kcfg.Mode); ok {
		if kcfg.Mode != normalizedMode {
			kcfg.Mode = normalizedMode
			*isTainted = true
		}
	} else {
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

	trimmedCtrl := strings.TrimSpace(kcfg.ExternalController)
	if !netutil.IsValidHostPort(trimmedCtrl) {
		if trimmedCtrl != "" {
			slog.Warn("控制面板监听地址非法，已恢复默认", "invalid", trimmedCtrl)
		}
		kcfg.ExternalController = domain.DefaultExternalController
		*isTainted = true
	} else if trimmedCtrl != kcfg.ExternalController {
		kcfg.ExternalController = trimmedCtrl
		*isTainted = true
	}

	setIfEmpty(&kcfg.ExternalUI, domain.DefaultExternalUI, isTainted)
	setIfEmpty(&kcfg.ExternalUIName, domain.DefaultExternalUIName, isTainted)
	setIfNil(&kcfg.ExternalUIURL, domain.DefaultExternalUIURL, isTainted)

	kcfg.ExternalControllerPipe = domain.IPCNamedPipe

	setIfNil(&kcfg.ExternalControllerCors.AllowPrivateNetwork, domain.DefaultAllowPrivateNetwork, isTainted)
	if kcfg.ExternalControllerCors.AllowOrigins == nil {
		kcfg.ExternalControllerCors.AllowOrigins = append([]string(nil), domain.DefaultAllowOrigins...)
		*isTainted = true
	}

	if kcfg.Secret == nil {
		s := random.String(domain.DefaultSecretLength)
		kcfg.Secret = &s
		*isTainted = true
	} else {
		trimmedSecret := strings.TrimSpace(*kcfg.Secret)
		if !domain.IsValidSecret(trimmedSecret) {
			s := random.String(domain.DefaultSecretLength)
			kcfg.Secret = &s
			*isTainted = true
			slog.Warn("接口访问密码包含非法字符，已自动重新生成")
		} else if netutil.IsPublicAddress(kcfg.ExternalController) && trimmedSecret == "" {
			s := random.String(domain.DefaultSecretLength)
			kcfg.Secret = &s
			*isTainted = true
			slog.Warn("外网监听未设置密码，已自动生成随机密码")
		} else if trimmedSecret != *kcfg.Secret {
			kcfg.Secret = &trimmedSecret
			*isTainted = true
		}
	}
}

func applyProfileDefaults(prof *domain.ProfileManager, isTainted *bool) {
	if prof.Items == nil {
		prof.Items = []domain.ProfileItem{}
		*isTainted = true
	}

	validItems := make([]domain.ProfileItem, 0, len(prof.Items))
	activeFound := false
	for _, item := range prof.Items {
		if filepath.Dir(filepath.ToSlash(item.Path)) != domain.ProfilesDir {
			*isTainted = true
			continue
		}
		sanitizeProfileItem(&item, isTainted)
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

func sanitizeProfileItem(item *domain.ProfileItem, tainted *bool) {
	trimmedName := strings.TrimSpace(item.Name)
	if trimmedName == "" {
		base := filepath.Base(item.Path)
		trimmedName = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if trimmedName != item.Name {
		item.Name = trimmedName
		*tainted = true
	}

	trimmedURL := strings.TrimSpace(item.URL)
	if trimmedURL != item.URL {
		item.URL = trimmedURL
		*tainted = true
	}

	if item.URL == "" {
		if item.AutoUpdate || item.Interval != 0 || item.LastUpdate != 0 ||
			item.Upload != 0 || item.Download != 0 || item.Total != 0 || item.Expire != 0 {
			item.AutoUpdate = false
			item.Interval = 0
			item.LastUpdate = 0
			item.Upload = 0
			item.Download = 0
			item.Total = 0
			item.Expire = 0
			*tainted = true
		}
		return
	}

	if item.Interval < 0 {
		item.Interval = 0
		*tainted = true
	} else if item.Interval > domain.MaxUpdateInterval {
		item.Interval = domain.MaxUpdateInterval
		*tainted = true
	}

	if item.AutoUpdate && item.Interval == 0 {
		item.AutoUpdate = false
		*tainted = true
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
		slog.Warn("端口配置非法，已恢复默认", "invalid", val, "default", defPort)
		p := defPort
		*portPtr = &p
		*tainted = true
	}
}

func sanitizeGeoUpdateInterval(intervalPtr **int, def int, tainted *bool) {
	if *intervalPtr == nil || **intervalPtr <= 0 {
		if *intervalPtr != nil {
			slog.Warn("Geo 更新间隔非法，已恢复默认", "invalid", **intervalPtr, "default", def)
		}
		v := def
		*intervalPtr = &v
		*tainted = true
	}
}

func sanitizeGeoURL(val *string, def string, tainted *bool) {
	trimmed := strings.TrimSpace(*val)
	if trimmed == "" || !netutil.IsValidHTTPURL(trimmed) {
		if trimmed != "" {
			slog.Warn("GeoX 下载地址非法，已恢复默认", "invalid", trimmed)
		}
		*val = def
		*tainted = true
		return
	}
	if trimmed != *val {
		*val = trimmed
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

func normalizeMode(mode string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case domain.ModeRule:
		return domain.ModeRule, true
	case domain.ModeGlobal:
		return domain.ModeGlobal, true
	case domain.ModeDirect:
		return domain.ModeDirect, true
	default:
		return "", false
	}
}

func normalizeTrayLogLevel(level string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return "debug", true
	case "info":
		return "info", true
	case "warn", "warning":
		return "warn", true
	case "error":
		return "error", true
	case "silent":
		return "silent", true
	default:
		return "", false
	}
}

func normalizeKernelLogLevel(level string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return "debug", true
	case "info":
		return "info", true
	case "warn", "warning":
		return "warning", true
	case "error":
		return "error", true
	case "silent":
		return "silent", true
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
