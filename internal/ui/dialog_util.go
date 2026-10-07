package ui

import (
	"strings"
	"unsafe"

	"github.com/tailscale/walk"
	"github.com/tailscale/win"
)


func calcCenteredPos(targetHWND, popupHWND win.HWND) (x, y int32) {
	var popRect win.RECT
	win.GetWindowRect(popupHWND, &popRect)
	dlgW := popRect.Right - popRect.Left
	dlgH := popRect.Bottom - popRect.Top

	var workArea win.RECT
	win.SystemParametersInfo(0x0030, 0, unsafe.Pointer(&workArea), 0)

	if targetHWND != 0 && win.IsWindowVisible(targetHWND) {
		var tgtRect win.RECT
		win.GetWindowRect(targetHWND, &tgtRect)
		tgtW := tgtRect.Right - tgtRect.Left
		tgtH := tgtRect.Bottom - tgtRect.Top
		x = tgtRect.Left + (tgtW-dlgW)/2
		y = tgtRect.Top + (tgtH-dlgH)/2
	} else {
		screenW := workArea.Right - workArea.Left
		screenH := workArea.Bottom - workArea.Top
		x = workArea.Left + (screenW-dlgW)/2
		y = workArea.Top + (screenH-dlgH)/2
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

	return x, y
}

func centerDialog(dlg *walk.Dialog, owner walk.Form) {
	if dlg == nil {
		return
	}

	var targetHWND win.HWND
	if owner != nil && owner.Visible() && !win.IsIconic(owner.Handle()) {
		targetHWND = owner.Handle()
	}

	x, y := calcCenteredPos(targetHWND, dlg.Handle())
	win.SetWindowPos(dlg.Handle(), win.HWND_TOP, x, y, 0, 0, win.SWP_NOSIZE)
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
