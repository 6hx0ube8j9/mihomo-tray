package app

import "mihomo-tray/internal/domain"

type UIPort interface {
	ShowError(title, message string)
	ShowNotification(title, message string)
	ShowConfirm(title, message string) bool

	ShowProfileManager(state domain.UIState)
	OpenYAMLFileDialog() (string, bool)
	ShowProfileInfoEditor(title, defaultName, defaultURL string, defaultInterval int, isRemote bool) (name, url string, interval int, ok bool)
	ShowPortEditor(cMixed, cSocks, cHttp int) (nMixed, nSocks, nHttp int, ok bool)
	ShowControllerEditor(current domain.ControllerSettings) (next domain.ControllerSettings, ok bool)

	Exit()
}