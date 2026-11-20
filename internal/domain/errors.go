package domain

import "errors"

var (
	// 运行时与内核状态
	ErrKernelNotReady = errors.New("内核尚未就绪")
	ErrSystemBusy     = errors.New("系统繁忙，请稍候重试")
	ErrTunDeviceBusy  = errors.New("虚拟网卡已被其他程序占用")

	// 代理与网络配置
	ErrNoProxyPort = errors.New("未检测到有效代理端口")

	// 用户配置与输入
	ErrInvalidPort    = errors.New("端口超出合法范围 (0-65535)")
	ErrProfileInvalid = errors.New("配置文件语法错误或格式不正确")
)
