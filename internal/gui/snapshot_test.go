package gui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/software"
	"fyne.io/fyne/v2/test"
)

// TestDesignSnapshots renders the window without OpenGL, so the
// design can be reviewed as PNGs: RDESK_SNAPSHOT_DIR=/tmp/x go test ./internal/gui
func TestDesignSnapshots(t *testing.T) {
	dir := os.Getenv("RDESK_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set RDESK_SNAPSHOT_DIR to render the design snapshots")
	}
	{
		p := darkPalette
		a := test.NewTempApp(t)
		a.Settings().SetTheme(rdeskTheme{p})
		u := &ui{app: a, p: p, seen: map[string]bool{}}
		u.win = a.NewWindow("rdesk")

		c := software.NewCanvas()
		c.SetPadded(false)
		c.SetScale(2)
		content := u.build()
		c.SetContent(content)
		u.setCredentials("887923931", "mgrg-cxz7", true)
		u.setStatus(true, nil)
		c.Resize(fyne.NewSize(windowWidth, content.MinSize().Height))

		f, err := os.Create(filepath.Join(dir, "window.png"))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(f, c.Capture()); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}
