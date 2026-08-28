package sys

import "golang.org/x/sys/windows"

// DLL loaders
var (
	k32     = windows.NewLazySystemDLL("kernel32.dll")
	u32     = windows.NewLazySystemDLL("user32.dll")
	s32     = windows.NewLazySystemDLL("shell32.dll")
	wininet = windows.NewLazySystemDLL("wininet.dll")
)

// Win32 procedures
var (
	// --- kernel32 ---
	procGetCurrentThread      = k32.NewProc("GetCurrentThreadId")       // window
	pGetModuleHandleW         = k32.NewProc("GetModuleHandleW")          // tray
	procAttachConsole         = k32.NewProc("AttachConsole")            // process
	procFreeConsole           = k32.NewProc("FreeConsole")              // process
	procSetConsoleCtrlHandler = k32.NewProc("SetConsoleCtrlHandler")    // process

	// --- user32: window & focus ---
	procEnumWindows          = u32.NewProc("EnumWindows")
	procGetClassName         = u32.NewProc("GetClassNameW")
	procIsWindowVisible      = u32.NewProc("IsWindowVisible")
	procGetWindowThread      = u32.NewProc("GetWindowThreadProcessId")
	procGetWindowText        = u32.NewProc("GetWindowTextW")
	procSetWindowPos         = u32.NewProc("SetWindowPos")
	procShowWindow           = u32.NewProc("ShowWindow")
	procBringToTop           = u32.NewProc("BringWindowToTop")
	procGetForeground        = u32.NewProc("GetForegroundWindow")
	procAttachThread         = u32.NewProc("AttachThreadInput")
	procSwitchToThisWindow   = u32.NewProc("SwitchToThisWindow")
	procSystemParametersInfo = u32.NewProc("SystemParametersInfoW")
	procSetProcessDpiContext = u32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware   = u32.NewProc("SetProcessDPIAware")

	// --- user32: shared ---
	procGetSystemMetrics = u32.NewProc("GetSystemMetrics")
	pGetSystemMetrics    = procGetSystemMetrics

	procSetForeground    = u32.NewProc("SetForegroundWindow")
	pSetForegroundWindow = procSetForeground

	// --- user32: tray & menu ---
	pRegisterClassExW         = u32.NewProc("RegisterClassExW")
	pCreateWindowExW          = u32.NewProc("CreateWindowExW")
	pDestroyWindow            = u32.NewProc("DestroyWindow")
	pDefWindowProcW           = u32.NewProc("DefWindowProcW")
	pGetMessageW              = u32.NewProc("GetMessageW")
	pTranslateMessage         = u32.NewProc("TranslateMessage")
	pDispatchMessageW         = u32.NewProc("DispatchMessageW")
	pPostQuitMessage          = u32.NewProc("PostQuitMessage")
	pPostMessageW             = u32.NewProc("PostMessageW")
	pRegisterWindowMessageW   = u32.NewProc("RegisterWindowMessageW")
	pCreatePopupMenu          = u32.NewProc("CreatePopupMenu")
	pAppendMenuW              = u32.NewProc("AppendMenuW")
	pDestroyMenu              = u32.NewProc("DestroyMenu")
	pTrackPopupMenu           = u32.NewProc("TrackPopupMenu")
	pGetCursorPos             = u32.NewProc("GetCursorPos")
	pDestroyIcon              = u32.NewProc("DestroyIcon")
	pCreateIconFromResourceEx = u32.NewProc("CreateIconFromResourceEx")

	// --- shell32 ---
	pShell_NotifyIconW = s32.NewProc("Shell_NotifyIconW") // tray

	// --- wininet ---
	procInternetSetOption = wininet.NewProc("InternetSetOptionW") // proxy
)
