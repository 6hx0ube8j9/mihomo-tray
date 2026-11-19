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

func calculateWindowSize() (winW, winH int) {
	usableW, usableH, _, _, dpiScale := sys.GetDisplayMetrics()
	return computeWindowSize(usableW, usableH, dpiScale)
}

func calculateWindowBounds() (winW, winH, winX, winY int) {
	usableW, usableH, startX, startY, dpiScale := sys.GetDisplayMetrics()
	winW, winH = computeWindowSize(usableW, usableH, dpiScale)
	winX = startX + (usableW-winW)/2
	winY = startY + (usableH-winH)/2
	return
}

func computeWindowSize(usableW, usableH int, dpiScale float64) (winW, winH int) {
	if usableW <= 0 || usableH <= 0 {
		usableW = fallbackWorkAreaWidth
		usableH = fallbackWorkAreaHeight
	}
	if dpiScale < 1.0 {
		dpiScale = 1.0
	}

	scaleW, scaleH := resolveViewportScale(float64(usableW) / float64(usableH))

	minW := int(math.Round(float64(minLogicalWidth) * dpiScale))
	minH := int(math.Round(float64(minLogicalHeight) * dpiScale))
	maxW := int(math.Round(float64(maxLogicalWidth) * dpiScale))
	maxH := int(math.Round(float64(maxLogicalHeight) * dpiScale))

	winW = clamp(int(float64(usableW)*scaleW), minW, maxW)
	winH = clamp(int(float64(usableH)*scaleH), minH, maxH)

	maxSafeW := int(float64(usableW) * maxSafeMarginRatio)
	maxSafeH := int(float64(usableH) * maxSafeMarginRatio)
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