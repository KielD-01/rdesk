//go:build windows

package input

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	procSendInput    = user32.NewProc("SendInput")
	procGetSysMetric = user32.NewProc("GetSystemMetrics")
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseMove       = 0x0001
	mouseLeftDown   = 0x0002
	mouseLeftUp     = 0x0004
	mouseRightDown  = 0x0008
	mouseRightUp    = 0x0010
	mouseMiddleDown = 0x0020
	mouseMiddleUp   = 0x0040
	mouseXDown      = 0x0080
	mouseXUp        = 0x0100
	mouseWheel      = 0x0800
	mouseHWheel     = 0x1000
	mouseVirtual    = 0x4000
	mouseAbsolute   = 0x8000

	keyExtended = 0x0001
	keyUp       = 0x0002
	keyScancode = 0x0008

	wheelDelta = 120
)

type mouseInput struct {
	typ       uint32
	_         uint32
	dx, dy    int32
	mouseData uint32
	flags     uint32
	time      uint32
	extra     uintptr
}

type keybdInput struct {
	typ   uint32
	_     uint32
	vk    uint16
	scan  uint16
	flags uint32
	time  uint32
	extra uintptr
	_     [8]byte // pad to sizeof(INPUT) = 40
}

type win struct{ mu sync.Mutex }

// New returns a SendInput based injector.
func New() (Injector, error) { return &win{}, nil }

func send(p unsafe.Pointer, size uintptr) error {
	n, _, err := procSendInput.Call(1, uintptr(p), size)
	if n != 1 {
		return fmt.Errorf("SendInput: %v (input blocked by UIPI? run rdesk as administrator to control elevated windows)", err)
	}
	return nil
}

func metric(i int) int {
	r, _, _ := procGetSysMetric.Call(uintptr(i))
	return int(int32(r))
}

func (w *win) mouse(flags, data uint32, dx, dy int32) error {
	in := mouseInput{typ: inputMouse, dx: dx, dy: dy, mouseData: data, flags: flags}
	return send(unsafe.Pointer(&in), unsafe.Sizeof(in))
}

func (w *win) Move(x, y int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	vx, vy, vw, vh := metric(76), metric(77), metric(78), metric(79)
	if vw <= 1 || vh <= 1 {
		return fmt.Errorf("bad virtual screen size")
	}
	nx := int32((x - vx) * 65535 / (vw - 1))
	ny := int32((y - vy) * 65535 / (vh - 1))
	return w.mouse(mouseMove|mouseAbsolute|mouseVirtual, 0, nx, ny)
}

func (w *win) Button(b int, down bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var f, data uint32
	switch b {
	case ButtonLeft:
		f = pick(down, mouseLeftDown, mouseLeftUp)
	case ButtonMiddle:
		f = pick(down, mouseMiddleDown, mouseMiddleUp)
	case ButtonRight:
		f = pick(down, mouseRightDown, mouseRightUp)
	case 3, 4:
		f = pick(down, mouseXDown, mouseXUp)
		data = uint32(b - 2) // XBUTTON1 / XBUTTON2
	default:
		return fmt.Errorf("unknown button %d", b)
	}
	return w.mouse(f, data, 0, 0)
}

func (w *win) Scroll(dx, dy int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if dy != 0 {
		if err := w.mouse(mouseWheel, uint32(int32(-dy*wheelDelta)), 0, 0); err != nil {
			return err
		}
	}
	if dx != 0 {
		return w.mouse(mouseHWheel, uint32(int32(dx*wheelDelta)), 0, 0)
	}
	return nil
}

func (w *win) Key(code string, down bool) error {
	k, ok := keys[code]
	if !ok {
		return fmt.Errorf("unknown key %q", code)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	in := keybdInput{typ: inputKeyboard}
	switch code {
	case "Pause": // E1-prefixed in set 1; use the virtual key
		in.vk = 0x13
	case "NumLock":
		in.vk = 0x90
		in.flags = keyExtended
	default:
		in.scan = k.win
		in.flags = keyScancode
		if k.ext {
			in.flags |= keyExtended
		}
	}
	if !down {
		in.flags |= keyUp
	}
	return send(unsafe.Pointer(&in), unsafe.Sizeof(in))
}

func (w *win) Close() {}

func pick(c bool, a, b uint32) uint32 {
	if c {
		return a
	}
	return b
}
