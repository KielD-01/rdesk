package gui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
)

// palette is one visual style of the window.
type palette struct {
	bg, panel, panel2, border color.Color
	text, muted               color.Color
	accent, accent2           color.Color
	ok, warn, danger          color.Color
	input                     color.Color
	dark                      bool
}

var (
	// darkPalette matches the browser viewer (web/static/style.css).
	darkPalette = palette{
		bg: rgb(0x0d1117), panel: rgb(0x161b22), panel2: rgb(0x1f2630), border: rgb(0x2d3540),
		text: rgb(0xe6edf3), muted: rgb(0x8b96a3), accent: rgb(0x4f8cff), accent2: rgb(0x3a74e8),
		ok: rgb(0x3fb950), warn: rgb(0xd29922), danger: rgb(0xe5534b), input: rgb(0x0d1117), dark: true,
	}
)

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff}
}

type rdeskTheme struct{ p palette }

func (t rdeskTheme) Color(n fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	p := t.p
	switch n {
	case theme.ColorNameBackground:
		return p.bg
	case theme.ColorNameOverlayBackground, theme.ColorNameMenuBackground:
		return p.panel
	case theme.ColorNameInputBackground:
		return p.input
	case theme.ColorNameInputBorder, theme.ColorNameSeparator:
		return p.border
	case theme.ColorNameButton:
		return p.panel2
	case theme.ColorNameDisabledButton:
		return p.panel
	case theme.ColorNameForeground:
		return p.text
	case theme.ColorNameForegroundOnPrimary, theme.ColorNameForegroundOnError, theme.ColorNameForegroundOnSuccess:
		return color.White
	case theme.ColorNamePlaceHolder, theme.ColorNameDisabled:
		return p.muted
	case theme.ColorNamePrimary, theme.ColorNameFocus, theme.ColorNameHyperlink:
		return p.accent
	case theme.ColorNameHover:
		if p.dark {
			return color.NRGBA{0xff, 0xff, 0xff, 0x14}
		}
		return color.NRGBA{0, 0, 0, 0x0c}
	case theme.ColorNamePressed:
		if p.dark {
			return color.NRGBA{0xff, 0xff, 0xff, 0x24}
		}
		return color.NRGBA{0, 0, 0, 0x18}
	case theme.ColorNameSelection:
		return color.NRGBA{0x4f, 0x8c, 0xff, 0x55}
	case theme.ColorNameError:
		return p.danger
	case theme.ColorNameSuccess:
		return p.ok
	case theme.ColorNameWarning:
		return p.warn
	case theme.ColorNameShadow:
		if p.dark {
			return color.NRGBA{0, 0, 0, 0x66}
		}
		return color.NRGBA{0, 0, 0, 0x22}
	case theme.ColorNameScrollBar:
		return color.NRGBA{0x80, 0x80, 0x80, 0x50}
	case theme.ColorNameHeaderBackground:
		return p.panel2
	}
	v := theme.VariantLight
	if p.dark {
		v = theme.VariantDark
	}
	return theme.DefaultTheme().Color(n, v)
}

func (rdeskTheme) Font(s fyne.TextStyle) fyne.Resource     { return theme.DefaultTheme().Font(s) }
func (rdeskTheme) Icon(n fyne.ThemeIconName) fyne.Resource { return theme.DefaultTheme().Icon(n) }

func (rdeskTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		return 14
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		return 8
	case theme.SizeNamePadding:
		return 5
	case theme.SizeNameInnerPadding:
		return 10
	case theme.SizeNameInputBorder:
		return 1
	}
	return theme.DefaultTheme().Size(n)
}

func inset(h, v float32, o fyne.CanvasObject) fyne.CanvasObject {
	return container.New(&insetLayout{h, v}, o)
}

// text makes a styled canvas text.
func text(s string, size float32, c color.Color, bold, mono bool) *canvas.Text {
	t := canvas.NewText(s, c)
	t.TextSize = size
	t.TextStyle = fyne.TextStyle{Bold: bold, Monospace: mono}
	return t
}

// insetLayout pads its children by fixed horizontal/vertical amounts.
type insetLayout struct{ h, v float32 }

func (l *insetLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objs {
		o.Move(fyne.NewPos(l.h, l.v))
		o.Resize(fyne.NewSize(size.Width-2*l.h, size.Height-2*l.v))
	}
}

func (l *insetLayout) MinSize(objs []fyne.CanvasObject) fyne.Size {
	var m fyne.Size
	for _, o := range objs {
		m = m.Max(o.MinSize())
	}
	return m.Add(fyne.NewSize(2*l.h, 2*l.v))
}

// gap is fixed vertical/horizontal space.
func gap(w, h float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(w, h))
	return r
}

// dot is a small coloured circle.
func dot(c color.Color, d float32) (*canvas.Circle, fyne.CanvasObject) {
	circle := canvas.NewCircle(c)
	return circle, container.NewCenter(container.NewGridWrap(fyne.NewSize(d, d), circle))
}

// pill is a rounded status badge with a dot.
type pill struct {
	dot  *canvas.Circle
	text *canvas.Text
	obj  fyne.CanvasObject
}

func newPill(p palette, fill color.Color, textColor color.Color) *pill {
	pl := &pill{text: text("", 12, textColor, true, false)}
	var d fyne.CanvasObject
	pl.dot, d = dot(p.warn, 8)
	bg := canvas.NewRectangle(fill)
	bg.CornerRadius = 13
	pl.obj = container.NewStack(bg, inset(10, 5, container.NewHBox(d, pl.text)))
	return pl
}

func (pl *pill) set(s string, c color.Color) {
	pl.text.Text = s
	pl.dot.FillColor = c
	pl.text.Refresh()
	pl.dot.Refresh()
}
