package host

import (
	"errors"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
	"github.com/pion/mediadevices/pkg/codec/opus"
	"github.com/pion/mediadevices/pkg/io/audio"
	"github.com/pion/mediadevices/pkg/prop"
	"github.com/pion/mediadevices/pkg/wave"
	"github.com/pion/mediadevices/pkg/wave/mixer"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const (
	audioRate     = 48000
	audioChannels = 2
	audioFrame    = 960 // 20 ms
)

// audioPipe captures system audio ("what you hear") once and fans the Opus
// stream out to every authenticated session.
type audioPipe struct {
	device string // name substring chosen by the user
	log    *slog.Logger

	mu      sync.Mutex
	sinks   map[*session]*webrtc.TrackLocalStaticSample
	running bool
	stop    chan struct{}
	failed  bool
}

func newAudioPipe(device string, log *slog.Logger) *audioPipe {
	return &audioPipe{device: device, log: log, sinks: map[*session]*webrtc.TrackLocalStaticSample{}}
}

func (a *audioPipe) add(s *session, t *webrtc.TrackLocalStaticSample) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sinks[s] = t
	if !a.running && !a.failed {
		a.running = true
		a.stop = make(chan struct{})
		go a.run(a.stop)
	}
}

func (a *audioPipe) remove(s *session) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sinks, s)
	if len(a.sinks) == 0 && a.running {
		close(a.stop)
		a.running = false
	}
}

// pickDevice chooses the capture device that carries the system mix.
func (a *audioPipe) pickDevice(ctx malgo.Context) (malgo.DeviceType, *malgo.DeviceID, string, error) {
	caps, _ := ctx.Devices(malgo.Capture)
	if a.device != "" {
		for i := range caps {
			if strings.Contains(strings.ToLower(caps[i].Name()), strings.ToLower(a.device)) {
				return malgo.Capture, &caps[i].ID, caps[i].Name(), nil
			}
		}
		return 0, nil, "", errors.New("audio device " + a.device + " not found")
	}
	switch runtime.GOOS {
	case "windows":
		return malgo.Loopback, nil, "default output (WASAPI loopback)", nil
	case "linux":
		// PulseAudio / PipeWire expose "Monitor of <sink>" capture sources.
		var def string
		if outs, err := ctx.Devices(malgo.Playback); err == nil {
			for i := range outs {
				if outs[i].IsDefault != 0 {
					def = outs[i].Name()
				}
			}
		}
		var first *malgo.DeviceInfo
		for i := range caps {
			n := caps[i].Name()
			if strings.HasPrefix(n, "Monitor of ") {
				if first == nil {
					first = &caps[i]
				}
				if def != "" && strings.TrimPrefix(n, "Monitor of ") == def {
					return malgo.Capture, &caps[i].ID, n, nil
				}
			}
		}
		if first != nil {
			return malgo.Capture, &first.ID, first.Name(), nil
		}
		return 0, nil, "", errors.New("no PulseAudio/PipeWire monitor source found")
	case "darwin":
		for i := range caps {
			n := strings.ToLower(caps[i].Name())
			if strings.Contains(n, "blackhole") || strings.Contains(n, "loopback") || strings.Contains(n, "soundflower") {
				return malgo.Capture, &caps[i].ID, caps[i].Name(), nil
			}
		}
		return 0, nil, "", errors.New("macOS has no system-audio capture device; install BlackHole and route output to it, or pass -audio-device")
	}
	return 0, nil, "", errors.New("unsupported OS")
}

func (a *audioPipe) run(stop chan struct{}) {
	fail := func(err error) {
		a.log.Warn("audio disabled", "err", err)
		a.mu.Lock()
		a.failed, a.running = true, false
		a.mu.Unlock()
	}
	mctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		fail(err)
		return
	}
	defer mctx.Free()
	defer mctx.Uninit()

	kind, id, name, err := a.pickDevice(mctx.Context)
	if err != nil {
		fail(err)
		return
	}

	pcm := make(chan []int16, 50)
	var buf []int16
	cfg := malgo.DefaultDeviceConfig(kind)
	cfg.SampleRate = audioRate
	cfg.Capture.Format = malgo.FormatS16
	cfg.Capture.Channels = audioChannels
	if id != nil {
		cfg.Capture.DeviceID = id.Pointer()
	}
	dev, err := malgo.InitDevice(mctx.Context, cfg, malgo.DeviceCallbacks{
		Data: func(_, in []byte, frames uint32) {
			n := int(frames) * audioChannels
			if len(in) < n*2 {
				return
			}
			buf = append(buf, unsafe.Slice((*int16)(unsafe.Pointer(&in[0])), n)...)
			for len(buf) >= audioFrame*audioChannels {
				chunk := make([]int16, audioFrame*audioChannels)
				copy(chunk, buf)
				buf = buf[audioFrame*audioChannels:]
				select {
				case pcm <- chunk:
				default: // encoder fell behind: drop
				}
			}
		},
	})
	if err != nil {
		fail(err)
		return
	}
	defer dev.Uninit()

	params, _ := opus.NewParams()
	params.BitRate = 96000
	params.ChannelMixer = &mixer.MonoMixer{} // unused: channel counts match
	reader := audio.ReaderFunc(func() (wave.Audio, func(), error) {
		select {
		case c := <-pcm:
			return &wave.Int16Interleaved{Data: c, Size: wave.ChunkInfo{Len: audioFrame, Channels: audioChannels, SamplingRate: audioRate}}, func() {}, nil
		case <-stop:
			return nil, func() {}, io.EOF
		}
	})
	enc, err := params.BuildAudioEncoder(reader, prop.Media{Audio: prop.Audio{SampleRate: audioRate, ChannelCount: audioChannels}})
	if err != nil {
		fail(err)
		return
	}
	defer enc.Close()

	if err := dev.Start(); err != nil {
		fail(err)
		return
	}
	a.log.Info("audio capture started", "device", name)
	defer a.log.Info("audio capture stopped")

	for {
		data, release, err := enc.Read()
		if err != nil {
			return
		}
		release()
		a.mu.Lock()
		for _, t := range a.sinks {
			t.WriteSample(media.Sample{Data: data, Duration: 20 * time.Millisecond})
		}
		a.mu.Unlock()
	}
}
