package webui

import (
	"math"

	"mihomo-tray/internal/sys"
)

const (
	minLogicalWidth  = 900
	minLogicalHeight = 580
	maxLogicalWidth  = 1600
	maxLogicalHeight = 1000

	fallbackWorkAreaWidth  = 1280
	fallbackWorkAreaHeight = 800

	maxSafeMarginRatio = 0.96
)

const (
	aspectRatioUltrawide = 2.0
	aspectRatioVertical  = 1.15

	scaleUltrawideW = 0.55
	scaleUltrawideH = 0.82

	scaleVerticalW = 0.92
	scaleVerticalH = 0.60

	scaleDefaultW = 0.72
	scaleDefaultH = 0.80
)

// calculateWindowSize returns the target window size in physical pixels for Win32/WebView2.
func calculateWindowSize() (winW, winH int) {
	m := sys.GetCursorDisplayMetrics()
	return computeWindowSize(m.WorkWidth, m.WorkHeight, m.DPIScale)
}

// calculateWindowBounds returns window size and centered position in DIP (logical pixels)
// for Chromium's --window-size and --window-position flags.
func calculateWindowBounds() (winW, winH, winX, winY int) {
	m := sys.GetCursorDisplayMetrics()
	workW, workH := m.WorkWidth, m.WorkHeight
	if workW <= 0 || workH <= 0 {
		workW = fallbackWorkAreaWidth
		workH = fallbackWorkAreaHeight
	}

	scale := m.DPIScale
	if scale < 1.0 {
		scale = 1.0
	}
	primaryScale := m.PrimaryDPIScale
	if primaryScale < 1.0 {
		primaryScale = scale
	}

	physW, physH := computeWindowSize(workW, workH, scale)
	physX := m.WorkLeft + (workW-physW)/2
	physY := m.WorkTop + (workH-physH)/2

	winW = int(math.Round(float64(physW) / scale))
	winH = int(math.Round(float64(physH) / scale))
	winX = screenCoordToDIP(physX, m.MonitorLeft, scale, primaryScale)
	winY = screenCoordToDIP(physY, m.MonitorTop, scale, primaryScale)
	return
}

func screenCoordToDIP(physPos, monOrigin int, scale, primaryScale float64) int {
	var originDIP float64
	if monOrigin > 0 {
		originDIP = float64(monOrigin) / primaryScale
	} else if monOrigin < 0 {
		originDIP = float64(monOrigin) / scale
	}
	offsetDIP := float64(physPos-monOrigin) / scale
	return int(math.Round(originDIP + offsetDIP))
}

func computeWindowSize(workW, workH int, dpiScale float64) (winW, winH int) {
	if workW <= 0 || workH <= 0 {
		workW = fallbackWorkAreaWidth
		workH = fallbackWorkAreaHeight
	}
	if dpiScale < 1.0 {
		dpiScale = 1.0
	}

	scaleW, scaleH := resolveViewportScale(float64(workW) / float64(workH))

	minW := int(math.Round(float64(minLogicalWidth) * dpiScale))
	minH := int(math.Round(float64(minLogicalHeight) * dpiScale))
	maxW := int(math.Round(float64(maxLogicalWidth) * dpiScale))
	maxH := int(math.Round(float64(maxLogicalHeight) * dpiScale))

	winW = clamp(int(float64(workW)*scaleW), minW, maxW)
	winH = clamp(int(float64(workH)*scaleH), minH, maxH)

	maxSafeW := int(float64(workW) * maxSafeMarginRatio)
	maxSafeH := int(float64(workH) * maxSafeMarginRatio)
	if winW > maxSafeW {
		winW = maxSafeW
	}
	if winH > maxSafeH {
		winH = maxSafeH
	}
	return
}

func resolveViewportScale(aspectRatio float64) (scaleW, scaleH float64) {
	switch {
	case aspectRatio >= aspectRatioUltrawide:
		return scaleUltrawideW, scaleUltrawideH
	case aspectRatio <= aspectRatioVertical:
		return scaleVerticalW, scaleVerticalH
	default:
		return scaleDefaultW, scaleDefaultH
	}
}

func clamp(val, minVal, maxVal int) int {
	if val < minVal {
		return minVal
	}
	if val > maxVal {
		return maxVal
	}
	return val
}
