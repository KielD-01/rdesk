// Package server implements the rdesk rendezvous server: WebSocket
// signaling between hosts and viewers, an embedded TURN/STUN relay and the
// static browser viewer.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/time/rate"

	"rdesk/internal/signal"
)

// Config configures the server.
type Config struct {
	// TURN/STUN
	TURN        bool
	TURNHost    string // host name/IP clients use to reach TURN/STUN
	TURNPort    int
	TURNSecret  string
	ExtraSTUN   []string
	TrustProxy  bool // take the client IP from X-Forwarded-For
	CredsTTL    time.Duration
	Web         fs.FS
	MaxMsgBytes int64
}

type Server struct {
	cfg Config
	log *slog.Logger

	mu       sync.Mutex
	hosts    map[string]*peer // by host ID
	ipLimits map[string]*rate.Limiter
	idLimits map[string]*rate.Limiter
}

// peer is one WebSocket connection (a host or a viewer).
type peer struct {
	conn *websocket.Conn
	send chan []byte
	once sync.Once
	done chan struct{}

	// host only
	id       string
	sessions map[string]*peer // sid -> viewer
	// viewer only
	sid  string
	host *peer
}

func newPeer(c *websocket.Conn) *peer {
	return &peer{conn: c, send: make(chan []byte, 256), done: make(chan struct{})}
}

func (p *peer) close() { p.once.Do(func() { close(p.done) }) }

// write queues a message; a peer that cannot keep up is dropped.
func (p *peer) write(m signal.Msg) {
	b, _ := json.Marshal(m)
	select {
	case p.send <- b:
	case <-p.done:
	default:
		p.close()
	}
}

func New(cfg Config, log *slog.Logger) *Server {
	if cfg.CredsTTL == 0 {
		cfg.CredsTTL = 12 * time.Hour
	}
	if cfg.MaxMsgBytes == 0 {
		cfg.MaxMsgBytes = 256 << 10
	}
	return &Server{
		cfg:      cfg,
		log:      log,
		hosts:    map[string]*peer{},
		ipLimits: map[string]*rate.Limiter{},
		idLimits: map[string]*rate.Limiter{},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/host", s.handleHost)
	mux.HandleFunc("/ws/viewer", s.handleViewer)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	files := http.FileServer(http.FS(s.cfg.Web))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		files.ServeHTTP(w, r)
	}))
	return mux
}

// ICEServers returns the ICE servers handed to a client, with short-lived
// TURN REST credentials.
func (s *Server) iceServers(user string) []signal.ICEServer {
	var out []signal.ICEServer
	if s.cfg.TURN {
		hp := net.JoinHostPort(s.cfg.TURNHost, itoa(s.cfg.TURNPort))
		out = append(out, signal.ICEServer{URLs: []string{"stun:" + hp}})
		u, p, err := turnCredentials(s.cfg.TURNSecret, user, s.cfg.CredsTTL)
		if err == nil {
			out = append(out, signal.ICEServer{
				URLs:       []string{"turn:" + hp + "?transport=udp", "turn:" + hp + "?transport=tcp"},
				Username:   u,
				Credential: p,
			})
		}
	}
	if len(s.cfg.ExtraSTUN) > 0 {
		out = append(out, signal.ICEServer{URLs: s.cfg.ExtraSTUN})
	}
	return out
}

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if cf := r.Header.Get("CF-Connecting-IP"); cf != "" {
			return strings.TrimSpace(cf)
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) allow(m map[string]*rate.Limiter, key string, every time.Duration, burst int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := m[key]
	if !ok {
		if len(m) > 50_000 { // crude memory bound
			clear(m)
		}
		l = rate.NewLimiter(rate.Every(every), burst)
		m[key] = l
	}
	return l.Allow()
}

func (s *Server) accept(w http.ResponseWriter, r *http.Request) (*peer, context.Context, bool) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return nil, nil, false
	}
	c.SetReadLimit(s.cfg.MaxMsgBytes)
	p := newPeer(c)
	ctx, cancel := context.WithCancel(r.Context())
	go func() {
		<-p.done
		cancel()
	}()
	go s.writer(ctx, p)
	return p, ctx, true
}

func (s *Server) writer(ctx context.Context, p *peer) {
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	defer p.conn.CloseNow()
	for {
		select {
		case <-ctx.Done():
			// flush what is queued (e.g. an error message) before closing
			for {
				select {
				case b := <-p.send:
					wctx, c := context.WithTimeout(context.Background(), time.Second)
					p.conn.Write(wctx, websocket.MessageText, b)
					c()
				default:
					p.conn.Close(websocket.StatusNormalClosure, "")
					return
				}
			}
		case b := <-p.send:
			wctx, c := context.WithTimeout(ctx, 10*time.Second)
			err := p.conn.Write(wctx, websocket.MessageText, b)
			c()
			if err != nil {
				p.close()
				return
			}
		case <-ping.C:
			pctx, c := context.WithTimeout(ctx, 10*time.Second)
			err := p.conn.Ping(pctx)
			c()
			if err != nil {
				p.close()
				return
			}
		}
	}
}

