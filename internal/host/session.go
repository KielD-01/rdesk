package host

import (
	"encoding/json"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/cc"
	"github.com/pion/interceptor/pkg/gcc"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"

	"rdesk/internal/auth"
	"rdesk/internal/signal"
)

const (
	h264Fmtp = "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"
	maxClip  = 1 << 20
)

// ctrl is a message on the "control" data channel.
type ctrl struct {
	T        string      `json:"t"`
	Nonce    string      `json:"nonce,omitempty"`
	Proof    string      `json:"proof,omitempty"`
	Text     string      `json:"text,omitempty"`
	Name     string      `json:"name,omitempty"`
	OS       string      `json:"os,omitempty"`
	Path     string      `json:"path,omitempty"`
	Parent   string      `json:"parent,omitempty"`
	Error    string      `json:"error,omitempty"`
	W        int         `json:"w,omitempty"`
	H        int         `json:"h,omitempty"`
	Features []string    `json:"features,omitempty"`
	Entries  []fileEntry `json:"entries,omitempty"`
}

// inMsg is a message on the "input" data channel.
type inMsg struct {
	T  string  `json:"t"`
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
	B  int     `json:"b"`
	D  bool    `json:"d"`
	C  string  `json:"c"`
	DX int     `json:"dx"`
	DY int     `json:"dy"`
}

type session struct {
	h   *Host
	sid string
	pc  *webrtc.PeerConnection

	videoTrack *webrtc.TrackLocalStaticSample
	audioTrack *webrtc.TrackLocalStaticSample
	control    *webrtc.DataChannel

	authed atomic.Bool
	nonce  string
	since  time.Time
	bwe    chan cc.BandwidthEstimator

	mu        sync.Mutex
	pending   []webrtc.ICECandidateInit
	remoteSet bool
	pressed   map[string]bool
	buttons   map[int]bool
	path      string
	closed    bool
	done      chan struct{}
}

func (h *Host) newAPI(bwe chan cc.BandwidthEstimator) (*webrtc.API, error) {
	m := &webrtc.MediaEngine{}
	fb := []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}, {Type: "ccm", Parameter: "fir"}, {Type: "transport-cc"}}
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: h264Fmtp, RTCPFeedback: fb},
		PayloadType:        102,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}
	if err := m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1;stereo=1"},
		PayloadType:        111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}
	reg := &interceptor.Registry{}
	ccf, err := cc.NewInterceptor(func() (cc.BandwidthEstimator, error) {
		return gcc.NewSendSideBWE(
			gcc.SendSideBWEInitialBitrate(min(1_500_000, h.opt.Video.Bitrate)),
			gcc.SendSideBWEMinBitrate(150_000),
			gcc.SendSideBWEMaxBitrate(h.opt.Video.Bitrate),
			gcc.SendSideBWEPacer(gcc.NewNoOpPacer()),
		)
	})
	if err != nil {
		return nil, err
	}
	ccf.OnNewPeerConnection(func(_ string, e cc.BandwidthEstimator) {
		select {
		case bwe <- e:
		default:
		}
	})
	reg.Add(ccf)
	if err := webrtc.ConfigureTWCCHeaderExtensionSender(m, reg); err != nil {
		return nil, err
	}
	if err := webrtc.RegisterDefaultInterceptors(m, reg); err != nil {
		return nil, err
	}
	se := webrtc.SettingEngine{}
	se.SetICETimeouts(8*time.Second, 25*time.Second, 2*time.Second)
	// Container bridges and veth pairs only add useless candidates.
	se.SetInterfaceFilter(func(name string) bool {
		for _, p := range []string{"docker", "br-", "veth", "virbr", "vmnet", "vboxnet", "cni", "flannel"} {
			if strings.HasPrefix(name, p) {
				return false
			}
		}
		return true
	})
	return webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(reg), webrtc.WithSettingEngine(se)), nil
}

