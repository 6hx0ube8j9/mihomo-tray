package ui

import (
	"github.com/tailscale/walk"
)

// centerWindow 居中主窗口
func centerWindow(w *walk.MainWindow) {
	if w == nil {
		return
	}
	monitor := walk.PrimaryMonitor()
	workArea := monitor.WorkArea()
	bounds := w.Bounds()
	newX := workArea.X + (workArea.Width-bounds.Width)/2
	newY := workArea.Y + (workArea.Height-bounds.Height)/2
	if newX < 0 {
		newX = 0
	}
	if newY < 0 {
		newY = 0
	}
	w.SetBounds(walk.Rectangle{X: newX, Y: newY, Width: bounds.Width, Height: bounds.Height})
}
