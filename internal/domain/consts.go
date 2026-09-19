package domain

// ================= 状态与事件 =================

const AppTaskName = "MihomoTrayTask"

type AppPhase int32

const (
	PhaseInitializing AppPhase = iota
	PhaseRunning
	PhaseExiting
)

type KernelEvent int

const (
	EventKernelReady KernelEvent = iota
	EventKernelExit
)

// ================= 配置与业务规则 =================

const (
	DefaultAutostart          = "false"
	DefaultProxy              = "false"
	DefaultTun                = "false"
	DefaultMode               = "rule"
	DefaultMixedPort          = "7890"
	DefaultExternalController = "127.0.0.1:9090"
	DefaultSecret             = ""
	DefaultExternalUI         = "ui"
	DefaultExternalUIURL      = "https://github.com/Zephyruso/zashboard/releases/latest/download/dist.zip"
	DefaultLogLevel           = "error"
	DefaultUserAgent          = "clash-verge/v1.7.7 clash-meta"

	DefaultUpdateInterval = 3                // 默认订阅更新间隔(天)
	MaxProfileCount       = 10               // 允许导入的最大配置(订阅)数量
	MaxProfileBytes       = 15 * 1024 * 1024 // 限制最大配置文件体积为 15MB
)
