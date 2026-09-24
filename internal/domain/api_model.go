package domain

type KernelStatus struct {
	Mode         string `json:"mode"`
	LogLevel     string `json:"log-level"`
	AllowLan     bool   `json:"allow-lan"`
	UnifiedDelay bool   `json:"unified-delay"`
	MixedPort    int    `json:"mixed-port"`
	Port         int    `json:"port"`
	SocksPort    int    `json:"socks-port"`
	Tun          struct {
		Enable bool   `json:"enable"`
		Device string `json:"device"`
	} `json:"tun"`
}