func toICE(list []signal.ICEServer) []webrtc.ICEServer {
	out := make([]webrtc.ICEServer, 0, len(list))
	for _, s := range list {
		ice := webrtc.ICEServer{URLs: s.URLs}
		if s.Username != "" {
			ice.Username, ice.Credential = s.Username, s.Credential
		}
		out = append(out, ice)
	}
	return out
}

func (h *Host) startSession(sid string, ice []signal.ICEServer) {
	s := &session{h: h, sid: sid, pressed: map[string]bool{}, buttons: map[int]bool{},
		done: make(chan struct{}), bwe: make(chan cc.BandwidthEstimator, 1), since: time.Now()}
	h.mu.Lock()
	h.sessions[sid] = s
	h.mu.Unlock()
	if err := s.start(ice); err != nil {
		h.log.Error("session setup", "sid", sid, "err", err)
		s.close("setup failed")
	}
}

func (s *session) start(ice []signal.ICEServer) error {
	h := s.h
	api, err := h.newAPI(s.bwe)
	if err != nil {
		return err
	}
	pc, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: toICE(ice)})
	if err != nil {
		return err
	}
	s.pc = pc

	s.videoTrack, err = webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: h264Fmtp}, "video", "rdesk")
	if err != nil {
		return err
	}
	vt, err := pc.AddTransceiverFromTrack(s.videoTrack, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
	if err != nil {
		return err
	}
	go s.readRTCP(vt.Sender())

	if h.audio != nil {
		s.audioTrack, err = webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio", "rdesk")
		if err != nil {
			return err
		}
		at, err := pc.AddTransceiverFromTrack(s.audioTrack, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
		if err != nil {
			return err
		}
		go func() { // drain RTCP so interceptors keep working
			b := make([]byte, 1500)
			for {
				if _, _, err := at.Sender().Read(b); err != nil {
					return
				}
			}
		}()
	}

	ordered := true
	s.control, err = pc.CreateDataChannel("control", &webrtc.DataChannelInit{Ordered: &ordered})
	if err != nil {
		return err
	}
	inputDC, err := pc.CreateDataChannel("input", &webrtc.DataChannelInit{Ordered: &ordered})
	if err != nil {
		return err
	}
	s.control.OnOpen(s.onControlOpen)
	s.control.OnMessage(s.onControl)
	inputDC.OnMessage(s.onInput)
	pc.OnDataChannel(s.onDataChannel)

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		j := c.ToJSON()
		s.signal(signal.Signal{Candidate: &signal.Candidate{Candidate: j.Candidate, SDPMid: j.SDPMid, SDPMLineIndex: j.SDPMLineIndex}})
	})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		switch st {
		case webrtc.PeerConnectionStateConnected:
			path := s.pathType()
			s.mu.Lock()
			s.path = path
			s.mu.Unlock()
			h.log.Info("viewer connected", "sid", s.sid, "path", path)
			h.sessionsChanged()
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			s.close("connection " + st.String())
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		return err
	}
	s.signal(signal.Signal{SDP: &signal.SDP{Type: "offer", SDP: offer.SDP}})

	// A viewer that does not authenticate in time is dropped.
	go func() {
		select {
		case <-time.After(45 * time.Second):
			if !s.authed.Load() {
				s.close("authentication timeout")
			}
		case <-s.done:
		}
	}()
	return nil
}

func (s *session) signal(sig signal.Signal) {
	b, _ := json.Marshal(sig)
	s.h.send(signal.Msg{T: signal.TSignal, Sid: s.sid, Data: b})
}

func (s *session) onSignal(raw json.RawMessage) {
	var sig signal.Signal
	if json.Unmarshal(raw, &sig) != nil || s.pc == nil {
		return
	}
	if sig.SDP != nil && sig.SDP.Type == "answer" {
		if err := s.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sig.SDP.SDP}); err != nil {
			s.h.log.Warn("bad answer", "sid", s.sid, "err", err)
			s.close("bad answer")
			return
		}
		s.mu.Lock()
		s.remoteSet = true
		pending := s.pending
		s.pending = nil
		s.mu.Unlock()
		for _, c := range pending {
			s.pc.AddICECandidate(c)
		}
	}
	if c := sig.Candidate; c != nil && c.Candidate != "" {
		init := webrtc.ICECandidateInit{Candidate: c.Candidate, SDPMid: c.SDPMid, SDPMLineIndex: c.SDPMLineIndex}
		s.mu.Lock()
		if !s.remoteSet {
			s.pending = append(s.pending, init)
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		s.pc.AddICECandidate(init)
	}
}

