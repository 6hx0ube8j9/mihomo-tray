package ui

import (
	"log/slog"
	"sync"
	"syscall"
	"time"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
	"golang.org/x/sys/windows"
	"mihomo-tray/internal/domain"
)

var trayGUID = windows.GUID{
	Data1: 0x6a1b2c3d,
	Data2: 0x4e5f,
	Data3: 0x7a8b,
	Data4: [8]byte{0x9c, 0x0d, 0x1e, 0x2f, 0x3a, 0x4b, 0x5c, 0x6d},
}

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

type TrayIconController struct {
	ni          *walk.NotifyIcon
	currentSize int
	lastStateId int
	icons       map[int]*walk.Icon

	stopOnce sync.Once
	stopChan chan struct{}
}

func NewTrayIconController(ni *walk.NotifyIcon) *TrayIconController {
	c := &TrayIconController{
		ni:          ni,
		lastStateId: -1,
		icons:       make(map[int]*walk.Icon),
		stopChan:    make(chan struct{}),
	}

	c.startDpiWatcher()
	return c
}

func CreateNotifyIcon() (*walk.NotifyIcon, error) {
	ni, err := walk.NewNotifyIconWithGUID(trayGUID)
	if err != nil {
		slog.Warn("[TrayHost] 带 GUID 注册失败，平滑降级至普通模式", "err", err)
		ni, err = walk.NewNotifyIcon()
		if err != nil {
			return nil, err
		}
	}
	_ = ni.SetVisible(true)
	_ = ni.SetToolTip("Mihomo Tray")
	return ni, nil
}

func (c *TrayIconController) SetState(stateId int) {
	if c.ni == nil {
		return
	}

	targetSize := c.getSystemMetricSize()
	if stateId == c.lastStateId && targetSize == c.currentSize {
		return
	}

	ico := c.getIcon(stateId, targetSize)
	if ico == nil {
		return
	}

	if err := c.ni.SetIcon(ico); err != nil {
		slog.Warn("[TrayHost] 应用托盘图标失败", "stateId", stateId, "err", err)
		return
	}

	c.lastStateId = stateId
}

func (c *TrayIconController) getIcon(stateId, targetSize int) *walk.Icon {
	if stateId < 0 || stateId >= len(trayIconResourceNames) {
		return nil
	}

	if targetSize != c.currentSize {
		c.clearIconCache()
		c.currentSize = targetSize
	}

	if ico, ok := c.icons[stateId]; ok {
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
		slog.Error("[TrayHost] 从资源提取图标帧失败", "resource", resName, "size", targetSize, "err", err)
		return nil
	}

	c.icons[stateId] = ico
	return ico
}

func (c *TrayIconController) getSystemMetricSize() int {
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

func (c *TrayIconController) startDpiWatcher() {
	go func() {
		ticker := time.NewTicker(1500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-c.stopChan:
				return
			case <-ticker.C:
				if c.ni == nil {
					return
				}
				if c.getSystemMetricSize() != c.currentSize {
					walk.App().Synchronize(func() {
						c.SetState(c.lastStateId)
					})
				}
			}
		}
	}()
}

func (c *TrayIconController) clearIconCache() {
	for _, ico := range c.icons {
		if ico != nil {
			ico.Dispose()
		}
	}
	c.icons = make(map[int]*walk.Icon)
}

func (c *TrayIconController) Dispose() {
	c.stopOnce.Do(func() {
		close(c.stopChan)
	})
	c.clearIconCache()
	c.ni = nil
}
