package ui

import (
	"syscall"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
	"mihomo-tray/internal/domain"
)

var (
	trayClassName = syscall.StringToUTF16Ptr("Shell_TrayWnd")

	trayIconNames = map[int]string{
		domain.IconStop:    "ICON_STOP",
		domain.IconError:   "ICON_ERROR",
		domain.IconTun:     "ICON_TUN",
		domain.IconProxy:   "ICON_PROXY",
		domain.IconDefault: "ICON_DEFAULT",
	}
)

type IconManager struct {
	currentSize int
	icons       map[int]*walk.Icon
}

func NewIconManager() *IconManager {
	return &IconManager{
		icons: make(map[int]*walk.Icon),
	}
}

func (m *IconManager) Get(stateId int) *walk.Icon {
	targetSize := m.getTrayIconSize()

	if targetSize != m.currentSize {
		m.Dispose()
		m.currentSize = targetSize
	}

	if ico, ok := m.icons[stateId]; ok {
		return ico
	}

	resName := trayIconNames[stateId]
	if resName == "" {
		return nil
	}

	ico, err := walk.NewIconFromResourceWithSize(resName, walk.Size{
		Width:  m.currentSize,
		Height: m.currentSize,
	})
	if err != nil {
		return nil
	}

	m.icons[stateId] = ico
	return ico
}

func (m *IconManager) CurrentSize() int {
	return m.currentSize
}

func (m *IconManager) Dispose() {
	for _, ico := range m.icons {
		if ico != nil {
			ico.Dispose()
		}
	}
	m.icons = make(map[int]*walk.Icon)
}

func (m *IconManager) getTrayIconSize() int {
	hwnd := win.FindWindow(trayClassName, nil)
	dpi := win.GetDpiForWindow(hwnd)
	if dpi == 0 {
		dpi = 96
	}

	if size := int(win.GetSystemMetricsForDpi(win.SM_CXSMICON, dpi)); size > 0 {
		return size
	}

	return walk.IntFrom96DPI(16, int(dpi))
}
