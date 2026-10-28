package webui

import (
	"mihomo-tray/internal/sys"
)

const (
	minWindowWidth  = 1000
	minWindowHeight = 680
	maxWindowWidth  = 2400
	maxWindowHeight = 1350

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

func calculateWindowBounds() (winW, winH, winX, winY int) {
	usableW, usableH, startX, startY := sys.GetDisplayWorkArea()
	if usableW <= 0 {
		usableW = fallbackWorkAreaWidth
	}
	if usableH <= 0 {
		usableH = fallbackWorkAreaHeight
	}

	scaleW, scaleH := resolveViewportScale(float64(usableW) / float64(usableH))

	winW = clamp(int(float64(usableW)*scaleW), minWindowWidth, maxWindowWidth)
	winH = clamp(int(float64(usableH)*scaleH), minWindowHeight, maxWindowHeight)

	maxSafeW := int(float64(usableW) * maxSafeMarginRatio)
	maxSafeH := int(float64(usableH) * maxSafeMarginRatio)
	if winW > maxSafeW {
		winW = maxSafeW
	}
	if winH > maxSafeH {
		winH = maxSafeH
	}

	winX = startX + (usableW-winW)/2
	winY = startY + (usableH-winH)/2
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
