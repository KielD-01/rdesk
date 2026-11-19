//go:build darwin

package capture

/*
#cgo LDFLAGS: -framework CoreGraphics -framework ApplicationServices
#include <CoreGraphics/CoreGraphics.h>
#include <ApplicationServices/ApplicationServices.h>

static int rd_screen_access(void) {
	if (CGPreflightScreenCaptureAccess()) return 1;
	CGRequestScreenCaptureAccess(); // shows the system prompt once
	return 0;
}

static int rd_accessibility_access(void) {
	const void *keys[] = { kAXTrustedCheckOptionPrompt };
	const void *vals[] = { kCFBooleanTrue };
	CFDictionaryRef opts = CFDictionaryCreate(NULL, keys, vals, 1,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	Boolean ok = AXIsProcessTrustedWithOptions(opts);
	CFRelease(opts);
	return ok ? 1 : 0;
}
*/
import "C"

import "errors"

// preflight checks (and requests) the macOS Screen Recording and
// Accessibility permissions rdesk needs.
func preflight() error {
	var msg string
	if C.rd_screen_access() == 0 {
		msg += "Screen Recording permission is missing: System Settings → Privacy & Security → Screen & System Audio Recording → enable your terminal (or rdesk), then restart rdesk.\n"
	}
	if C.rd_accessibility_access() == 0 {
		msg += "Accessibility permission is missing (needed for mouse/keyboard): System Settings → Privacy & Security → Accessibility → enable your terminal (or rdesk), then restart rdesk.\n"
	}
	if msg != "" {
		return errors.New(msg)
	}
	return nil
}
