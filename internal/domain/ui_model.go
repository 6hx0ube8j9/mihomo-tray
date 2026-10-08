package domain

type IconState int

const (
	IconStop IconState = iota
	IconError
	IconTun
	IconProxy
	IconDefault
)

type UICommand struct {
	Action  string
	Payload string
}

// ================= UI Action Protocol =================
const (
	// 配置管理操作
	ActionOpenProfileManager  = "OpenProfileManager"
	ActionRequestAddLocal     = "RequestAddLocalProfile"
	ActionRequestAddRemote    = "RequestAddRemoteProfile"
	ActionEditProfileInfo     = "EditProfileInfo"
	ActionSetProfileInterval  = "SetProfileInterval"
	ActionUpdateRemoteProfile = "UpdateRemoteProfile"
	ActionSwitchProfile       = "SwitchProfile"
	ActionRemoveProfile       = "RemoveProfile"
	ActionMoveProfileUp       = "MoveProfileUp"
	ActionMoveProfileDown     = "MoveProfileDown"
	ActionOpenConfigFile      = "OpenConfigFile"
	ActionEditCurrentConfig   = "EditCurrentConfig"

	// 弹窗编辑器请求
	ActionRequestEditPort       = "RequestEditPort"
	ActionRequestEditController = "RequestEditController"

	// 内核网络与模式控制
	ActionToggleTun      = "ToggleTun"
	ActionToggleProxy    = "ToggleProxy"
	ActionSwitchMode     = "SwitchMode"
	ActionToggleAllowLan = "ToggleAllowLan"
	ActionForceSyncAPI   = "ForceSyncAPI"

	// 系统与生命周期
	ActionOpenBaseDir      = "OpenBaseDir"
	ActionOpenAppConfig    = "OpenAppConfig"
	ActionToggleAutoStart  = "ToggleAutoStart"
	ActionToggleRunAsAdmin = "ToggleRunAsAdmin"
	ActionReloadConfig     = "ReloadConfig"
	ActionRestartKernel    = "RestartKernel"
	ActionExitApp          = "ExitApp"

	// Web 面板偏好
	ActionToggleSystemBrowser = "ToggleSystemBrowser"
	ActionToggleRemoteWebUI   = "ToggleRemoteWebUI"
	ActionOpenWebUI           = "OpenWebUI"
	ActionCopyWebUIPassword   = "CopyWebUIPassword"
	ActionClearWebUICache     = "ClearWebUICache"
)

type UIProfileItem struct {
	Name       string
	Path       string
	IsActive   bool
	IsRemote   bool
	Interval   int
	LastUpdate string
}

type UIState struct {
	IconState        IconState
	IsTun            bool
	IsProxy          bool
	Mode             string
	AutoStart        bool
	IsAdmin          bool
	RunAsAdmin       bool
	UseSystemBrowser bool
	RemoteWebUI      bool
	AllowLan         bool
	ProfileItems     []UIProfileItem
	CanAddProfile    bool
	MixedPort        int
	SocksPort        int
	HttpPort         int
}
