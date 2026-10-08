package sys

import "golang.org/x/sys/windows"

var (
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modWininet  = windows.NewLazySystemDLL("wininet.dll")
	modRasapi32 = windows.NewLazySystemDLL("rasapi32.dll")
)
