//go:build linux

package input

/*
#cgo LDFLAGS: -lX11 -lXtst
#include <X11/Xlib.h>
#include <X11/extensions/XTest.h>

static Display *rd_in_open(void) {
	XInitThreads();
	Display *d = XOpenDisplay(NULL);
	if (!d) return NULL;
	int ev, er, maj, min;
	if (!XTestQueryExtension(d, &ev, &er, &maj, &min)) { XCloseDisplay(d); return NULL; }
	return d;
}
static void rd_move(Display *d, int x, int y) { XTestFakeMotionEvent(d, -1, x, y, CurrentTime); XFlush(d); }
static void rd_button(Display *d, unsigned b, int down) { XTestFakeButtonEvent(d, b, down, CurrentTime); XFlush(d); }
static void rd_key(Display *d, unsigned kc, int down) { XTestFakeKeyEvent(d, kc, down, CurrentTime); XFlush(d); }
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
)

type x11 struct {
	mu sync.Mutex
	d  *C.Display
}

// New opens an X11 XTest injector.
func New() (Injector, error) {
	d := C.rd_in_open()
	if d == nil {
		return nil, errors.New("cannot open X display with the XTEST extension")
	}
	return &x11{d: d}, nil
}

func (x *x11) Move(px, py int) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	C.rd_move(x.d, C.int(px), C.int(py))
	return nil
}

// X buttons: 1 left, 2 middle, 3 right, 8 back, 9 forward.
var xButtons = map[int]C.uint{0: 1, 1: 2, 2: 3, 3: 8, 4: 9}

func (x *x11) Button(b int, down bool) error {
	xb, ok := xButtons[b]
	if !ok {
		return fmt.Errorf("unknown button %d", b)
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	C.rd_button(x.d, xb, cbool(down))
	return nil
}

func (x *x11) Scroll(dx, dy int) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	click := func(b C.uint, n int) {
		for i := 0; i < n && i < 50; i++ {
			C.rd_button(x.d, b, 1)
			C.rd_button(x.d, b, 0)
		}
	}
	if dy > 0 {
		click(5, dy)
	} else if dy < 0 {
		click(4, -dy)
	}
	if dx > 0 {
		click(7, dx)
	} else if dx < 0 {
		click(6, -dx)
	}
	return nil
}

func (x *x11) Key(code string, down bool) error {
	k, ok := keys[code]
	if !ok {
		return fmt.Errorf("unknown key %q", code)
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	C.rd_key(x.d, C.uint(k.evdev+8), cbool(down))
	return nil
}

func (x *x11) Close() {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.d != nil {
		C.XCloseDisplay(x.d)
		x.d = nil
	}
}

func cbool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}
