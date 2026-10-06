package config

import (
	"path/filepath"
	"strings"
	"log/slog"

	"mihomo-tray/internal/domain"
	"mihomo-tray/internal/netutil"
	"mihomo-tray/internal/random"
)

func applyDefaults(cfg *domain.TrayConfig) bool {
	isTainted := false

	if cfg.General.Autostart == nil { t := domain.DefaultAutostart; cfg.General.Autostart = &t; isTainted = true }
	if cfg.General.SystemBrowser == nil { t := domain.DefaultSystemBrowser; cfg.General.SystemBrowser = &t; isTainted = true }
	if cfg.General.RemoteWebUI == nil { t := domain.DefaultRemoteWebUI; cfg.General.RemoteWebUI = &t; isTainted = true }
	if cfg.General.RemoteWebUIURL == nil || *cfg.General.RemoteWebUIURL == "" {
		s := domain.DefaultRemoteWebUIURL
		cfg.General.RemoteWebUIURL = &s
		isTainted = true
	}	
	if cfg.General.SystemProxy == nil { t := domain.DefaultSystemProxy; cfg.General.SystemProxy = &t; isTainted = true }
	if cfg.General.TrayLogLevel == "" { cfg.General.TrayLogLevel = domain.DefaultTrayLogLevel; isTainted = true }

	if cfg.Config.MixedPort == nil { v := domain.DefaultMixedPort; cfg.Config.MixedPort = &v; isTainted = true }
	if cfg.Config.Port == nil { v := domain.DefaultPort; cfg.Config.Port = &v; isTainted = true }
	if cfg.Config.SocksPort == nil { v := domain.DefaultSocksPort; cfg.Config.SocksPort = &v; isTainted = true }

	if cfg.Config.Mode == "" { cfg.Config.Mode = domain.DefaultMode; isTainted = true }
	if cfg.Config.LogLevel == "" { cfg.Config.LogLevel = domain.DefaultLogLevel; isTainted = true }
	if cfg.Config.AllowLan == nil { t := domain.DefaultAllowLan; cfg.Config.AllowLan = &t; isTainted = true }
	if cfg.Config.UnifiedDelay == nil { t := domain.DefaultUnifiedDelay; cfg.Config.UnifiedDelay = &t; isTainted = true }
	
	if cfg.Config.ExternalController == "" { 
		cfg.Config.ExternalController = domain.DefaultExternalController
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

	if cfg.Config.ExternalUIURL == nil { 
		s := domain.DefaultExternalUIURL
		cfg.Config.ExternalUIURL = &s
		isTainted = true 
	}
	
	if cfg.Config.ExternalUI == "" { 
		cfg.Config.ExternalUI = domain.DefaultExternalUI
		isTainted = true 
	}	

	if cfg.Config.ExternalUIName == "" {
		cfg.Config.ExternalUIName = domain.DefaultExternalUIName 
		isTainted = true
	}

	cfg.Config.ExternalControllerPipe = domain.IPCNamedPipe

	if cfg.Config.ExternalControllerCors.AllowOrigins == nil {
		cfg.Config.ExternalControllerCors.AllowOrigins = domain.DefaultAllowOrigins
		isTainted = true
	}
	if cfg.Config.ExternalControllerCors.AllowPrivateNetwork == nil {
		t := domain.DefaultAllowPrivateNetwork
		cfg.Config.ExternalControllerCors.AllowPrivateNetwork = &t
		isTainted = true
	}

	var validItems []domain.ProfileItem
	activeFound := false
	for _, item := range cfg.Profiles.Items {
		if filepath.Dir(filepath.ToSlash(item.Path)) != ProfilesDir {
			isTainted = true
			continue
		}
		validItems = append(validItems, item)
		if cfg.Profiles.Active == item.Path { activeFound = true }
	}
	cfg.Profiles.Items = validItems
	if !activeFound && cfg.Profiles.Active != "" {
		cfg.Profiles.Active = ""
		isTainted = true
	}

	return isTainted
}
