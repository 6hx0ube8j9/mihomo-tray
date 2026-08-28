package sys

import "golang.org/x/sys/windows"

// DLL loaders
var (
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modShell32  = windows.NewLazySystemDLL("shell32.dll")
	modWininet  = windows.NewLazySystemDLL("wininet.dll")
)

// Win32 procedures
var (
	// --- kernel32 ---
	procGetCurrentThread      = modKernel32.NewProc("GetCurrentThreadId")    // window
	pGetModuleHandleW         = modKernel32.NewProc("GetModuleHandleW")       // tray
	procAttachConsole         = modKernel32.NewProc("AttachConsole")         // process
	procFreeConsole           = modKernel32.NewProc("FreeConsole")           // process
	procSetConsoleCtrlHandler = modKernel32.NewProc("SetConsoleCtrlHandler") // process

	// --- user32: window & focus ---
	procEnumWindows          = modUser32.NewProc("EnumWindows")
	procGetClassName         = modUser32.NewProc("GetClassNameW")
	procIsWindowVisible      = modUser32.NewProc("IsWindowVisible")
	procGetWindowThread      = modUser32.NewProc("GetWindowThreadProcessId")
	procGetWindowText        = modUser32.NewProc("GetWindowTextW")
	procSetWindowPos         = modUser32.NewProc("SetWindowPos")
	procShowWindow           = modUser32.NewProc("ShowWindow")
	procBringToTop           = modUser32.NewProc("BringWindowToTop")
	procGetForeground        = modUser32.NewProc("GetForegroundWindow")
	procAttachThread         = modUser32.NewProc("AttachThreadInput")
	procSwitchToThisWindow   = modUser32.NewProc("SwitchToThisWindow")
	procSystemParametersInfo = modUser32.NewProc("SystemParametersInfoW")
	procSetProcessDpiContext = modUser32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware   = modUser32.NewProc("SetProcessDPIAware")

	// --- user32: shared ---
	procGetSystemMetrics = modUser32.NewProc("GetSystemMetrics")
	pGetSystemMetrics    = procGetSystemMetrics

	procSetForeground    = modUser32.NewProc("SetForegroundWindow")
	pSetForegroundWindow = procSetForeground

	// --- user32: tray & menu ---
	pRegisterClassExW         = modUser32.NewProc("RegisterClassExW")
	pCreateWindowExW          = modUser32.NewProc("CreateWindowExW")
	pDestroyWindow            = modUser32.NewProc("DestroyWindow")
	pDefWindowProcW           = modUser32.NewProc("DefWindowProcW")
	pGetMessageW              = modUser32.NewProc("GetMessageW")
	pTranslateMessage         = modUser32.NewProc("TranslateMessage")
	pDispatchMessageW         = modUser32.NewProc("DispatchMessageW")
	pPostQuitMessage          = modUser32.NewProc("PostQuitMessage")
	pPostMessageW             = modUser32.NewProc("PostMessageW")
	pRegisterWindowMessageW   = modUser32.NewProc("RegisterWindowMessageW")
	pCreatePopupMenu          = modUser32.NewProc("CreatePopupMenu")
	pAppendMenuW              = modUser32.NewProc("AppendMenuW")
	pDestroyMenu              = modUser32.NewProc("DestroyMenu")
	pTrackPopupMenu           = modUser32.NewProc("TrackPopupMenu")
	pGetCursorPos             = modUser32.NewProc("GetCursorPos")
	pDestroyIcon              = modUser32.NewProc("DestroyIcon")
	pCreateIconFromResourceEx = modUser32.NewProc("CreateIconFromResourceEx")

	// --- shell32 ---
	pShell_NotifyIconW = modShell32.NewProc("Shell_NotifyIconW") // tray

	// --- wininet ---
	procInternetSetOption = modWininet.NewProc("InternetSetOptionW") // proxy
)
