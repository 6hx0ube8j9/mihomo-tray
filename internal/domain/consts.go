package domain

// ================= 文件与组件名称 =================
const (
	KernelExeName     = "mihomo.exe"
	RuntimeConfigName = "config.yaml"
	TrayConfigName    = "mihomo-tray.json"

	// IPCNamedPipe 命名管道，底层硬编码
	IPCNamedPipe = `\\.\pipe\mihomo-tray-ipc`
)

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

// ================= 配置强类型 =================
const (
	// 字符串策略默认值
	DefaultMode         = "rule"
	DefaultLogLevel     = "info"
	DefaultTrayLogLevel = "info"

	// 端口默认值
	DefaultMixedPort = 7890
	DefaultPort      = 7892
	DefaultSocksPort = 7891

	// 控制平面默认值
	DefaultExternalController = "127.0.0.1:9090"
	DefaultExternalUI         = "ui"
	DefaultExternalUIURL      = "https://github.com/Zephyruso/zashboard/releases/latest/download/dist.zip"
	DefaultExternalUIName     = ""

	// 托盘常规设置与策略
	DefaultAutostart           = true
	DefaultSystemProxy         = true
	DefaultSystemBrowser       = true
	DefaultAllowLan            = true
	DefaultUnifiedDelay        = true
	DefaultAllowPrivateNetwork = true

	DefaultTunEnable = false

	// 业务环境限制
	DefaultUserAgent      = "clash-verge (clash.meta)"
	DefaultUpdateInterval = 3
	MaxProfileCount       = 10
	MaxProfileBytes       = 15 * 1024 * 1024
	MaxUpdateInterval     = 90
)

// 默认跨域面板白名单
var DefaultAllowOrigins = []string{
	"https://yacd.metacubex.one",
	"https://metacubex.github.io",
	"https://d.metacubex.one",
	"https://board.zash.run.place",
}
