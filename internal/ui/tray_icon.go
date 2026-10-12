package ui

import (
	"log/slog"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
	"mihomo-tray/internal/domain"
)

var trayIconResourceNames = []string{
	domain.IconStop:    "ICON_STOP",
	domain.IconError:   "ICON_ERROR",
	domain.IconTun:     "ICON_TUN",
	domain.IconProxy:   "ICON_PROXY",
	domain.IconDefault: "ICON_DEFAULT",
}

type IconManager struct {
	currentSize int
	currentId   int
	currentIcon *walk.Icon
}

func NewIconManager() *IconManager {
	return &IconManager{
		currentId: -1,
	}
}

func (m *IconManager) Get(stateId int) *walk.Icon {
	if stateId < 0 || stateId >= len(trayIconResourceNames) {
		return nil
	}
	resName := trayIconResourceNames[stateId]
	if resName == "" {
		return nil
	}

	targetSize := int(win.GetSystemMetrics(win.SM_CXSMICON))
	if targetSize <= 0 {
		targetSize = 16
	}

	if m.currentIcon != nil && m.currentId == stateId && m.currentSize == targetSize {
		return m.currentIcon
	}

	m.Dispose()

	walkSize := walk.Size{Width: targetSize, Height: targetSize}
	ico, err := walk.NewIconFromResourceWithSize(resName, walkSize)
	if err != nil {
		slog.Error("加载托盘图标失败", "resource", resName, "size", targetSize, "err", err)
		return nil
	}

	m.currentSize = targetSize
	m.currentId = stateId
	m.currentIcon = ico
	return m.currentIcon
}

func (m *IconManager) Dispose() {
	if m.currentIcon != nil {
		m.currentIcon.Dispose()
		m.currentIcon = nil
	}
}
