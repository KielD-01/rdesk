// rdesk: the host agent. Run it on the machine you want to reach.
// It opens a window (ID, password, connections); -headless runs it in the console.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"rdesk/internal/capture"
	"rdesk/internal/config"
	"rdesk/internal/gui"
	"rdesk/internal/host"
)

type flags struct {
	display   int
	fps       int
	bitrate   int
	maxWidth  int
	audioDev  string
	downloads string
}

func main() {
	serverURL := flag.String("server", "", "rendezvous server URL (remembered); default "+config.DefaultServer)
	password := flag.String("password", "", "set a permanent password for unattended access (remembered); \"-\" removes it")
	headless := flag.Bool("headless", false, "no window: print the ID and password to the console")
	var f flags
	flag.IntVar(&f.display, "display", 0, "display index to share (Linux: -1 = all monitors)")
	flag.IntVar(&f.fps, "fps", 30, "max frames per second")
	flag.IntVar(&f.bitrate, "bitrate", 6000, "max video bitrate, kbit/s")
	flag.IntVar(&f.maxWidth, "max-width", 2560, "downscale screens wider than this")
	noAudio := flag.Bool("no-audio", false, "do not stream system audio (remembered)")
	flag.StringVar(&f.audioDev, "audio-device", "", "capture device name (substring) for system audio")
	viewOnly := flag.Bool("view-only", false, "ignore the viewer's mouse and keyboard (remembered)")
	flag.StringVar(&f.downloads, "downloads", "", "where uploaded files go (default ~/Downloads/rdesk)")
	listDisplays := flag.Bool("list-displays", false, "print displays and exit")
	verbose := flag.Bool("v", false, "verbose logs")
	flag.Parse()

	lvl := slog.LevelInfo
	if *verbose {
		lvl = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))

	if *listDisplays {
		for i, b := range capture.Displays() {
			fmt.Printf("%d: %dx%d at %d,%d\n", i, b.Dx(), b.Dy(), b.Min.X, b.Min.Y)
		}
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fatal("reading %s: %v", config.Path(), err)
	}
	changed := false
	if *serverURL != "" {
		cfg.Server, changed = strings.TrimRight(*serverURL, "/"), true
	}
	switch *password {
	case "":
	case "-":
		cfg.Password, changed = "", true
	default:
		if len(*password) < 8 {
			fatal("a permanent password needs at least 8 characters")
		}
		cfg.Password, changed = *password, true
	}
	flag.Visit(func(fl *flag.Flag) {
		switch fl.Name {
		case "no-audio":
			cfg.NoAudio, changed = *noAudio, true
		case "view-only":
			cfg.ViewOnly, changed = *viewOnly, true
		}
	})
	if changed {
		if err := config.Save(cfg); err != nil {
			fatal("saving config: %v", err)
		}
	}

	if err := capture.Preflight(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	if f.downloads == "" {
		home, _ := os.UserHomeDir()
		f.downloads = filepath.Join(home, "Downloads", "rdesk")
	}

	options := func(c config.Config) host.Options {
		return host.Options{
			Server:   c.Server,
			Token:    c.Token,
			Password: c.Password,
			Video: host.VideoOptions{
				Display: f.display, FPS: f.fps, Bitrate: f.bitrate * 1000, MaxWidth: f.maxWidth,
			},
			Audio:    !c.NoAudio,
			AudioDev: f.audioDev,
			Input:    !c.ViewOnly,
			Download: f.downloads,
		}
	}

	if *headless || !haveDisplay() {
		runHeadless(options(cfg), log)
		return
	}
	gui.Run(cfg, options, log)
}

// haveDisplay reports whether a window can be shown.
func haveDisplay() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

func runHeadless(o host.Options, log *slog.Logger) {
	o.OnCredentials = func(id, pw string, oneTime bool, url string) {
		fmt.Println()
		fmt.Println("  ┌─ rdesk host ready ─────────────────────────")
		fmt.Printf("  │ ID:       %s %s %s\n", id[0:3], id[3:6], id[6:])
		if oneTime {
			fmt.Printf("  │ Password: %s   (one-time, changes after each session)\n", pw)
		} else {
			fmt.Println("  │ Password: (permanent password from config)")
		}
		fmt.Printf("  │ Connect:  %s\n", url)
		fmt.Println("  └────────────────────────────────────────────")
		fmt.Println()
	}
	h := host.New(o, log)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Info("connecting", "server", o.Server)
	h.Run(ctx)
	h.Close()
}

func fatal(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "rdesk: "+f+"\n", a...)
	os.Exit(1)
}
