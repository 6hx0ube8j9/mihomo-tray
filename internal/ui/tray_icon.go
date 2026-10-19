package ui

import (
	"log/slog"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"

	"mihomo-tray/internal/domain"
)

const (
	baseDPI          = 96
	baseIconSize     = 16
	dpiProbeInterval = 1500 * time.Millisecond
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

type TrayIconController struct {
	ni          *walk.NotifyIcon
	currentSize atomic.Int32
	lastStateId atomic.Int32
	icons       map[int]*walk.Icon

	engine     *Engine
	isDisposed atomic.Bool
}

func NewTrayIconController(e *Engine, ni *walk.NotifyIcon) *TrayIconController {
	c := &TrayIconController{
		ni:     ni,
		icons:  make(map[int]*walk.Icon),
		engine: e,
	}
	c.lastStateId.Store(-1)
	c.currentSize.Store(0)

	c.startDpiWatcher()
	return c
}

func CreateNotifyIcon() (*walk.NotifyIcon, error) {
	ni, err := walk.NewNotifyIcon()
	if err != nil {
		return nil, err
	}
	_ = ni.SetVisible(true)
	_ = ni.SetToolTip(domain.AppName)
	return ni, nil
}

func (c *TrayIconController) SetState(stateId int) {
	if c.isDisposed.Load() || c.ni == nil {
		return
	}

	targetSize := c.getSystemMetricSize()
	currSize := int(c.currentSize.Load())
	lastId := int(c.lastStateId.Load())

	if stateId == lastId && targetSize == currSize {
		return
	}

	sizeChanged := targetSize != currSize
	var oldIcons map[int]*walk.Icon

	if sizeChanged {
		oldIcons = c.icons
		c.icons = make(map[int]*walk.Icon)
	}

	ico := c.getIcon(stateId, targetSize)
	if ico == nil {
		if sizeChanged {
			c.icons = oldIcons
		}
		return
	}

	if err := c.ni.SetIcon(ico); err != nil {
		slog.Warn("设置托盘图标失败", "state_id", stateId, "err", err)
		if sizeChanged {
			ico.Dispose()
			c.icons = oldIcons
		}
		return
	}

	c.lastStateId.Store(int32(stateId))

	if sizeChanged {
		c.currentSize.Store(int32(targetSize))
		for _, oldIco := range oldIcons {
			if oldIco != nil {
				oldIco.Dispose()
			}
		}
	}
}

func (c *TrayIconController) getIcon(stateId, targetSize int) *walk.Icon {
	if stateId < 0 || stateId >= len(trayIconResourceNames) {
		return nil
	}

	if ico, ok := c.icons[stateId]; ok && ico != nil {
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

	if err != nil || ico == nil {
		slog.Debug("加载指定尺寸图标失败，尝试默认尺寸", "resource", resName, "size", targetSize, "err", err)
		ico, err = walk.NewIconFromResource(resName)
		if err != nil || ico == nil {
			slog.Error("加载默认图标资源失败", "resource", resName, "err", err)
			return nil
		}
	}

	c.icons[stateId] = ico
	return ico
}

func (c *TrayIconController) getSystemMetricSize() int {
	hwnd := win.FindWindow(trayClassName, nil)
	dpi := win.GetDpiForWindow(hwnd)
	if dpi == 0 {
		dpi = baseDPI
	}

	if size := int(win.GetSystemMetricsForDpi(win.SM_CXSMICON, dpi)); size > 0 {
		return size
	}

	return walk.IntFrom96DPI(baseIconSize, int(dpi))
}

func (c *TrayIconController) startDpiWatcher() {
	go func() {
		ticker := time.NewTicker(dpiProbeInterval)
		defer ticker.Stop()

		for {
			select {
			case <-c.engine.ctx.Done():
				return
			case <-ticker.C:
				if c.isDisposed.Load() {
					return
				}

				if c.getSystemMetricSize() != int(c.currentSize.Load()) {
					c.engine.RunOnUI(func() {
						if !c.isDisposed.Load() {
							c.SetState(int(c.lastStateId.Load()))
						}
					})
				}
			}
		}
	}()
}

func (c *TrayIconController) clearAllCache() {
	for id, ico := range c.icons {
		if ico != nil {
			ico.Dispose()
		}
		delete(c.icons, id)
	}
}

func (c *TrayIconController) Dispose() {
	c.isDisposed.Store(true)
	c.clearAllCache()
	c.ni = nil
}
