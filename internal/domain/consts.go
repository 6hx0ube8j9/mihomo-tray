package domain

// ================= 文件与目录名称 =================
const (
	KernelExeName      = "mihomo.exe"
	RuntimeConfigName  = "config.yaml"
	TrayConfigName     = "mihomo-tray.json"
	TestConfigFileName = "config.test.tmp"
	ProfilesDir        = "profiles"
	WebCacheDir        = "webcache"

	// IPCNamedPipe Windows 本地命名管道
	IPCNamedPipe = `\\.\pipe\mihomo-tray-ipc`
)

// ================= 路由模式 =================
const (
	ModeRule   = "rule"
	ModeDirect = "direct"
	ModeGlobal = "global"
)

// ================= 时间与展示格式 =================
const (
	TimeFormatLog = "2006-01-02 15:04:05"
)

// ================= 内核日志分类标签 =================
const (
	LogTagConfig           = "CONFIG"
	LogTagProfileUpdate    = "PROFILE_UPDATE"
	LogTagKernelTransition = "KERNEL_TRANSITION"
)

const (
    LogsDir       = "logs"
    AppLogFile    = "mihomo-tray.log"
    CoreLogFile   = "core.log"
)

// ================= 网络与本地回环 =================
const (
	LocalhostIP                   = "127.0.0.1"
	DefaultExternalControllerPort = "9090"
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

// ================= 默认配置策略 =================
const (
	// 字符串策略默认值
	DefaultMode         = ModeRule
	DefaultLogLevel     = "info"
	DefaultTrayLogLevel = "info"

	// 端口默认值
	DefaultMixedPort = 7890
	DefaultPort      = 7892
	DefaultSocksPort = 7891

	// 内核参数默认值
	DefaultAllowLan            = true
	DefaultUnifiedDelay        = true
	DefaultAllowPrivateNetwork = true
	DefaultTunEnable           = false

	// 控制平面默认值
	DefaultExternalController = "127.0.0.1:9090"
	DefaultSecretLength       = 20
	DefaultExternalUI         = "ui"
	DefaultExternalUIURL      = "https://github.com/Zephyruso/zashboard/releases/latest/download/dist.zip"
	DefaultExternalUIName     = "default"

	// 托盘偏好默认值
	DefaultAutostart      = false
	DefaultSystemProxy    = false
	DefaultSystemBrowser  = false
	DefaultRemoteWebUI    = false
	DefaultRemoteWebUIURL = "https://board.zash.run.place"

	// 订阅与业务限制
	DefaultUserAgent      = "clash-verge (clash.meta)"
	DefaultUpdateInterval = 3
	MaxProfileCount       = 10
	MaxProfileBytes       = 15 * 1024 * 1024
	MaxUpdateInterval     = 365
)

// DefaultAllowOrigins 默认跨域白名单
var DefaultAllowOrigins = []string{
	"https://board.zash.run.place",
	"https://metacubex.github.io",
	"https://yacd.metacubex.one",
	"https://d.metacubex.one",
}
