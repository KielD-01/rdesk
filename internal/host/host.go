// Package host implements the rdesk host agent: it registers with the
// rendezvous server, accepts viewer sessions over WebRTC and streams the
// screen and audio while injecting the viewer's input.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"rdesk/internal/auth"
	"rdesk/internal/input"
	"rdesk/internal/signal"
)

// Options configures the host.
type Options struct {
	Server   string // https://rdesk.example.com
	Token    string // secret that owns our ID
	Password string // permanent password; empty = one-time codes
	Video    VideoOptions
	Audio    bool
	AudioDev string
	Input    bool   // allow remote control
	Download string // directory for uploaded files
	// OnCredentials is called whenever the ID or password to show changes.
	OnCredentials func(id, password string, oneTime bool, viewerURL string)
}

type Host struct {
	opt Options
	log *slog.Logger

	video *videoPipe
	audio *audioPipe
	clip  *clipboardSync

	inMu sync.Mutex
	in   input.Injector

	mu       sync.Mutex
	id       string
	password string
	failures int
	sessions map[string]*session
	rates    map[*session]int
	out      chan signal.Msg
}

func New(opt Options, log *slog.Logger) *Host {
	h := &Host{
		opt:      opt,
		log:      log,
		video:    newVideoPipe(opt.Video, log),
		sessions: map[string]*session{},
	}
	if opt.Audio {
		h.audio = newAudioPipe(opt.AudioDev, log)
	}
	h.clip = newClipboardSync(log, h.broadcastClipboard)
	h.video.onSize = h.onScreenSize
	h.password = opt.Password
	if h.password == "" {
		h.password = auth.OneTimeCode()
	}
	return h
}

// Run keeps the host registered with the server until ctx ends.
func (h *Host) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		start := time.Now()
		err := h.connect(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		h.log.Warn("disconnected from server, retrying", "err", err, "in", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func wsURL(server, path string) string {
	u := strings.TrimRight(server, "/")
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	case !strings.HasPrefix(u, "ws"):
		u = "wss://" + u
	}
	return u + path
}

func (h *Host) connect(ctx context.Context) error {
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	c, _, err := websocket.Dial(dctx, wsURL(h.opt.Server, "/ws/host"), &websocket.DialOptions{
		HTTPHeader: http.Header{"User-Agent": []string{"rdesk-host"}},
	})
	cancel()
	if err != nil {
		return err
	}
	c.SetReadLimit(1 << 20)
	defer c.CloseNow()
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	out := make(chan signal.Msg, 256)
	h.mu.Lock()
	h.out = out
	h.mu.Unlock()
	go func() {
		ping := time.NewTicker(25 * time.Second)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-out:
				b, _ := json.Marshal(m)
				wctx, c2 := context.WithTimeout(ctx, 10*time.Second)
				err := c.Write(wctx, websocket.MessageText, b)
				c2()
				if err != nil {
					stop()
					return
				}
			case <-ping.C:
				pctx, c2 := context.WithTimeout(ctx, 10*time.Second)
				err := c.Ping(pctx)
				c2()
				if err != nil {
					stop()
					return
				}
			}
		}
	}()

	out <- signal.Msg{T: signal.TRegister, Token: h.opt.Token}
	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			return err
		}
		var m signal.Msg
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		switch m.T {
		case signal.TRegistered:
			h.mu.Lock()
			h.id = m.ID
			h.mu.Unlock()
			h.showCredentials()
		case signal.TSession:
			h.startSession(m.Sid, m.ICE)
		case signal.TSignal:
			h.mu.Lock()
			s := h.sessions[m.Sid]
			h.mu.Unlock()
			if s != nil {
				s.onSignal(m.Data)
			}
		case signal.TBye:
			h.mu.Lock()
			s := h.sessions[m.Sid]
			h.mu.Unlock()
			if s != nil {
				s.signalingGone()
			}
		case signal.TError:
			return fmt.Errorf("server: %s", m.Error)
		}
	}
}

// send queues a signaling message; dropped when offline.
func (h *Host) send(m signal.Msg) {
	h.mu.Lock()
	out := h.out
	h.mu.Unlock()
	if out == nil {
		return
	}
	select {
	case out <- m:
	default:
	}
}

func (h *Host) showCredentials() {
	h.mu.Lock()
	id, pw := h.id, h.password
	h.mu.Unlock()
	if h.opt.OnCredentials != nil && id != "" {
		h.opt.OnCredentials(id, pw, h.opt.Password == "", strings.TrimRight(h.opt.Server, "/")+"/#"+id)
	}
}

// currentPassword returns the password a viewer must prove.
func (h *Host) currentPassword() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.password
}

// authFailed rotates a one-time code after repeated failures.
func (h *Host) authFailed() {
	h.mu.Lock()
	h.failures++
	rotate := h.opt.Password == "" && h.failures >= 5
	if rotate {
		h.failures = 0
		h.password = auth.OneTimeCode()
	}
	h.mu.Unlock()
	if rotate {
		h.log.Warn("too many wrong passwords: one-time code changed")
		h.showCredentials()
	}
}

func (h *Host) authOK() {
	h.mu.Lock()
	h.failures = 0
	h.mu.Unlock()
}

// sessionEnded rotates a one-time code once no authenticated session is left.
func (h *Host) sessionEnded(s *session) {
	h.mu.Lock()
	delete(h.sessions, s.sid)
	anyAuthed := false
	for _, o := range h.sessions {
		if o.authed.Load() {
			anyAuthed = true
		}
	}
	rotate := s.authed.Load() && !anyAuthed && h.opt.Password == ""
	if rotate {
		h.password = auth.OneTimeCode()
	}
	h.mu.Unlock()
	if rotate {
		h.showCredentials()
	}
}

func (h *Host) injector() input.Injector {
	h.inMu.Lock()
	defer h.inMu.Unlock()
	if h.in == nil {
		in, err := input.New()
		if err != nil {
			h.log.Error("input injection unavailable", "err", err)
			return nil
		}
		h.in = in
	}
	return h.in
}

func (h *Host) authedSessions() []*session {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*session
	for _, s := range h.sessions {
		if s.authed.Load() {
			out = append(out, s)
		}
	}
	return out
}

func (h *Host) broadcastClipboard(text string) {
	for _, s := range h.authedSessions() {
		s.sendControl(ctrl{T: "clip", Text: text})
	}
}

func (h *Host) onScreenSize(_ image.Rectangle, size image.Point) {
	for _, s := range h.authedSessions() {
		s.sendControl(ctrl{T: "screen", W: size.X, H: size.Y})
	}
}

// updateBitrate records a session's bandwidth estimate; the shared encoder
// follows the slowest viewer.
func (h *Host) updateBitrate(s *session, bps int) {
	h.mu.Lock()
	if h.rates == nil {
		h.rates = map[*session]int{}
	}
	h.rates[s] = bps
	lowest := math.MaxInt
	for _, r := range h.rates {
		lowest = min(lowest, r)
	}
	h.mu.Unlock()
	h.video.setBitrate(lowest)
}

func (h *Host) removeBitrate(s *session) {
	h.mu.Lock()
	delete(h.rates, s)
	h.mu.Unlock()
}

func itoa(i int) string { return strconv.Itoa(i) }
