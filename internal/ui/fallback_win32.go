package ui

import "mihomo-tray/internal/domain"

/*
ABOUT THIS FILE:
This file contains fallback implementations and defensive routines for Win32/CGO edge cases.
It acts as a sealed "dead code" repository for emergency rollbacks.

WHY KEEP THIS?
In CGO + Win32 UI development (specifically with Tailscale's walk fork), closing the main window 
might cause an "Access Violation" (0xc0000005) crash if the Go runtime shuts down before 
the system's `WM_NCDESTROY` message can trigger our self-cleaning WndProc hooks in dashboard.go.

HOW TO USE:
If the application ever experiences crashes upon exiting, simply replace the exit logic in `tray.go`
with a call to `ForceSafeExit(t.engine)`.
*/

// ForceSafeExit forcefully disposes of the Dashboard to unhook Win32 callbacks
// safely before closing the main window loop. 
// Kept as a sealed interface for future safe-exit guarantees.
func ForceSafeExit(e *Engine) {
	e.SendCommand(domain.ActionExitApp, "")
	
	e.app.Synchronize(func() {
		// Manually destroy the dashboard to unhook callbacks before Walk loop ends.
		if e.Dashboard != nil && e.Dashboard.window != nil {
			e.Dashboard.window.Dispose()
		}
		if e.mw != nil {
			e.mw.Close()
		}
	})
}