// signalingGone: the viewer's signaling socket closed. Established sessions
// keep running over WebRTC; half-open ones are dropped.
func (s *session) signalingGone() {
	if s.pc == nil || s.pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
		s.close("viewer left")
	}
}

func (s *session) pathType() string {
	sctp := s.pc.SCTP()
	if sctp == nil || sctp.Transport() == nil {
		return "?"
	}
	pair, err := sctp.Transport().ICETransport().GetSelectedCandidatePair()
	if err != nil || pair == nil {
		return "?"
	}
	if pair.Local.Typ == webrtc.ICECandidateTypeRelay || pair.Remote.Typ == webrtc.ICECandidateTypeRelay {
		return "relay (TURN)"
	}
	return "direct (" + pair.Local.Typ.String() + "/" + pair.Remote.Typ.String() + ")"
}

func (s *session) readRTCP(sender *webrtc.RTPSender) {
	for {
		pkts, _, err := sender.ReadRTCP()
		if err != nil {
			return
		}
		if !s.authed.Load() {
			continue
		}
		for _, p := range pkts {
			switch p.(type) {
			case *rtcp.PictureLossIndication, *rtcp.FullIntraRequest:
				s.h.video.requestKeyframe()
			}
		}
	}
}

func (s *session) sendControl(m ctrl) {
	if s.control == nil {
		return
	}
	b, _ := json.Marshal(m)
	s.control.SendText(string(b))
}

func (s *session) onControlOpen() {
	s.nonce = auth.Nonce()
	s.sendControl(ctrl{T: "hello", Nonce: s.nonce})
}

func (s *session) fingerprints() (offer, answer string) {
	if l := s.pc.LocalDescription(); l != nil {
		offer = auth.Fingerprint(l.SDP)
	}
	if r := s.pc.RemoteDescription(); r != nil {
		answer = auth.Fingerprint(r.SDP)
	}
	return
}

func (s *session) onControl(msg webrtc.DataChannelMessage) {
	if len(msg.Data) > maxClip+4096 {
		return
	}
	var m ctrl
	if json.Unmarshal(msg.Data, &m) != nil {
		return
	}
	h := s.h
	if !s.authed.Load() {
		if m.T != "auth" || s.nonce == "" {
			return
		}
		offerFP, answerFP := s.fingerprints()
		pw := h.currentPassword()
		want := auth.Proof(pw, "viewer", s.nonce, offerFP, answerFP)
		if offerFP == "" || answerFP == "" || !auth.Equal(want, m.Proof) {
			h.log.Warn("wrong password from viewer", "sid", s.sid)
			s.nonce = "" // one attempt per connection
			s.sendControl(ctrl{T: "auth-fail"})
			h.authFailed()
			go func() { time.Sleep(time.Second); s.close("authentication failed") }()
			return
		}
		s.authed.Store(true)
		h.authOK()
		name, _ := os.Hostname()
		_, size := h.video.geometry()
		features := []string{"clipboard", "files"}
		if h.opt.Input {
			features = append(features, "input", "terminal")
		}
		if h.audio != nil {
			features = append(features, "audio")
		}
		s.sendControl(ctrl{T: "auth-ok", Proof: auth.Proof(pw, "host", s.nonce, offerFP, answerFP),
			Name: name, OS: runtime.GOOS, W: size.X, H: size.Y, Features: features})
		h.log.Info("viewer authenticated", "sid", s.sid)
		h.sessionsChanged()
		h.video.add(s, s.videoTrack)
		if h.audio != nil {
			h.audio.add(s, s.audioTrack)
		}
		h.clip.start()
		go s.followBandwidth()
		return
	}

	switch m.T {
	case "clip":
		if len(m.Text) <= maxClip {
			h.clip.set(m.Text)
		}
	case "keyframe":
		h.video.requestKeyframe()
	case "media": // the viewer pauses video/audio, e.g. in terminal-only mode
		if m.Text == "off" {
			h.video.remove(s)
			if h.audio != nil {
				h.audio.remove(s)
			}
		} else {
			h.video.add(s, s.videoTrack)
			if h.audio != nil {
				h.audio.add(s, s.audioTrack)
			}
		}
	case "ls":
		s.listDir(m.Path)
	case "get":
		s.sendFile(m.Path)
	}
}

