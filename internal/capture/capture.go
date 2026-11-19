// Package capture grabs the screen.
package capture

import (
	"fmt"
	"image"

	"github.com/kbinani/screenshot"
)

// Frame is a captured screen image in 32-bit BGRA or RGBA.
type Frame struct {
	Pix    []byte
	Stride int
	W, H   int
	BGRA   bool // byte order B,G,R,A (else R,G,B,A)
}

// Capturer captures one display.
type Capturer interface {
	// Bounds is the display rectangle in the coordinate space the input
	// injector uses (pixels on Windows/X11, points on macOS).
	Bounds() image.Rectangle
	// Capture returns the current screen. The frame is valid until the next call.
	Capture() (*Frame, error)
	Close()
}

// Displays lists the bounds of active displays.
func Displays() []image.Rectangle {
	n := screenshot.NumActiveDisplays()
	out := make([]image.Rectangle, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, screenshot.GetDisplayBounds(i))
	}
	return out
}

func displayBounds(i int) (image.Rectangle, error) {
	n := screenshot.NumActiveDisplays()
	if n == 0 {
		return image.Rectangle{}, fmt.Errorf("no active displays found")
	}
	if i < 0 || i >= n {
		return image.Rectangle{}, fmt.Errorf("display %d does not exist (have %d)", i, n)
	}
	return screenshot.GetDisplayBounds(i), nil
}

// generic captures through kbinani/screenshot (GDI on Windows,
// ScreenCaptureKit/CoreGraphics on macOS).
type generic struct {
	display int
	bounds  image.Rectangle
	frame   Frame
}

func newGeneric(display int) (Capturer, error) {
	b, err := displayBounds(display)
	if err != nil {
		return nil, err
	}
	return &generic{display: display, bounds: b}, nil
}

func (g *generic) Bounds() image.Rectangle { return g.bounds }

func (g *generic) Capture() (*Frame, error) {
	img, err := screenshot.CaptureDisplay(g.display)
	if err != nil {
		return nil, err
	}
	g.frame = Frame{Pix: img.Pix, Stride: img.Stride, W: img.Rect.Dx(), H: img.Rect.Dy()}
	return &g.frame, nil
}

func (g *generic) Close() {}
