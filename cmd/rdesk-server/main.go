// rdesk-server: signaling + TURN/STUN relay + browser viewer for rdesk.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"rdesk/internal/server"
	"rdesk/web"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	listen := flag.String("listen", env("RDESK_LISTEN", ":8080"), "HTTP listen address (put TLS in front of it)")
	turnListen := flag.String("turn-listen", env("RDESK_TURN_LISTEN", "0.0.0.0:3478"), "TURN/STUN listen address (UDP+TCP)")
	publicIP := flag.String("public-ip", env("RDESK_PUBLIC_IP", ""), "public IPv4 of this machine; enables the TURN relay")
	turnHost := flag.String("turn-host", env("RDESK_TURN_HOST", ""), "host name clients use for TURN/STUN (default: -public-ip)")
	relayPorts := flag.String("relay-ports", env("RDESK_RELAY_PORTS", "49160-49200"), "UDP port range for TURN relays")
	secret := flag.String("turn-secret", env("RDESK_TURN_SECRET", ""), "shared secret for TURN credentials (random if empty)")
	stun := flag.String("stun", env("RDESK_EXTRA_STUN", "stun:stun.l.google.com:19302"), "extra STUN URLs, comma separated")
	allowPrivate := flag.Bool("turn-allow-private", env("RDESK_TURN_ALLOW_PRIVATE", "") == "1", "let TURN relay to private/loopback peers (local testing only)")
	trustProxy := flag.Bool("trust-proxy", env("RDESK_TRUST_PROXY", "") == "1", "trust X-Forwarded-For (behind a reverse proxy)")
	health := flag.Bool("health", false, "check that the server on -listen answers, then exit (Docker healthcheck)")
	flag.Parse()

	if *health {
		_, port, _ := net.SplitHostPort(*listen)
		c := http.Client{Timeout: 3 * time.Second}
		res, err := c.Get("http://127.0.0.1:" + port + "/healthz")
		if err != nil || res.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg := server.Config{TrustProxy: *trustProxy, Web: web.FS}
	for _, u := range strings.Split(*stun, ",") {
		if u = strings.TrimSpace(u); u != "" {
			cfg.ExtraSTUN = append(cfg.ExtraSTUN, u)
		}
	}

	if *publicIP != "" {
		if *secret == "" {
			b := make([]byte, 24)
			rand.Read(b)
			*secret = hex.EncodeToString(b)
		}
		lo, hi, err := parseRange(*relayPorts)
		if err != nil {
			log.Error("bad -relay-ports", "err", err)
			os.Exit(2)
		}
		t, err := server.StartTURN(server.TURNConfig{
			Listen: *turnListen, PublicIP: *publicIP, MinPort: lo, MaxPort: hi,
			Secret: *secret, Realm: "rdesk", AllowPrivatePeers: *allowPrivate,
		}, log)
		if err != nil {
			log.Error("turn", "err", err)
			os.Exit(1)
		}
		defer t.Close()
		_, port, _ := net.SplitHostPort(*turnListen)
		cfg.TURN = true
		cfg.TURNHost = *publicIP
		if *turnHost != "" {
			cfg.TURNHost = *turnHost
		}
		cfg.TURNPort, _ = strconv.Atoi(port)
		cfg.TURNSecret = *secret
	} else {
		log.Warn("no -public-ip: TURN relay disabled, only direct/STUN connections will work")
	}

	srv := &http.Server{
		Addr:              *listen,
		Handler:           server.New(cfg, log).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Info("http listening", "addr", *listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("http", "err", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(sctx)
}

func parseRange(s string) (uint16, uint16, error) {
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		b = a
	}
	lo, err := strconv.ParseUint(strings.TrimSpace(a), 10, 16)
	if err != nil {
		return 0, 0, err
	}
	hi, err := strconv.ParseUint(strings.TrimSpace(b), 10, 16)
	if err != nil {
		return 0, 0, err
	}
	return uint16(lo), uint16(hi), nil
}
