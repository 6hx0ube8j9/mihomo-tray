package ui

import (
	"log/slog"
	"sync"
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
	mu          sync.Mutex
	ni          *walk.NotifyIcon
	currentSize int
	lastStateId int
	icons       map[int]*walk.Icon

	cachedTrayHwnd win.HWND
	engine         *Engine
	isDisposed     atomic.Bool
}

func NewTrayIconController(e *Engine, ni *walk.NotifyIcon) *TrayIconController {
	c := &TrayIconController{
		ni:          ni,
		icons:       make(map[int]*walk.Icon),
		engine:      e,
		lastStateId: -1,
		currentSize: 0,
	}

	c.startDpiWatcher()
	return c
}

func CreateNotifyIcon() (*walk.NotifyIcon, error) {
	ni, err := walk.NewNotifyIcon()
	if err != nil {
		return nil, err
	}
	_ = ni.SetToolTip(domain.AppName)
	return ni, nil
}

func (c *TrayIconController) SetState(stateId int) {
	if c.isDisposed.Load() || stateId < 0 || stateId >= len(trayIconResourceNames) {
		return
	}

	c.engine.RunOnUI(func() {
		c.setStateInternal(stateId)
	})
}

func (c *TrayIconController) setStateInternal(stateId int) {
	if c.isDisposed.Load() || c.ni == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	targetSize := c.getSystemMetricSizeLocked()
	if stateId == c.lastStateId && targetSize == c.currentSize {
		return
	}

	sizeChanged := targetSize != c.currentSize
	var oldIcons map[int]*walk.Icon

	if sizeChanged {
		oldIcons = c.icons
		c.icons = make(map[int]*walk.Icon)
	}

	ico := c.getIconLocked(stateId, targetSize)
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

	if !c.ni.Visible() {
		_ = c.ni.SetVisible(true)
	}

	c.lastStateId = stateId

	if sizeChanged {
		c.currentSize = targetSize
		for _, oldIco := range oldIcons {
			if oldIco != nil {
				oldIco.Dispose()
			}
		}
	}
}

func (c *TrayIconController) getIconLocked(stateId, targetSize int) *walk.Icon {
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

func (c *TrayIconController) getSystemMetricSizeLocked() int {
	if c.cachedTrayHwnd == 0 || !win.IsWindow(c.cachedTrayHwnd) {
		c.cachedTrayHwnd = win.FindWindow(trayClassName, nil)
	}

	var dpi uint32
	if c.cachedTrayHwnd != 0 {
		dpi = win.GetDpiForWindow(c.cachedTrayHwnd)
	}

	if dpi == 0 {
		dpi = win.GetDpiForSystem()
	}

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

				c.mu.Lock()
				lastId := c.lastStateId
				currSize := c.currentSize
				targetSize := c.getSystemMetricSizeLocked()
				c.mu.Unlock()

				if lastId != -1 && targetSize != currSize {
					c.SetState(lastId)
				}
			}
		}
	}()
}

func (c *TrayIconController) Dispose() {
	if c.isDisposed.Swap(true) {
		return
	}

	c.engine.RunOnUI(func() {
		c.mu.Lock()
		defer c.mu.Unlock()

		for id, ico := range c.icons {
			if ico != nil {
				ico.Dispose()
			}
			delete(c.icons, id)
		}

		if c.ni != nil {
			_ = c.ni.SetVisible(false)
			c.ni.Dispose()
			c.ni = nil
		}
	})
}
