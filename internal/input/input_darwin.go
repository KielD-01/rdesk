//go:build darwin

package input

/*
#cgo LDFLAGS: -framework CoreGraphics -framework ApplicationServices
#include <ApplicationServices/ApplicationServices.h>

static void rd_mouse(CGEventType t, double x, double y, CGMouseButton b, int clicks, CGEventFlags flags) {
	CGEventRef e = CGEventCreateMouseEvent(NULL, t, CGPointMake(x, y), b);
	if (!e) return;
	if (clicks > 0) CGEventSetIntegerValueField(e, kCGMouseEventClickState, clicks);
	CGEventSetFlags(e, flags);
	CGEventPost(kCGHIDEventTap, e);
	CFRelease(e);
}

static void rd_scroll(int dy, int dx) {
	CGEventRef e = CGEventCreateScrollWheelEvent(NULL, kCGScrollEventUnitLine, 2, dy, dx);
	if (!e) return;
	CGEventPost(kCGHIDEventTap, e);
	CFRelease(e);
}

static void rd_key(CGKeyCode k, int down, CGEventFlags flags) {
	CGEventRef e = CGEventCreateKeyboardEvent(NULL, k, down ? true : false);
	if (!e) return;
	CGEventSetFlags(e, flags);
	CGEventPost(kCGHIDEventTap, e);
	CFRelease(e);
}
*/
import "C"

import (
	"fmt"
	"sync"
	"time"
)

type mac struct {
	mu      sync.Mutex
	x, y    float64
	buttons [3]bool
	flags   C.CGEventFlags

	lastDown   time.Time
	lastButton int
	clicks     int
}

// New returns a CoreGraphics event injector (needs the Accessibility permission).
func New() (Injector, error) { return &mac{}, nil }

func (m *mac) Move(x, y int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.x, m.y = float64(x), float64(y)
	t, b := C.CGEventType(C.kCGEventMouseMoved), C.CGMouseButton(C.kCGMouseButtonLeft)
	switch {
	case m.buttons[0]:
		t = C.CGEventType(C.kCGEventLeftMouseDragged)
	case m.buttons[2]:
		t, b = C.CGEventType(C.kCGEventRightMouseDragged), C.CGMouseButton(C.kCGMouseButtonRight)
	case m.buttons[1]:
		t, b = C.CGEventType(C.kCGEventOtherMouseDragged), C.CGMouseButton(C.kCGMouseButtonCenter)
	}
	C.rd_mouse(t, C.double(m.x), C.double(m.y), b, 0, m.flags)
	return nil
}

func (m *mac) Button(b int, down bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var t C.CGEventType
	var cb C.CGMouseButton
	switch b {
	case ButtonLeft:
		cb = C.CGMouseButton(C.kCGMouseButtonLeft)
		t = pickT(down, C.CGEventType(C.kCGEventLeftMouseDown), C.CGEventType(C.kCGEventLeftMouseUp))
	case ButtonRight:
		cb = C.CGMouseButton(C.kCGMouseButtonRight)
		t = pickT(down, C.CGEventType(C.kCGEventRightMouseDown), C.CGEventType(C.kCGEventRightMouseUp))
	case ButtonMiddle:
		cb = C.CGMouseButton(C.kCGMouseButtonCenter)
		t = pickT(down, C.CGEventType(C.kCGEventOtherMouseDown), C.CGEventType(C.kCGEventOtherMouseUp))
	default:
		return fmt.Errorf("unsupported button %d", b)
	}
	if down {
		if b == m.lastButton && time.Since(m.lastDown) < 400*time.Millisecond {
			m.clicks++
		} else {
			m.clicks = 1
		}
		m.lastDown, m.lastButton = time.Now(), b
	}
	m.buttons[b] = down
	C.rd_mouse(t, C.double(m.x), C.double(m.y), cb, C.int(m.clicks), m.flags)
	return nil
}

func (m *mac) Scroll(dx, dy int) error {
	C.rd_scroll(C.int(-dy), C.int(-dx))
	return nil
}

var macModifiers = map[string]C.CGEventFlags{
	"ShiftLeft": C.CGEventFlags(C.kCGEventFlagMaskShift), "ShiftRight": C.CGEventFlags(C.kCGEventFlagMaskShift),
	"ControlLeft": C.CGEventFlags(C.kCGEventFlagMaskControl), "ControlRight": C.CGEventFlags(C.kCGEventFlagMaskControl),
	"AltLeft": C.CGEventFlags(C.kCGEventFlagMaskAlternate), "AltRight": C.CGEventFlags(C.kCGEventFlagMaskAlternate),
	"MetaLeft": C.CGEventFlags(C.kCGEventFlagMaskCommand), "MetaRight": C.CGEventFlags(C.kCGEventFlagMaskCommand),
	"OSLeft": C.CGEventFlags(C.kCGEventFlagMaskCommand), "OSRight": C.CGEventFlags(C.kCGEventFlagMaskCommand),
}

func (m *mac) Key(code string, down bool) error {
	k, ok := keys[code]
	if !ok || k.mac == noMac {
		return fmt.Errorf("unknown key %q", code)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if f, ok := macModifiers[code]; ok {
		if down {
			m.flags |= f
		} else {
			m.flags &^= f
		}
	}
	C.rd_key(C.CGKeyCode(k.mac), cbool(down), m.flags)
	return nil
}

func (m *mac) Close() {}

func pickT(c bool, a, b C.CGEventType) C.CGEventType {
	if c {
		return a
	}
	return b
}

func cbool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}
