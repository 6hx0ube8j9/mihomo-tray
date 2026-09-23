package domain

import (
	"fmt"
	"time"
)

type TrayConfig struct {
	General  GeneralConfig  `json:"general"`
	Config   KernelConfig   `json:"config"`
	Profiles ProfileManager `json:"profiles"`
}

type GeneralConfig struct {
	Autostart     *bool  `json:"autostart"`
	RunAsAdmin    bool   `json:"run_as_admin"`
	SystemBrowser *bool  `json:"system_browser"`
	TrayLogLevel  string `json:"tray_log_level"`
	SystemProxy   *bool  `json:"system_proxy"`
}

type KernelConfig struct {
	MixedPort *int `json:"mixed-port"`
	Port      *int `json:"port"`
	SocksPort *int `json:"socks-port"`

	Mode         string `json:"mode"`
	LogLevel     string `json:"log-level"`
	AllowLan     *bool  `json:"allow-lan"`
	UnifiedDelay *bool  `json:"unified-delay"`

	ExternalController string `json:"external-controller"`
	Secret             string `json:"secret"`
	ExternalUI         string `json:"external-ui"`
	ExternalUIURL      string `json:"external-ui-url"`
	ExternalUIName     string `json:"external-ui-name"`

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

// 订阅与本地配置资产管理 (保持原样)

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

func (p *ProfileItem) FormatLastUpdateText() string {
	if p.LastUpdate == 0 {
		return "从未更新"
	}
	diff := time.Since(time.Unix(p.LastUpdate, 0))
	if diff.Hours() > 24 { return fmt.Sprintf("%d 天前", int(diff.Hours()/24)) }
	if diff.Hours() > 1 { return fmt.Sprintf("%d 小时前", int(diff.Hours())) }
	if diff.Minutes() > 1 { return fmt.Sprintf("%d 分钟前", int(diff.Minutes())) }
	return "刚刚"
}

func (p *ProfileItem) IsUpdateDue() bool {
	if p.URL == "" || p.Interval <= 0 { return false }
	targetDuration := time.Duration(p.Interval) * 24 * time.Hour
	return time.Since(time.Unix(p.LastUpdate, 0)) >= targetDuration
}
