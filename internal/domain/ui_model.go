package domain

const (
	IconStop = iota
	IconError
	IconTun
	IconProxy
	IconDefault
)

type UICommand struct {
	Action  string
	Payload string
}

// ================= UI Protocol =================
const (
	ActionOpenProfileManager  = "OpenProfileManager"
	ActionRequestAddLocal     = "RequestAddLocalProfile"
	ActionRequestAddRemote    = "RequestAddRemoteProfile"
	ActionRequestEditRemote   = "RequestEditRemoteProfile"
	ActionAddLocalProfile     = "AddLocalProfile"
	ActionAddRemoteProfile    = "AddRemoteProfile"
	ActionSetProfileInterval  = "SetProfileInterval"
	ActionUpdateRemoteProfile = "UpdateRemoteProfile"
	ActionSwitchProfile       = "SwitchProfile"
	ActionRemoveProfile       = "RemoveProfile"
	ActionMoveProfileUp       = "MoveProfileUp"
	ActionMoveProfileDown     = "MoveProfileDown"
	ActionToggleAutoStart     = "ToggleAutoStart"
	ActionToggleRunAsAdmin    = "ToggleRunAsAdmin"
	ActionToggleTun           = "ToggleTun"
	ActionToggleProxy         = "ToggleProxy"
	ActionSwitchMode          = "SwitchMode"
	ActionForceSyncAPI        = "ForceSyncAPI"
	ActionOpenWebUI           = "OpenWebUI"
	ActionOpenBaseDir         = "OpenBaseDir"
	ActionReloadConfig        = "ReloadConfig"
	ActionRestartKernel       = "RestartKernel"
	ActionOpenConfigFile      = "OpenConfigFile"
	ActionExitApp             = "ExitApp"
	ActionToggleSystemBrowser = "ToggleSystemBrowser"
	ActionEditCurrentConfig   = "EditCurrentConfig"
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
	IconState        int
	IsTun            bool
	IsProxy          bool
	Mode             string
	AutoStart        bool
	IsAdmin          bool
	RunAsAdmin       bool
	UseSystemBrowser bool
	ProfileItems     []UIProfileItem
	CanAddProfile    bool
}
