package domain

import (
	"fmt"
	"time"
)

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

type TrayConfig struct {
	Autostart        string        `json:"autostart"`
	RunAsAdmin       string        `json:"run_as_admin"`
	Mode             string        `json:"mode"`
	Proxy            string        `json:"proxy"`
	Tun              string        `json:"tun"`
	TrayLogLevel     string        `json:"tray_log_level"`
	Active           string        `json:"active"`
	UseSystemBrowser string        `json:"use_system_browser,omitempty"`
	AllowLan         string        `json:"allow_lan,omitempty"`
	Items            []ProfileItem `json:"items"`
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
    if diff.Hours() > 24 {
        return fmt.Sprintf("%d 天前", int(diff.Hours()/24))
    } else if diff.Hours() > 1 {
        return fmt.Sprintf("%d 小时前", int(diff.Hours()))
    } else if diff.Minutes() > 1 {
        return fmt.Sprintf("%d 分钟前", int(diff.Minutes()))
    }
    return "刚刚"
}

func (p *ProfileItem) IsUpdateDue() bool {
	if p.URL == "" || p.Interval <= 0 {
		return false
	}
	targetDuration := time.Duration(p.Interval) * 24 * time.Hour
	return time.Since(time.Unix(p.LastUpdate, 0)) >= targetDuration
}
