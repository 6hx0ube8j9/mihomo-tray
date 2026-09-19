package domain

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
	Autostart    string        `json:"autostart"`
	RunAsAdmin   string        `json:"run_as_admin"`
	Mode         string        `json:"mode"`
	Proxy        string        `json:"proxy"`
	Tun          string        `json:"tun"`
	TrayLogLevel string        `json:"tray_log_level"`
	Active       string        `json:"active"`
	Items        []ProfileItem `json:"items"`
}

type FetchResult struct {
	TempPath string
	Upload   int64
	Download int64
	Total    int64
	Expire   int64
}
