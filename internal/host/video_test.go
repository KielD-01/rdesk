package host

import (
	"image"
	"testing"

	"github.com/pion/mediadevices/pkg/codec"
	"github.com/pion/mediadevices/pkg/codec/openh264"
	"github.com/pion/mediadevices/pkg/io/video"
	"github.com/pion/mediadevices/pkg/prop"

	"rdesk/internal/capture"
)

func testFrame(w, h, shift int) *capture.Frame {
	f := &capture.Frame{Pix: make([]byte, w*h*4), Stride: w * 4, W: w, H: h, BGRA: true}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			o := y*f.Stride + x*4
			f.Pix[o] = byte(x + shift)
			f.Pix[o+1] = byte(y ^ x)
			f.Pix[o+2] = byte((x * y) >> 4)
		}
	}
	return f
}

func TestToI420Gray(t *testing.T) {
	f := &capture.Frame{Pix: make([]byte, 4*4*4), Stride: 16, W: 4, H: 4, BGRA: true}
	for i := range f.Pix {
		f.Pix[i] = 128
	}
	img := toI420(f, 1, nil)
	if img.Y[0] != 126 || img.Cb[0] != 128 || img.Cr[0] != 128 {
		t.Fatalf("gray converted to Y=%d Cb=%d Cr=%d", img.Y[0], img.Cb[0], img.Cr[0])
	}
}

func benchPipeline(b *testing.B, w, h int) {
	frames := []*capture.Frame{testFrame(w, h, 0), testFrame(w, h, 7)}
	var cur *image.YCbCr
	p, _ := openh264.NewParams()
	p.UsageType = openh264.ScreenContentRealTime
	p.BitRate = 6_000_000
	p.EnableFrameSkip = true
	enc, err := p.BuildVideoEncoder(video.ReaderFunc(func() (image.Image, func(), error) {
		return cur, func() {}, nil
	}), prop.Media{Video: prop.Video{Width: w, Height: h, FrameRate: 30}})
	if err != nil {
		b.Fatal(err)
	}
	defer enc.Close()
	enc.Controller().(codec.KeyFrameController).ForceKeyFrame()
	var yuv *image.YCbCr
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		yuv = toI420(frames[i%2], 1, yuv)
		cur = yuv
		if _, _, err := enc.Read(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkConvert1080p(b *testing.B) {
	f := testFrame(1920, 1080, 0)
	var yuv *image.YCbCr
	for i := 0; i < b.N; i++ {
		yuv = toI420(f, 1, yuv)
	}
}

func BenchmarkPipeline1080p(b *testing.B) { benchPipeline(b, 1920, 1080) }
func BenchmarkPipeline1440p(b *testing.B) { benchPipeline(b, 2560, 1440) }
