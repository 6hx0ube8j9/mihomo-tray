package domain

// ================= 文件与目录名称 =================
const (
	AppName             = "Mihomo Tray"
	HostTitle           = "SysTrayHost"
	KernelExeName       = "mihomo.exe"
	RuntimeConfigName   = "config.yaml"
	TrayConfigName      = "mihomo-tray.json"
	TestConfigFileName  = "config.test.tmp"
	ProfilesDir         = "profiles"
	WebCacheDir         = "webcache"
	WebViewCacheDirName = "webview2"

	// IPCNamedPipe Windows 本地管道
	IPCNamedPipe = `\\.\pipe\{B4C72E91-8D3A-4F20-A1E6-7D5832E904F1}`

	// 单实例互斥体与跨权限唤醒事件
	AppMutexName    = `Local\{D8A1F023-3E4B-4A92-9C78-5E2A1B4F8C90}`
	ShowUIEventName = `Local\{F2E9C174-6A58-4D81-8B39-0D4E7F2A5C63}`
)

// ================= 路由模式 =================
const (
	ModeRule   = "rule"
	ModeDirect = "direct"
	ModeGlobal = "global"
)

// ================= Web 面板打开方式 =================
const (
	WebOpenModeAuto    = "auto"
	WebOpenModeApp     = "app"
	WebOpenModeBrowser = "browser"
	WebOpenModeWebView = "webview"
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

func ThemeFromBool(isDark bool) Theme {
	if isDark {
		return ThemeDark
	}
	return ThemeLight
}

func (t Theme) IsDark() bool {
	return t == ThemeDark
}

func (t Theme) String() string {
	return string(t)
}

// ================= 状态与事件 =================
const AppTaskName = "NetTrayAgent"

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
	DefaultTrayLogLevel = "error"

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
	DefaultWebOpenMode    = WebOpenModeAuto
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