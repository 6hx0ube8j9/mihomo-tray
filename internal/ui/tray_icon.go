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
	icons       []*walk.Icon
	currentSize int
}

func NewIconManager() *IconManager {
	mgr := &IconManager{}
	mgr.Reload()
	return mgr
}

func (m *IconManager) HasDpiChanged() bool {
	return getSystemSmallIconSize() != m.currentSize
}

func (m *IconManager) Reload() {
	newSize := getSystemSmallIconSize()
	m.Dispose()

	m.currentSize = newSize
	m.icons = make([]*walk.Icon, len(trayIconResourceNames))
	walkSize := walk.Size{Width: newSize, Height: newSize}

	for id, resName := range trayIconResourceNames {
		if resName == "" {
			continue
		}
		ico, err := walk.NewIconFromResourceWithSize(resName, walkSize)
		if err != nil {
			slog.Error("加载托盘图标资源失败", "resource", resName, "size", newSize, "err", err)
			continue
		}
		m.icons[id] = ico
	}
}

func (m *IconManager) Get(stateId int) *walk.Icon {
	if stateId >= 0 && stateId < len(m.icons) {
		return m.icons[stateId]
	}
	return nil
}

func (m *IconManager) Dispose() {
	for _, ico := range m.icons {
		if ico != nil {
			ico.Dispose()
		}
	}
	m.icons = nil
}

func getSystemSmallIconSize() int {
	size := int(win.GetSystemMetrics(win.SM_CXSMICON))
	if size <= 0 {
		return 16
	}
	return size
}
