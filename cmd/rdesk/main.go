// rdesk: the host agent. Run it on the machine you want to reach.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"rdesk/internal/capture"
	"rdesk/internal/host"
)

type config struct {
	Server   string `json:"server"`
	Token    string `json:"token"`
	Password string `json:"password,omitempty"`
}

func configPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "rdesk", "config.json")
}

func loadConfig() (config, error) {
	var c config
	b, err := os.ReadFile(configPath())
	if err == nil {
		err = json.Unmarshal(b, &c)
	} else if os.IsNotExist(err) {
		err = nil
	}
	return c, err
}

func saveConfig(c config) error {
	p := configPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, b, 0o600)
}

func main() {
	serverURL := flag.String("server", "", "rendezvous server URL, e.g. https://rdesk.example.com (remembered)")
	password := flag.String("password", "", "set a permanent password for unattended access (remembered); \"-\" removes it")
	display := flag.Int("display", 0, "display index to share (Linux: -1 = all monitors)")
	fps := flag.Int("fps", 30, "max frames per second")
	bitrate := flag.Int("bitrate", 6000, "max video bitrate, kbit/s")
	maxWidth := flag.Int("max-width", 2560, "downscale screens wider than this")
	noAudio := flag.Bool("no-audio", false, "do not stream system audio")
	audioDev := flag.String("audio-device", "", "capture device name (substring) for system audio")
	viewOnly := flag.Bool("view-only", false, "ignore the viewer's mouse and keyboard")
	downloads := flag.String("downloads", "", "where uploaded files go (default ~/Downloads/rdesk)")
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

	cfg, err := loadConfig()
	if err != nil {
		fatal("reading %s: %v", configPath(), err)
	}
	changed := false
	if *serverURL != "" && *serverURL != cfg.Server {
		cfg.Server, changed = strings.TrimRight(*serverURL, "/"), true
	}
	if cfg.Token == "" {
		b := make([]byte, 32)
		rand.Read(b)
		cfg.Token, changed = base64.RawURLEncoding.EncodeToString(b), true
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
	if cfg.Server == "" {
		fatal("no server configured: run once with -server https://your-rdesk-server")
	}
	if changed {
		if err := saveConfig(cfg); err != nil {
			fatal("saving config: %v", err)
		}
	}

	if err := capture.Preflight(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}

	dl := *downloads
	if dl == "" {
		home, _ := os.UserHomeDir()
		dl = filepath.Join(home, "Downloads", "rdesk")
	}

	h := host.New(host.Options{
		Server:   cfg.Server,
		Token:    cfg.Token,
		Password: cfg.Password,
		Video: host.VideoOptions{
			Display: *display, FPS: *fps, Bitrate: *bitrate * 1000, MaxWidth: *maxWidth,
		},
		Audio:    !*noAudio,
		AudioDev: *audioDev,
		Input:    !*viewOnly,
		Download: dl,
		OnCredentials: func(id, pw string, oneTime bool, url string) {
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
		},
	}, log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Info("connecting", "server", cfg.Server)
	h.Run(ctx)
}

func fatal(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "rdesk: "+f+"\n", a...)
	os.Exit(1)
}
