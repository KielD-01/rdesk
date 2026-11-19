package host

import (
	"bytes"
	"image"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/mediadevices/pkg/codec"
	"github.com/pion/mediadevices/pkg/codec/openh264"
	"github.com/pion/mediadevices/pkg/io/video"
	"github.com/pion/mediadevices/pkg/prop"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	"rdesk/internal/capture"
)

// VideoOptions tunes the video stream.
type VideoOptions struct {
	Display  int // display index, -1 = whole X screen (Linux)
	FPS      int
	Bitrate  int // bits per second (upper bound; congestion control may lower it)
	MaxWidth int // downscale wider screens by an integer factor
}

// videoPipe captures and encodes the screen once and fans the H.264 stream
// out to every authenticated session.
type videoPipe struct {
	opt VideoOptions
	log *slog.Logger

	mu      sync.Mutex
	sinks   map[*session]*webrtc.TrackLocalStaticSample
	running bool
	bounds  image.Rectangle // display bounds in input coordinates
	size    image.Point     // encoded size

	keyframe atomic.Bool
	bitrate  atomic.Int64 // current target, set by congestion control
	onSize   func(bounds image.Rectangle, size image.Point)
}

func newVideoPipe(opt VideoOptions, log *slog.Logger) *videoPipe {
	v := &videoPipe{opt: opt, log: log, sinks: map[*session]*webrtc.TrackLocalStaticSample{}}
	v.bitrate.Store(int64(opt.Bitrate))
	return v
}

func (v *videoPipe) add(s *session, t *webrtc.TrackLocalStaticSample) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.sinks[s] = t
	v.keyframe.Store(true)
	if !v.running {
		v.running = true
		go v.run()
	}
}

func (v *videoPipe) remove(s *session) {
	v.mu.Lock()
	delete(v.sinks, s)
	v.mu.Unlock()
}

// Geometry returns the captured display bounds and the encoded frame size.
func (v *videoPipe) geometry() (image.Rectangle, image.Point) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.bounds, v.size
}

func (v *videoPipe) requestKeyframe() { v.keyframe.Store(true) }

// setBitrate is called by congestion control with an estimate in bps.
func (v *videoPipe) setBitrate(bps int) {
	bps = min(bps, v.opt.Bitrate)
	bps = max(bps, 150_000)
	v.bitrate.Store(int64(bps))
}

func (v *videoPipe) run() {
	defer func() {
		v.mu.Lock()
		v.running = false
		restart := len(v.sinks) > 0
		v.mu.Unlock()
		if restart { // a session joined while we were shutting down, or an error
			time.Sleep(time.Second)
			v.mu.Lock()
			if !v.running && len(v.sinks) > 0 {
				v.running = true
				go v.run()
			}
			v.mu.Unlock()
		}
	}()

	cap, err := capture.New(v.opt.Display)
	if err != nil {
		v.log.Error("screen capture", "err", err)
		return
	}
	defer cap.Close()
	bounds := cap.Bounds()

	var (
		enc       codec.ReadCloser
		encSize   image.Point
		encRate   int64
		cur, yuv  *image.YCbCr
		prev      []byte
		lastSent  time.Time
		lastWrite time.Time
	)
	defer func() {
		if enc != nil {
			enc.Close()
		}
	}()

	frameDur := time.Second / time.Duration(max(v.opt.FPS, 1))
	tick := time.NewTicker(frameDur)
	defer tick.Stop()

	for range tick.C {
		v.mu.Lock()
		n := len(v.sinks)
		v.mu.Unlock()
		if n == 0 {
			return
		}

		fr, err := cap.Capture()
		if err != nil {
			v.log.Warn("capture failed", "err", err)
			return // restarted by the deferred check (e.g. after a resolution change)
		}
		// Skip unchanged frames; still send one per second so new
		// decoders and lossy links converge.
		raw := fr.Pix[:fr.Stride*fr.H]
		force := v.keyframe.Load()
		if !force && bytes.Equal(raw, prev) && time.Since(lastSent) < time.Second {
			continue
		}
		if len(prev) != len(raw) {
			prev = make([]byte, len(raw))
		}
		copy(prev, raw)

		f := 1
		if v.opt.MaxWidth > 0 && fr.W > v.opt.MaxWidth {
			f = (fr.W + v.opt.MaxWidth - 1) / v.opt.MaxWidth
		}
		yuv = toI420(fr, f, yuv)
		size := yuv.Rect.Size()

		if enc == nil || size != encSize {
			if enc != nil {
				enc.Close()
			}
			p, _ := openh264.NewParams()
			p.UsageType = openh264.ScreenContentRealTime
			p.BitRate = int(v.bitrate.Load())
			p.IntraPeriod = 0        // keyframes only on demand (start, PLI)
			p.EnableFrameSkip = true // lets rate control hold the bitrate
			enc, err = p.BuildVideoEncoder(video.ReaderFunc(func() (image.Image, func(), error) {
				return cur, func() {}, nil
			}), prop.Media{Video: prop.Video{Width: size.X, Height: size.Y, FrameRate: float32(v.opt.FPS)}})
			if err != nil {
				v.log.Error("h264 encoder", "err", err)
				return
			}
			encSize, encRate = size, int64(p.BitRate)
			force = true
			v.mu.Lock()
			v.bounds, v.size = bounds, size
			cb := v.onSize
			v.mu.Unlock()
			if cb != nil {
				cb(bounds, size)
			}
			v.log.Info("video", "capture", fmtSize(fr.W, fr.H), "encode", fmtSize(size.X, size.Y), "fps", v.opt.FPS)
		}
		if r := v.bitrate.Load(); r != encRate {
			if bc, ok := enc.Controller().(codec.BitRateController); ok {
				bc.SetBitRate(int(r))
			}
			encRate = r
		}
		if force {
			v.keyframe.Store(false)
			if kc, ok := enc.Controller().(codec.KeyFrameController); ok {
				kc.ForceKeyFrame()
			}
		}

		cur = yuv
		data, release, err := enc.Read()
		if err != nil {
			v.log.Error("encode", "err", err)
			return
		}
		release()
		if len(data) == 0 {
			continue
		}
		now := time.Now()
		dur := frameDur
		if !lastWrite.IsZero() {
			dur = now.Sub(lastWrite)
		}
		lastWrite, lastSent = now, now

		v.mu.Lock()
		for _, t := range v.sinks {
			t.WriteSample(media.Sample{Data: data, Duration: dur})
		}
		v.mu.Unlock()
	}
}

func fmtSize(w, h int) string { return itoa(w) + "x" + itoa(h) }
