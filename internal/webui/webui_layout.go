package webui

import (
	"sync"

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

var (
	layoutOnce sync.Once
	cachedW    int
	cachedH    int
	cachedX    int
	cachedY    int
)

func calculateWindowBounds() (winW, winH, winX, winY int) {
	layoutOnce.Do(func() {
		cachedW, cachedH, cachedX, cachedY = computeWindowBounds()
	})
	return cachedW, cachedH, cachedX, cachedY
}

func computeWindowBounds() (winW, winH, winX, winY int) {
	usableW, usableH, startX, startY := sys.GetDisplayWorkArea()

	if usableW < minWindowWidth || usableH < minWindowHeight {
		usableW = fallbackWorkAreaWidth
		usableH = fallbackWorkAreaHeight
		startX = 0
		startY = 0
	}

	if startX < 0 {
		startX = 0
	}
	if startY < 0 {
		startY = 0
	}

	aspectRatio := float64(usableW) / float64(usableH)
	scaleW, scaleH := resolveViewportScale(aspectRatio)

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

	if winX < startX {
		winX = startX
	}
	if winY < startY {
		winY = startY
	}

	return winW, winH, winX, winY
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
