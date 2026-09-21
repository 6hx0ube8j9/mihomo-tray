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

const (
	ActionEditCurrentConfig = "EditCurrentConfig"
	ActionCopyWebUIPassword = "CopyWebUIPassword"
	ActionClearWebUICache   = "ClearWebUICache"
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
