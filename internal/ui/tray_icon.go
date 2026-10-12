package ui

import (
	"log/slog"
	"syscall"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
	"mihomo-tray/internal/domain"
)

var (
	trayClassName = syscall.StringToUTF16Ptr("Shell_TrayWnd")

	trayIconResourceNames = []string{
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

func (m *IconManager) CurrentSize() int {
	return m.currentSize
}

// GetSystemMetricSize 直接使用 tailscale/win 导出函数获取任务栏实时像素
func (m *IconManager) GetSystemMetricSize() int {
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

// Get 获取图标：当前尺寸下读 Map 缓存；DPI 改变时自动刷新缓存
func (m *IconManager) Get(stateId int) *walk.Icon {
	if stateId < 0 || stateId >= len(trayIconResourceNames) {
		return nil
	}

	targetSize := m.GetSystemMetricSize()

	// 屏幕缩放比例变动：清空并释放所有旧尺寸的图标句柄
	if targetSize != m.currentSize {
		m.Dispose()
		m.currentSize = targetSize
	}

	// 命中当前尺寸的内存缓存直接复用
	if ico, ok := m.icons[stateId]; ok {
		return ico
	}

	resName := trayIconResourceNames[stateId]
	if resName == "" {
		return nil
	}

	ico, err := walk.NewIconFromResourceWithSize(resName, walk.Size{
		Width:  targetSize,
		Height: targetSize,
	})
	if err != nil {
		slog.Error("[IconManager] 加载图标资源失败", "resource", resName, "size", targetSize, "err", err)
		return nil
	}

	m.icons[stateId] = ico
	return ico
}

// Dispose 释放当前持有的所有 Win32 图标句柄
func (m *IconManager) Dispose() {
	for _, ico := range m.icons {
		if ico != nil {
			ico.Dispose()
		}
	}
	m.icons = make(map[int]*walk.Icon)
}