// followBandwidth feeds the congestion controller's estimate to the encoder.
func (s *session) followBandwidth() {
	var est cc.BandwidthEstimator
	select {
	case est = <-s.bwe:
	case <-s.done:
		return
	case <-time.After(5 * time.Second):
		return
	}
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			s.h.updateBitrate(s, est.GetTargetBitrate())
		}
	}
}

func (s *session) onInput(msg webrtc.DataChannelMessage) {
	if !s.authed.Load() || !s.h.opt.Input {
		return
	}
	var m inMsg
	if json.Unmarshal(msg.Data, &m) != nil {
		return
	}
	in := s.h.injector()
	if in == nil {
		return
	}
	switch m.T {
	case "mm":
		b, _ := s.h.video.geometry()
		if b.Empty() {
			return
		}
		x := b.Min.X + int(math.Round(clamp01(m.X)*float64(b.Dx()-1)))
		y := b.Min.Y + int(math.Round(clamp01(m.Y)*float64(b.Dy()-1)))
		in.Move(x, y)
	case "mb":
		s.mu.Lock()
		s.buttons[m.B] = m.D
		s.mu.Unlock()
		in.Button(m.B, m.D)
	case "wh":
		in.Scroll(clampInt(m.DX, -20, 20), clampInt(m.DY, -20, 20))
	case "k":
		s.mu.Lock()
		repeat := m.D && s.pressed[m.C]
		if m.D {
			s.pressed[m.C] = true
		} else {
			delete(s.pressed, m.C)
		}
		s.mu.Unlock()
		// X11 auto-repeats held XTEST keys itself; elsewhere the viewer's
		// key repeats are what make a held key repeat.
		if repeat && runtime.GOOS == "linux" {
			return
		}
		in.Key(m.C, m.D)
	case "release": // viewer lost focus
		s.releaseAll()
	}
}

// releaseAll lifts every key and button this viewer is holding down.
func (s *session) releaseAll() {
	s.mu.Lock()
	keys, buttons := s.pressed, s.buttons
	s.pressed, s.buttons = map[string]bool{}, map[int]bool{}
	s.mu.Unlock()
	in := s.h.injector()
	if in == nil {
		return
	}
	for k := range keys {
		in.Key(k, false)
	}
	for b, down := range buttons {
		if down {
			in.Button(b, false)
		}
	}
}

func (s *session) onDataChannel(dc *webrtc.DataChannel) {
	if !s.authed.Load() {
		dc.Close()
		return
	}
	switch dc.Label() {
	case "upload":
		s.receiveFile(dc)
		return
	case "term":
		if s.h.opt.Input { // view-only hosts get no shell either
			s.serveTerminal(dc)
			return
		}
	}
	dc.Close()
}

func (s *session) close(reason string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.done)
	s.mu.Unlock()

	h := s.h
	h.video.remove(s)
	if h.audio != nil {
		h.audio.remove(s)
	}
	if s.authed.Load() {
		s.releaseAll()
	}
	h.removeBitrate(s)
	if s.pc != nil {
		go s.pc.Close()
	}
	h.send(signal.Msg{T: signal.TBye, Sid: s.sid})
	h.sessionEnded(s)
	h.log.Info("session ended", "sid", s.sid, "reason", reason)
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func clampInt(v, lo, hi int) int { return max(lo, min(hi, v)) }
