package domain

import (
	"fmt"
	"strconv"
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

	GeoAutoUpdate     *bool         `json:"geo-auto-update"`
	GeoUpdateInterval *int          `json:"geo-update-interval"`
	GeoXURL           GeoXURLConfig `json:"geox-url"`

	ExternalController string  `json:"external-controller"`
	Secret             *string `json:"secret"`
	ExternalUI         string  `json:"external-ui"`
	ExternalUIURL      *string `json:"external-ui-url"`
	ExternalUIName     string  `json:"external-ui-name"`

	ExternalControllerPipe string `json:"-"`

	ExternalControllerCors CorsConfig `json:"external-controller-cors"`
	Tun                    TunConfig  `json:"tun"`
}

type GeoXURLConfig struct {
	MMDB    string `json:"mmdb"`
	ASN     string `json:"asn"`
	GeoIP   string `json:"geoip"`
	GeoSite string `json:"geosite"`
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
	AutoUpdate bool   `json:"auto_update"`
	Interval   int    `json:"interval,omitempty"`
	LastUpdate int64  `json:"last_update,omitempty"`
	Upload     int64  `json:"upload,omitempty"`
	Download   int64  `json:"download,omitempty"`
	Total      int64  `json:"total,omitempty"`
	Expire     int64  `json:"expire,omitempty"`
}

type FetchResult struct {
	TempPath           string
	Upload             int64
	Download           int64
	Total              int64
	Expire             int64
	UsedDirectFallback bool
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func effectiveInt(p *int, def int) int {
	if p == nil {
		return def
	}
	return *p
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
	out.Config.GeoAutoUpdate = clonePtr(c.Config.GeoAutoUpdate)
	out.Config.GeoUpdateInterval = clonePtr(c.Config.GeoUpdateInterval)
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

func (c TrayConfig) EffectiveMixedPort() int {
	return effectiveInt(c.Config.MixedPort, DefaultMixedPort)
}

func (c TrayConfig) EffectivePort() int {
	return effectiveInt(c.Config.Port, DefaultPort)
}

func (c TrayConfig) EffectiveSocksPort() int {
	return effectiveInt(c.Config.SocksPort, DefaultSocksPort)
}

func (c TrayConfig) EffectiveSystemProxyPort() (int, bool) {
	if port := c.EffectiveMixedPort(); port > 0 {
		return port, true
	}
	if port := c.EffectivePort(); port > 0 {
		return port, true
	}
	return 0, false
}

func (c TrayConfig) EffectiveSystemProxyPortStr() (string, bool) {
	port, ok := c.EffectiveSystemProxyPort()
	if !ok {
		return "", false
	}
	return strconv.Itoa(port), true
}

func (c TrayConfig) EffectiveSecret() string {
	if c.Config.Secret == nil {
		return ""
	}
	return *c.Config.Secret
}

func (c TrayConfig) EffectiveAllowLan() bool {
	if c.Config.AllowLan == nil {
		return DefaultAllowLan
	}
	return *c.Config.AllowLan
}

func (c TrayConfig) EffectiveExternalController() string {
	if addr := strings.TrimSpace(c.Config.ExternalController); addr != "" {
		return addr
	}
	return DefaultExternalController
}

func (c TrayConfig) EffectiveRemoteWebUIURL() string {
	if c.General.RemoteWebUIURL != nil {
		if u := strings.TrimSpace(*c.General.RemoteWebUIURL); u != "" {
			return u
		}
	}
	return DefaultRemoteWebUIURL
}

func (c TrayConfig) EffectiveWebOpenMode() string {
	if c.General.WebOpenMode == nil {
		return DefaultWebOpenMode
	}
	return NormalizeWebOpenMode(*c.General.WebOpenMode)
}

func (c TrayConfig) IsSystemProxyEnabled() bool {
	return c.General.SystemProxy != nil && *c.General.SystemProxy
}

func (c TrayConfig) IsAutostartEnabled() bool {
	return c.General.Autostart != nil && *c.General.Autostart
}

func (c TrayConfig) IsRemoteWebUIEnabled() bool {
	return c.General.RemoteWebUI != nil && *c.General.RemoteWebUI
}

func (c TrayConfig) ControllerSettings() ControllerSettings {
	var customPath string
	if c.General.CustomBrowserPath != nil {
		customPath = strings.TrimSpace(*c.General.CustomBrowserPath)
	}

	customEnabled := c.General.CustomBrowserEnabled != nil &&
		*c.General.CustomBrowserEnabled &&
		customPath != ""

	return ControllerSettings{
		Addr:                 c.EffectiveExternalController(),
		Secret:               c.EffectiveSecret(),
		RemoteWebUI:          c.IsRemoteWebUIEnabled(),
		RemoteWebUIURL:       c.EffectiveRemoteWebUIURL(),
		WebOpenMode:          c.EffectiveWebOpenMode(),
		CustomBrowserEnabled: customEnabled,
		CustomBrowserPath:    customPath,
	}
}

func TruncateMiddle(name string) string {
	r := []rune(name)
	if len(r) <= 14 {
		return name
	}
	return string(r[:8]) + "..." + string(r[len(r)-6:])
}

func (p ProfileItem) IsRemote() bool {
	return strings.TrimSpace(p.URL) != ""
}

func (p ProfileItem) IsAutoUpdateEnabled() bool {
	return p.IsRemote() && p.AutoUpdate && p.Interval > 0
}

func (p ProfileItem) FormatLastUpdateText() string {
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

func (p ProfileItem) IsUpdateDue() bool {
	if !p.IsAutoUpdateEnabled() {
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
