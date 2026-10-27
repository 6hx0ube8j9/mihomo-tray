package domain

// ================= 文件与目录名称 =================
const (
	AppName            = "Mihomo Tray"
	HostTitle          = "SysTrayHost"
	KernelExeName      = "mihomo.exe"
	RuntimeConfigName  = "config.yaml"
	TrayConfigName     = "mihomo-tray.json"
	TestConfigFileName = "config.test.tmp"
	ProfilesDir        = "profiles"
	WebCacheDir        = "webcache"
	
	// IPCNamedPipe Windows 本地管道
	IPCNamedPipe = `\\.\pipe\{6A1B2C3D-4E5F-7A8B-9C0D-1E2F3A4B5C70}`

	// 单实例互斥体与跨权限唤醒事件
	AppMutexName    = `Local\{6A1B2C3D-4E5F-7A8B-9C0D-1E2F3A4B5C6E}`
	ShowUIEventName = `Local\{6A1B2C3D-4E5F-7A8B-9C0D-1E2F3A4B5C6F}`
)

// ================= 路由模式 =================
const (
	ModeRule   = "rule"
	ModeDirect = "direct"
	ModeGlobal = "global"
)

// ================= Web 面板打开方式 =================
const (
	WebOpenModeSystem   = "system"
	WebOpenModeWebView2 = "webview2"
	WebOpenModeApp      = "app"
)

// ================= 日志文件与时间格式 =================
const (
	LogsDir       = "logs"
	AppLogFile    = "mihomo-tray.log"
	CoreLogFile   = "core.log"
	TimeFormatLog = "2006-01-02 15:04:05"
)

// ================= 网络与本地回环 =================
const (
	LocalhostIP                   = "127.0.0.1"
	DefaultExternalControllerPort = "9090"
)

// ================= 系统主题 =================
type Theme string

const (
	ThemeLight Theme = "light"
	ThemeDark  Theme = "dark"
)

func (t Theme) IsDark() bool {
	return t == ThemeDark
}

func (t Theme) String() string {
	return string(t)
}

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
	DefaultTunDevice           = "Meta"

	// 控制平面默认值
	DefaultExternalController = "127.0.0.1:9090"
	DefaultSecretLength       = 20
	DefaultExternalUI         = "ui"
	DefaultExternalUIURL      = "https://github.com/Zephyruso/zashboard/releases/latest/download/dist.zip"
	DefaultExternalUIName     = "default"

	// 托盘偏好默认值
	DefaultAutostart      = false
	DefaultSystemProxy    = false
	DefaultWebOpenMode    = WebOpenModeWebView2
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
