package ui

import (
	"log/slog"
	"syscall"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
	"mihomo-tray/internal/domain"
)

var (
	// modUser32 已在 ui_winapi.go 中声明，此处直接复用，仅挂载所需的现代 DPI 过程
	procGetDpiForWindow        = modUser32.NewProc("GetDpiForWindow")
	procGetSystemMetricsForDpi = modUser32.NewProc("GetSystemMetricsForDpi")
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

func (m *IconManager) CurrentSize() int {
	return m.currentSize
}

// GetSystemMetricSize 获取任务栏实时的物理像素需求（真正感知 Per-Monitor 缩放）
func (m *IconManager) GetSystemMetricSize() int {
	// 获取任务栏窗口句柄（任务栏由 Explorer 维护，缩放改变时其 DPI 瞬间更新）
	trayClassName, _ := syscall.UTF16PtrFromString("Shell_TrayWnd")
	hwndTaskbar := win.FindWindow(trayClassName, nil)

	var dpi uint32 = 96
	if hwndTaskbar != 0 && procGetDpiForWindow.Find() == nil {
		ret, _, _ := procGetDpiForWindow.Call(uintptr(hwndTaskbar))
		if ret > 0 {
			dpi = uint32(ret)
		}
	}

	// 依据任务栏当前真实 DPI 计算小图标物理像素
	if procGetSystemMetricsForDpi.Find() == nil {
		ret, _, _ := procGetSystemMetricsForDpi.Call(uintptr(win.SM_CXSMICON), uintptr(dpi))
		if ret > 0 {
			return int(ret)
		}
	}

	// 回退保底线性计算: 16 * dpi / 96
	size := int((16 * dpi) / 96)
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

	// 命中缓存：状态与尺寸均未改变
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
