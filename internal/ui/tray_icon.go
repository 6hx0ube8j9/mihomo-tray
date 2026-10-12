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
	slog.Debug("[IconManager] 初始化 NewIconManager")
	return &IconManager{
		icons: make(map[int]*walk.Icon),
	}
}

// HasDpiChanged 检查任务栏当前的物理像素需求是否已发生改变
func (m *IconManager) HasDpiChanged() bool {
	return m.getSystemMetricSize() != m.currentSize
}

// getSystemMetricSize 获取任务栏实时的物理像素需求（内部私有化，不向业务层泄露）
func (m *IconManager) getSystemMetricSize() int {
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

// Get 获取图标：当前尺寸下直接读取 Map 缓存；DPI 改变时自动刷新并释放旧缓存
func (m *IconManager) Get(stateId int) *walk.Icon {
	if stateId < 0 || stateId >= len(trayIconResourceNames) {
		slog.Warn("[IconManager] 传入的 stateId 超出范围", "stateId", stateId)
		return nil
	}

	targetSize := m.getSystemMetricSize()

	// 屏幕缩放比例变动：清空并释放所有旧尺寸的图标句柄
	if targetSize != m.currentSize {
		slog.Debug("[IconManager] 屏幕缩放变动，重建图标缓存池", "oldSize", m.currentSize, "newSize", targetSize)
		m.Dispose()
		m.currentSize = targetSize
	}

	// 命中当前尺寸的内存缓存，直接复用
	if ico, ok := m.icons[stateId]; ok {
		slog.Debug("[IconManager] 命中内存缓存", "stateId", stateId, "size", targetSize)
		return ico
	}

	resName := trayIconResourceNames[stateId]
	if resName == "" {
		slog.Warn("[IconManager] stateId 对应资源名为空", "stateId", stateId)
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
	slog.Debug("[IconManager] 成功加载并缓存新图标", "resource", resName, "size", targetSize, "stateId", stateId)
	return ico
}

// Dispose 释放当前持有的所有 Win32 图标句柄
func (m *IconManager) Dispose() {
	slog.Debug("[IconManager] Dispose: 释放旧图标句柄池", "count", len(m.icons))
	for _, ico := range m.icons {
		if ico != nil {
			ico.Dispose()
		}
	}
	m.icons = make(map[int]*walk.Icon)
}
