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
	slog.Debug("[IconManager] 初始化 NewIconManager 开始")
	mgr := &IconManager{
		currentId:   -1,
		currentSize: 0,
	}
	slog.Debug("[IconManager] 初始化 NewIconManager 完成")
	return mgr
}

// CurrentSize 返回当前已加载的图标物理像素
func (m *IconManager) CurrentSize() int {
	return m.currentSize
}

// GetSystemMetricSize 直接查询 Windows 当前系统小图标物理需求
func (m *IconManager) GetSystemMetricSize() int {
	size := int(win.GetSystemMetrics(win.SM_CXSMICON))
	if size <= 0 {
		return 16
	}
	return size
}

// Get 根据业务状态获取匹配当前 DPI 的最清晰图标
func (m *IconManager) Get(stateId int) *walk.Icon {
	targetSize := m.GetSystemMetricSize()
	slog.Debug("[IconManager] Get 图标请求",
		"reqStateId", stateId,
		"currentId", m.currentId,
		"currentSize", m.currentSize,
		"targetSize", targetSize,
	)

	if stateId < 0 || stateId >= len(trayIconResourceNames) {
		slog.Warn("[IconManager] 传入的 stateId 超出范围", "stateId", stateId)
		return nil
	}
	resName := trayIconResourceNames[stateId]
	if resName == "" {
		slog.Warn("[IconManager] stateId 对应的资源名称为空", "stateId", stateId)
		return nil
	}

	// 缓存命中：状态与尺寸均未发生改变
	if m.currentIcon != nil && m.currentId == stateId && m.currentSize == targetSize {
		slog.Debug("[IconManager] 命中内存句柄缓存，直接复用", "stateId", stateId, "size", m.currentSize)
		return m.currentIcon
	}

	slog.Debug("[IconManager] 缓存未命中或尺寸变化，准备加载新资源",
		"resName", resName,
		"oldSize", m.currentSize,
		"newTargetSize", targetSize,
		"oldId", m.currentId,
		"newId", stateId,
	)

	// 释放旧 GDI 图标句柄
	m.Dispose()

	walkSize := walk.Size{Width: targetSize, Height: targetSize}
	ico, err := walk.NewIconFromResourceWithSize(resName, walkSize)
	if err != nil {
		slog.Error("[IconManager] 从资源加载指定尺寸图标失败", "resource", resName, "size", targetSize, "err", err)
		return nil
	}

	m.currentSize = targetSize
	m.currentId = stateId
	m.currentIcon = ico

	slog.Debug("[IconManager] 图标资源加载成功并已缓存", "resName", resName, "size", m.currentSize, "id", m.currentId)
	return m.currentIcon
}

func (m *IconManager) Dispose() {
	if m.currentIcon != nil {
		slog.Debug("[IconManager] Dispose: 释放旧图标句柄", "lastId", m.currentId, "lastSize", m.currentSize)
		m.currentIcon.Dispose()
		m.currentIcon = nil
	}
}
