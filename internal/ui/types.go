package ui

type UICommand struct {
	Action  string
	Payload string
}

type ProfileItem struct {
	Name       string
	Path       string
	IsActive   bool
	IsRemote   bool
	Interval   int
	LastUpdate string
}

type UIState struct {
	IconState     int
	IsTun         bool
	IsProxy       bool
	Mode          string
	AutoStart     bool
	IsAdmin       bool
	RunAsAdmin    bool
	ProfileItems  []ProfileItem
	CanAddProfile bool
}