func read(ctx context.Context, p *peer) (signal.Msg, error) {
	var m signal.Msg
	_, b, err := p.conn.Read(ctx)
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(b, &m)
	return m, err
}

func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if !s.allow(s.ipLimits, "h|"+ip, 2*time.Second, 10) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	p, ctx, ok := s.accept(w, r)
	if !ok {
		return
	}
	defer p.close()

	rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	m, err := read(rctx, p)
	cancel()
	if err != nil || m.T != signal.TRegister || len(m.Token) < 32 {
		p.write(signal.Msg{T: signal.TError, Error: "expected register"})
		return
	}
	p.id = signal.IDFromToken(m.Token)
	p.sessions = map[string]*peer{}

	s.mu.Lock()
	if old := s.hosts[p.id]; old != nil {
		old.write(signal.Msg{T: signal.TError, Error: "replaced by a newer connection"})
		old.close()
	}
	s.hosts[p.id] = p
	s.mu.Unlock()
	s.log.Info("host online", "id", p.id, "ip", ip)

	defer func() {
		s.mu.Lock()
		if s.hosts[p.id] == p {
			delete(s.hosts, p.id)
		}
		viewers := p.sessions
		p.sessions = map[string]*peer{}
		s.mu.Unlock()
		for _, v := range viewers {
			v.write(signal.Msg{T: signal.TError, Error: "host went offline"})
			v.close()
		}
		s.log.Info("host offline", "id", p.id)
	}()

	p.write(signal.Msg{T: signal.TRegistered, ID: p.id, ICE: s.iceServers("h" + p.id)})

	for {
		m, err := read(ctx, p)
		if err != nil {
			return
		}
		switch m.T {
		case signal.TSignal:
			s.mu.Lock()
			v := p.sessions[m.Sid]
			s.mu.Unlock()
			if v != nil {
				v.write(signal.Msg{T: signal.TSignal, Data: m.Data})
			}
		case signal.TBye:
			s.mu.Lock()
			v := p.sessions[m.Sid]
			delete(p.sessions, m.Sid)
			s.mu.Unlock()
			if v != nil {
				v.write(signal.Msg{T: signal.TBye})
				v.close()
			}
		}
	}
}

func (s *Server) handleViewer(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	p, ctx, ok := s.accept(w, r)
	if !ok {
		return
	}
	defer p.close()

	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	m, err := read(rctx, p)
	cancel()
	if err != nil || m.T != signal.TConnect {
		p.write(signal.Msg{T: signal.TError, Error: "expected connect"})
		return
	}
	id := strings.ReplaceAll(m.ID, " ", "")
	// Throttle connection attempts per viewer IP and per target host, which
	// bounds online password guessing.
	if !s.allow(s.ipLimits, "v|"+ip, 3*time.Second, 10) || !s.allow(s.idLimits, id, 4*time.Second, 6) {
		p.write(signal.Msg{T: signal.TError, Error: "too many attempts, wait a minute"})
		return
	}

	sid := randHex(12)
	s.mu.Lock()
	h := s.hosts[id]
	if h != nil {
		if len(h.sessions) >= 16 {
			h = nil
		} else {
			p.sid, p.host = sid, h
			h.sessions[sid] = p
		}
	}
	s.mu.Unlock()
	if h == nil {
		p.write(signal.Msg{T: signal.TError, Error: "host is offline or busy"})
		return
	}
	s.log.Info("viewer connecting", "id", id, "sid", sid, "ip", ip)

	defer func() {
		s.mu.Lock()
		_, still := h.sessions[sid]
		delete(h.sessions, sid)
		s.mu.Unlock()
		if still {
			h.write(signal.Msg{T: signal.TBye, Sid: sid})
		}
	}()

	h.write(signal.Msg{T: signal.TSession, Sid: sid, ICE: s.iceServers("h" + id)})
	p.write(signal.Msg{T: signal.TReady, ICE: s.iceServers("v" + sid)})

	for {
		m, err := read(ctx, p)
		if err != nil {
			return
		}
		if m.T == signal.TSignal {
			h.write(signal.Msg{T: signal.TSignal, Sid: sid, Data: m.Data})
		}
	}
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}
