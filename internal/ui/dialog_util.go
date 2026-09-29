package ui

import (
	"strings"
	"unsafe"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
)

func getValidOwner() walk.Form {
	if GlobalEngine != nil {
		if GlobalEngine.Dashboard != nil && GlobalEngine.Dashboard.window != nil {
			hwnd := GlobalEngine.Dashboard.window.Handle()
			if win.IsWindowVisible(hwnd) && !win.IsIconic(hwnd) {
				return GlobalEngine.Dashboard.window
			}
		}
		if GlobalEngine.mw != nil {
			return GlobalEngine.mw
		}
	}
	return nil
}

func safeSync(fn func()) {
	if GlobalEngine != nil && GlobalEngine.app != nil {
		GlobalEngine.app.Synchronize(fn)
	}
}

func autoWrapText(text string, maxVisualWidth int) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var result []string
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		runes := []rune(line)
		if len(runes) == 0 {
			result = append(result, "")
			continue
		}
		var currentLine []rune
		currentWidth := 0
		for _, r := range runes {
			w := 1
			if r > 255 {
				w = 2
			}
			if currentWidth+w > maxVisualWidth {
				result = append(result, string(currentLine))
				currentLine = []rune{r}
				currentWidth = w
			} else {
				currentLine = append(currentLine, r)
				currentWidth += w
			}
		}
		if len(currentLine) > 0 {
			result = append(result, string(currentLine))
		}
	}
	return strings.Join(result, "\r\n")
}

func lockWindowSize(hwnd win.HWND) {
	style := win.GetWindowLong(hwnd, win.GWL_STYLE)
	style &^= win.WS_THICKFRAME | win.WS_MAXIMIZEBOX
	win.SetWindowLong(hwnd, win.GWL_STYLE, style)
	win.SetWindowPos(hwnd, 0, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_FRAMECHANGED)
}

func centerDialog(dlg *walk.Dialog, owner walk.Form, hActive win.HWND) {
	if dlg == nil {
		return
	}

	var dRect win.RECT
	win.GetWindowRect(dlg.Handle(), &dRect)
	dlgW := dRect.Right - dRect.Left
	dlgH := dRect.Bottom - dRect.Top

	var dcRect win.RECT
	win.GetClientRect(dlg.Handle(), &dcRect)
	dPtLT := win.POINT{X: 0, Y: 0}
	win.ClientToScreen(dlg.Handle(), &dPtLT)

	dcOffsetCX := (dPtLT.X - dRect.Left) + dcRect.Right/2
	dcOffsetCY := (dPtLT.Y - dRect.Top) + dcRect.Bottom/2

	var workArea win.RECT
	win.SystemParametersInfo(0x0030, 0, unsafe.Pointer(&workArea), 0)

	var x, y int32
	shouldFollowOwner := owner != nil && owner.Visible() && !win.IsIconic(owner.Handle())

	if shouldFollowOwner && hActive != 0 && hActive != owner.Handle() {
		shouldFollowOwner = false
	}

	if shouldFollowOwner {
		var pClientRect win.RECT
		win.GetClientRect(owner.Handle(), &pClientRect)

		ptLT := win.POINT{X: 0, Y: 0}
		win.ClientToScreen(owner.Handle(), &ptLT)

		pCX := ptLT.X + pClientRect.Right/2
		pCY := ptLT.Y + pClientRect.Bottom/2

		x = pCX - dcOffsetCX
		y = pCY - dcOffsetCY
	} else {
		x = workArea.Left + (workArea.Right-workArea.Left-dlgW)/2
		y = workArea.Top + (workArea.Bottom-workArea.Top-dlgH)/2
	}

	if x < workArea.Left {
		x = workArea.Left
	} else if x+dlgW > workArea.Right {
		x = workArea.Right - dlgW
	}
	
	if y < workArea.Top {
		y = workArea.Top
	} else if y+dlgH > workArea.Bottom {
		y = workArea.Bottom - dlgH
	}

	win.SetWindowPos(dlg.Handle(), win.HWND_TOP, x, y, 0, 0, win.SWP_NOSIZE)
}

func restoreFocus(parent walk.Form, hActive win.HWND) {
	if parent != nil && parent.Visible() && !win.IsIconic(parent.Handle()) {
		win.SetForegroundWindow(parent.Handle())
		win.SetFocus(parent.Handle())
	} else if hActive != 0 && win.IsWindowVisible(hActive) && !win.IsIconic(hActive) {
		win.SetForegroundWindow(hActive)
		win.SetFocus(hActive)
	}
}
