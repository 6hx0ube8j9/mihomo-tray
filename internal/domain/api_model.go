package domain

type KernelStatus struct {
	Mode string `json:"mode"`
	AllowLan bool   `json:"allow-lan"`
	Tun  struct {
		Enable bool   `json:"enable"`
		Device string `json:"device"`
	} `json:"tun"`
}
