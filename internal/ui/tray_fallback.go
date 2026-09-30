package ui

import "mihomo-tray/internal/domain"

/*
NOTE: Win32/CGO Access Violation Fallback
Keep this as a backup. If the app ever crashes on exit (due to Walk framework updates 
or Windows environment quirks), call `forceSafeExit(t.engine)` in tray.go instead.
*/

// forceSafeExit forcefully disposes of the Dashboard to unhook Win32 callbacks safely.
func forceSafeExit(e *Engine) {
	e.SendCommand(domain.ActionExitApp, "")
	e.app.Synchronize(func() {
		if e.Dashboard != nil && e.Dashboard.window != nil {
			e.Dashboard.window.Dispose()
		}
		if e.mw != nil {
			e.mw.Close()
		}
	})
}
