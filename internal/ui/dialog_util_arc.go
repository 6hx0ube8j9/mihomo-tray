//go:build ignore

// Package archive contains historical Win32 patches and legacy algorithms 
// that are kept for reference and architectural documentation.
package archive

import (
	"unsafe"

	"github.com/tailscale/win"
)

// calcCenteredPos calculates the absolute screen coordinates (X, Y) required to 
// perfectly center a popup window (popupHWND) relative to its parent window (targetHWND).
// 
// Design Considerations & Mitigations:
// 1. DWM Shadow Cancellation: It intentionally uses GetWindowRect for both parent and popup. 
//    This ensures that the invisible Desktop Window Manager (DWM) drop shadows (typically 7px 
//    on left/right/bottom) present on both windows perfectly cancel each other out during 
//    subtraction, resulting in absolute geometric centering.
// 2. Fallback Mechanism: If the parent window is hidden, iconic (minimized), or undefined, 
//    it gracefully falls back to centering the popup within the primary monitor's working area 
//    (SystemParametersInfo 0x0030), strictly clamped within screen bounds to prevent off-screen rendering.
func calcCenteredPos(targetHWND, popupHWND win.HWND) (x, y int32) {
	var popRect win.RECT
	win.GetWindowRect(popupHWND, &popRect)
	dlgW := popRect.Right - popRect.Left
	dlgH := popRect.Bottom - popRect.Top

	if targetHWND != 0 && win.IsWindowVisible(targetHWND) && !win.IsIconic(targetHWND) {
		// Use GetWindowRect to include non-client areas (title bar & shadows) for symmetric calculation.
		var targetRect win.RECT
		win.GetWindowRect(targetHWND, &targetRect)

		centerCX := targetRect.Left + (targetRect.Right-targetRect.Left)/2
		centerCY := targetRect.Top + (targetRect.Bottom-targetRect.Top)/2

		return centerCX - dlgW/2, centerCY - dlgH/2
	}

	// Fallback: Center relative to the screen's working area (excluding taskbar)
	var workArea win.RECT
	win.SystemParametersInfo(0x0030, 0, unsafe.Pointer(&workArea), 0)

	screenW := workArea.Right - workArea.Left
	screenH := workArea.Bottom - workArea.Top
	x = workArea.Left + (screenW-dlgW)/2
	y = workArea.Top + (screenH-dlgH)/2

	// Safety clamp: Ensure the window does not exceed screen boundaries
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

// lockWindowSize forcibly strips the resizable frame and maximize button from a window.
// 
// Historical Context & Usage:
// This function acts as a bypass for a known lifecycle synchronization bug in the 
// tailscale/walk framework. When using the declarative 'FixedSize: true' property, 
// the framework defers the final height calculation (especially for VSpacers) until 
// the layout engine settles. If geometric centering is performed during the 'Starting()' 
// event, the deferred height causes the window to center incorrectly (visually shifting downwards).
// 
// By omitting the official 'FixedSize' property and calling this Win32 patch during 
// 'Starting()', we instantly lock the window dimensions without triggering the framework's 
// layout deferral, ensuring perfectly synchronized coordinate calculations.
func lockWindowSize(hwnd win.HWND) {
	// Retrieve the current window style
	style := win.GetWindowLong(hwnd, win.GWL_STYLE)
	
	// Strip the thick sizing frame and maximize button bits
	style &^= win.WS_THICKFRAME | win.WS_MAXIMIZEBOX
	win.SetWindowLong(hwnd, win.GWL_STYLE, style)
	
	// Force the operating system to recalculate the non-client area immediately 
	// without altering the window's size, position, or Z-order.
	win.SetWindowPos(hwnd, 0, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_FRAMECHANGED)
}
