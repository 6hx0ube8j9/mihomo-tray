package app

import "mihomo-tray/internal/domain"

type UIPort interface {
	ShowError(title, message string)
	ShowInfo(title, message string)
	ShowNotification(title, message string)
	ShowConfirm(title, message string) bool

	ShowProfileManager(state domain.UIState)
	OpenYAMLFileDialog() (string, bool)
	ShowSubscriptionEditor(title, defaultName, defaultURL string, defaultInterval int, isRemote bool) (name, url string, interval int, ok bool)
	ShowPortEditor(cMixed, cSocks, cHttp int) (nMixed, nSocks, nHttp int, ok bool)
	ShowControllerEditor(cAddr, cSec string, cOnline, cSys bool, cRemoteURL string) (nAddr, nSec string, nOnline, nSys bool, nRemoteURL string, ok bool)

	Exit()
}
