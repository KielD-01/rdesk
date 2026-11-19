//go:build windows

package capture

import "golang.org/x/sys/windows"

// preflight makes the process per-monitor DPI aware so that capture and
// input coordinates are physical pixels.
func preflight() error {
	user32 := windows.NewLazySystemDLL("user32.dll")
	if p := user32.NewProc("SetProcessDpiAwarenessContext"); p.Find() == nil {
		// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4
		p.Call(^uintptr(3))
		return nil
	}
	if p := user32.NewProc("SetProcessDPIAware"); p.Find() == nil {
		p.Call()
	}
	return nil
}
