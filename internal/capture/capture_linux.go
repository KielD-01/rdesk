//go:build linux

package capture

/*
#cgo LDFLAGS: -lX11 -lXext
#include <stdlib.h>
#include <sys/ipc.h>
#include <sys/shm.h>
#include <X11/Xlib.h>
#include <X11/Xutil.h>
#include <X11/extensions/XShm.h>

typedef struct {
	Display *d;
	XImage *img;
	XShmSegmentInfo shm;
	int x, y, w, h;
} rd_cap;

static rd_cap *rd_cap_open(int x, int y, int w, int h, char **err) {
	XInitThreads();
	Display *d = XOpenDisplay(NULL);
	if (!d) { *err = "cannot open X display (is DISPLAY set?)"; return NULL; }
	if (!XShmQueryExtension(d)) { XCloseDisplay(d); *err = "X server has no MIT-SHM extension"; return NULL; }
	int scr = DefaultScreen(d);
	rd_cap *c = calloc(1, sizeof(rd_cap));
	c->d = d; c->x = x; c->y = y; c->w = w; c->h = h;
	c->img = XShmCreateImage(d, DefaultVisual(d, scr), DefaultDepth(d, scr), ZPixmap, NULL, &c->shm, w, h);
	if (!c->img || c->img->bits_per_pixel != 32) {
		*err = "unsupported X visual (need 32 bits per pixel)";
		if (c->img) XDestroyImage(c->img);
		XCloseDisplay(d); free(c); return NULL;
	}
	c->shm.shmid = shmget(IPC_PRIVATE, c->img->bytes_per_line * c->img->height, IPC_CREAT | 0600);
	if (c->shm.shmid < 0) { *err = "shmget failed"; XDestroyImage(c->img); XCloseDisplay(d); free(c); return NULL; }
	c->shm.shmaddr = c->img->data = shmat(c->shm.shmid, NULL, 0);
	c->shm.readOnly = False;
	XShmAttach(d, &c->shm);
	XSync(d, False);
	shmctl(c->shm.shmid, IPC_RMID, NULL); // freed automatically once detached
	return c;
}

static int rd_cap_grab(rd_cap *c) {
	return XShmGetImage(c->d, DefaultRootWindow(c->d), c->img, c->x, c->y, AllPlanes) ? 0 : -1;
}

static void rd_cap_close(rd_cap *c) {
	XShmDetach(c->d, &c->shm);
	XSync(c->d, False);
	c->img->data = NULL; // shared memory, not malloc'd: keep XDestroyImage off it
	XDestroyImage(c->img);
	shmdt(c->shm.shmaddr);
	XCloseDisplay(c->d);
	free(c);
}

static int rd_root_size(int *w, int *h) {
	Display *d = XOpenDisplay(NULL);
	if (!d) return -1;
	*w = DisplayWidth(d, DefaultScreen(d));
	*h = DisplayHeight(d, DefaultScreen(d));
	XCloseDisplay(d);
	return 0;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"strings"
	"unsafe"

	"github.com/jezek/xgb"
)

// The screenshot library logs harmless X auth fallbacks through xgb.
func init() { xgb.Logger = log.New(io.Discard, "", 0) }

type x11 struct {
	c      *C.rd_cap
	bounds image.Rectangle
	frame  Frame
}

// IsWayland reports whether we run in a Wayland session (X11 capture of
// the root window only shows XWayland clients there).
func IsWayland() bool {
	return strings.EqualFold(os.Getenv("XDG_SESSION_TYPE"), "wayland") || os.Getenv("WAYLAND_DISPLAY") != ""
}

// New opens a capturer for display index i.
func New(display int) (Capturer, error) {
	if IsWayland() {
		return nil, errors.New("Wayland session detected: screen capture currently needs an X11 session " +
			"(pick \"Xorg\"/\"X11\" on the login screen); Wayland support via xdg-desktop-portal is planned")
	}
	var b image.Rectangle
	if display < 0 { // whole X screen (all monitors)
		var w, h C.int
		if C.rd_root_size(&w, &h) != 0 {
			return nil, errors.New("cannot open X display (is DISPLAY set?)")
		}
		b = image.Rect(0, 0, int(w), int(h))
	} else {
		var err error
		if b, err = displayBounds(display); err != nil {
			return nil, err
		}
	}
	var cerr *C.char
	c := C.rd_cap_open(C.int(b.Min.X), C.int(b.Min.Y), C.int(b.Dx()), C.int(b.Dy()), &cerr)
	if c == nil {
		return nil, fmt.Errorf("x11 capture: %s", C.GoString(cerr))
	}
	return &x11{c: c, bounds: b}, nil
}

func (x *x11) Bounds() image.Rectangle { return x.bounds }

func (x *x11) Capture() (*Frame, error) {
	if C.rd_cap_grab(x.c) != 0 {
		return nil, errors.New("XShmGetImage failed")
	}
	img := x.c.img
	n := int(img.bytes_per_line) * int(img.height)
	x.frame = Frame{
		Pix:    unsafe.Slice((*byte)(unsafe.Pointer(img.data)), n),
		Stride: int(img.bytes_per_line),
		W:      int(img.width),
		H:      int(img.height),
		BGRA:   true,
	}
	return &x.frame, nil
}

func (x *x11) Close() {
	if x.c != nil {
		C.rd_cap_close(x.c)
		x.c = nil
	}
}

// Preflight is a no-op on Linux.
func Preflight() error { return nil }
