package ui

import "mihomo-tray/internal/domain"

/*
NOTE: Win32/CGO Access Violation Fallback
Keep this as a backup. If the app ever crashes on exit (due to Walk framework updates 
or Windows environment quirks), call `forceSafeExit(t.engine)` in tray.go instead.
*/

// forceSafeExit forcefully disposes of heavy UI components (like Dashboard) to unhook Win32 callbacks safely,
// before triggering the standard Context-driven exit pipeline.
func forceSafeExit(e *Engine) {
	e.app.Synchronize(func() {
		if e.Dashboard != nil && e.Dashboard.window != nil {
			e.Dashboard.window.Dispose()
		}
	})
	
	e.SendCommand(domain.ActionExitApp, "")
}
