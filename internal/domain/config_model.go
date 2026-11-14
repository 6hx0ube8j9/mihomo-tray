package domain

import (
	"fmt"
	"strings"
	"time"
)

type TrayConfig struct {
	General  GeneralConfig  `json:"general"`
	Config   KernelConfig   `json:"config"`
	Profiles ProfileManager `json:"profiles"`
}

type GeneralConfig struct {
	Autostart            *bool   `json:"autostart"`
	RunAsAdmin           bool    `json:"run_as_admin"`
	WebOpenMode          *string `json:"web_open_mode"`
	RemoteWebUI          *bool   `json:"remote_webui"`
	RemoteWebUIURL       *string `json:"remote_webui_url"`
	CustomBrowserEnabled *bool   `json:"custom_browser_enabled"`
	CustomBrowserPath    *string `json:"custom_browser_path"`
	TrayLogLevel         string  `json:"tray_log_level"`
	SystemProxy          *bool   `json:"system_proxy"`
}

type KernelConfig struct {
	MixedPort *int `json:"mixed-port"`
	Port      *int `json:"port"`
	SocksPort *int `json:"socks-port"`

	Mode         string `json:"mode"`
	LogLevel     string `json:"log-level"`
	AllowLan     *bool  `json:"allow-lan"`
	UnifiedDelay *bool  `json:"unified-delay"`

	ExternalController string  `json:"external-controller"`
	Secret             *string `json:"secret"`
	ExternalUI         string  `json:"external-ui"`
	ExternalUIURL      *string `json:"external-ui-url"`
	ExternalUIName     string  `json:"external-ui-name"`

	ExternalControllerPipe string `json:"-"`

	ExternalControllerCors CorsConfig `json:"external-controller-cors"`
	Tun                    TunConfig  `json:"tun"`
}

type CorsConfig struct {
	AllowPrivateNetwork *bool    `json:"allow-private-network"`
	AllowOrigins        []string `json:"allow-origins"`
}

type TunConfig struct {
	Enable bool `json:"enable"`
}

type ProfileManager struct {
	Active string        `json:"active"`
	Items  []ProfileItem `json:"items"`
}

type ProfileItem struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	URL        string `json:"url,omitempty"`
	AutoUpdate bool   `json:"auto_update,omitempty"`
	Interval   int    `json:"interval,omitempty"`
	LastUpdate int64  `json:"last_update,omitempty"`
	Upload     int64  `json:"upload,omitempty"`
	Download   int64  `json:"download,omitempty"`
	Total      int64  `json:"total,omitempty"`
	Expire     int64  `json:"expire,omitempty"`
}

type FetchResult struct {
	TempPath string
	Upload   int64
	Download int64
	Total    int64
	Expire   int64
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func (c TrayConfig) Clone() TrayConfig {
	out := c

	out.General.Autostart = clonePtr(c.General.Autostart)
	out.General.WebOpenMode = clonePtr(c.General.WebOpenMode)
	out.General.RemoteWebUI = clonePtr(c.General.RemoteWebUI)
	out.General.RemoteWebUIURL = clonePtr(c.General.RemoteWebUIURL)
	out.General.CustomBrowserEnabled = clonePtr(c.General.CustomBrowserEnabled)
	out.General.CustomBrowserPath = clonePtr(c.General.CustomBrowserPath)
	out.General.SystemProxy = clonePtr(c.General.SystemProxy)

	out.Config.MixedPort = clonePtr(c.Config.MixedPort)
	out.Config.Port = clonePtr(c.Config.Port)
	out.Config.SocksPort = clonePtr(c.Config.SocksPort)
	out.Config.AllowLan = clonePtr(c.Config.AllowLan)
	out.Config.UnifiedDelay = clonePtr(c.Config.UnifiedDelay)
	out.Config.Secret = clonePtr(c.Config.Secret)
	out.Config.ExternalUIURL = clonePtr(c.Config.ExternalUIURL)
	out.Config.ExternalControllerCors.AllowPrivateNetwork = clonePtr(c.Config.ExternalControllerCors.AllowPrivateNetwork)

	if c.Config.ExternalControllerCors.AllowOrigins != nil {
		origins := make([]string, len(c.Config.ExternalControllerCors.AllowOrigins))
		copy(origins, c.Config.ExternalControllerCors.AllowOrigins)
		out.Config.ExternalControllerCors.AllowOrigins = origins
	}

	if c.Profiles.Items != nil {
		items := make([]ProfileItem, len(c.Profiles.Items))
		copy(items, c.Profiles.Items)
		out.Profiles.Items = items
	}

	return out
}

func TruncateMiddle(name string) string {
	r := []rune(name)
	if len(r) <= 14 {
		return name
	}
	return string(r[:8]) + "..." + string(r[len(r)-6:])
}

func (p *ProfileItem) FormatLastUpdateText() string {
	if p.LastUpdate <= 0 {
		return "从未更新"
	}

	diff := time.Since(time.Unix(p.LastUpdate, 0))
	if diff < time.Minute {
		return "刚刚"
	}

	if days := int(diff / (24 * time.Hour)); days >= 1 {
		return fmt.Sprintf("%d 天前", days)
	}
	if hours := int(diff / time.Hour); hours >= 1 {
		return fmt.Sprintf("%d 小时前", hours)
	}
	return fmt.Sprintf("%d 分钟前", int(diff/time.Minute))
}

func (p *ProfileItem) IsUpdateDue() bool {
	if p.URL == "" || p.Interval <= 0 {
		return false
	}
	targetDuration := time.Duration(p.Interval) * 24 * time.Hour
	return time.Since(time.Unix(p.LastUpdate, 0)) >= targetDuration
}

// IsValidSecret reports whether s contains only printable ASCII characters without spaces (33-126).
func IsValidSecret(s string) bool {
	if s == "" {
		return true
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

func IsValidWebOpenMode(mode string) bool {
	switch mode {
	case WebOpenModeAuto, WebOpenModeApp, WebOpenModeBrowser, WebOpenModeWebView:
		return true
	default:
		return false
	}
}

func NormalizeWebOpenMode(raw string) string {
	mode := strings.ToLower(strings.TrimSpace(raw))
	if IsValidWebOpenMode(mode) {
		return mode
	}
	return DefaultWebOpenMode
}