package domain

type KernelStatus struct {
	Mode string `json:"mode"`
	Tun  struct {
		Enable bool   `json:"enable"`
		Device string `json:"device"`
	} `json:"tun"`
}
