package webui

import (
	"strings"

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

const (
	wndClassChromiumPrefix = "Chrome_WidgetWin"
	wndClassGeckoPrefix    = "MozillaWindowClass"
)

var ghostCharReplacer = strings.NewReplacer(
	"\u200b", "", "\u200c", "", "\u200d", "",
	"\u200e", "", "\u200f", "", "\ufeff", "", "\u00a0", " ",
)

var standardBrowserSuffixes = []string{
	"google chrome", "microsoft edge", "msedge",
	"brave", "vivaldi", "firefox", "opera", "chromium",
}

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

	winX = max(0, startX+(usableW-winW)/2)
	winY = max(0, startY+(usableH-winH)/2)
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

func (m *Manager) findAndFocusWebUI(targetTitle, appHostPort string, targetPid uint32) bool {
	exactTitle := strings.TrimSpace(targetTitle)
	fallbackAnchor := strings.ToLower(strings.TrimSpace(appHostPort))

	var pendingPidHwnd uintptr

	targetHwnd := sys.FindTopWindow(func(info sys.WindowInfo) bool {
		if !isBrowserWindowClass(info.ClassName) || info.Title == "" {
			return false
		}

		titleLower := strings.ToLower(info.Title)
		isTitleHit := exactTitle != "" && info.Title == exactTitle
		isAnchorHit := fallbackAnchor != "" && strings.Contains(titleLower, fallbackAnchor)

		if targetPid != 0 && info.Pid == targetPid {
			if isTitleHit || isAnchorHit {
				return true
			}
			if pendingPidHwnd == 0 {
				pendingPidHwnd = info.Hwnd
			}
			return false
		}

		if targetPid == 0 {
			if isStandardBrowserWindow(titleLower) {
				return false
			}
			return isTitleHit || isAnchorHit
		}

		return false
	})

	if targetHwnd == 0 {
		targetHwnd = pendingPidHwnd
	}

	if targetHwnd != 0 {
		m.hwnd.Store(targetHwnd)
		sys.FocusWindowSilky(targetHwnd)
		return true
	}
	return false
}

func isBrowserWindowClass(className string) bool {
	return strings.HasPrefix(className, wndClassChromiumPrefix) ||
		strings.HasPrefix(className, wndClassGeckoPrefix)
}

func isStandardBrowserWindow(title string) bool {
	clean := ghostCharReplacer.Replace(strings.ToLower(title))
	clean = strings.TrimSpace(clean)
	for _, suffix := range standardBrowserSuffixes {
		if strings.HasSuffix(clean, suffix) {
			return true
		}
	}
	return false
}
