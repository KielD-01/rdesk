// Package config stores the host's settings in the user's config directory.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// DefaultServer is used until another server is configured. Override at
// build time: -ldflags "-X rdesk/internal/config.DefaultServer=https://…".
var DefaultServer = "https://rdesk.romanovich.dev"

type Config struct {
	Server   string `json:"server"`
	Token    string `json:"token"`              // secret that owns this host's ID
	Password string `json:"password,omitempty"` // permanent password; empty = one-time codes
	ViewOnly bool   `json:"view_only,omitempty"`
	NoAudio  bool   `json:"no_audio,omitempty"`
}

// Path is the config file location.
func Path() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "rdesk", "config.json")
}

// Load reads the config, filling in a server and a fresh token when missing
// (and saving them, so the host keeps its ID).
func Load() (Config, error) {
	var c Config
	b, err := os.ReadFile(Path())
	if err == nil {
		if err = json.Unmarshal(b, &c); err != nil {
			return c, err
		}
	} else if !os.IsNotExist(err) {
		return c, err
	}
	changed := false
	if c.Server == "" {
		c.Server, changed = DefaultServer, true
	}
	if c.Token == "" {
		t := make([]byte, 32)
		rand.Read(t)
		c.Token, changed = base64.RawURLEncoding.EncodeToString(t), true
	}
	if changed {
		err = Save(c)
	}
	return c, err
}

// Save writes the config (readable by the user only).
func Save(c Config) error {
	c.Server = strings.TrimRight(strings.TrimSpace(c.Server), "/")
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(p, b, 0o600)
}
