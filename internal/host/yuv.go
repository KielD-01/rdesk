package host

import (
	"image"
	"runtime"
	"sync"

	"rdesk/internal/capture"
)

// toI420 converts a 32-bit frame to I420 (BT.601 limited range), sampling
// every f-th pixel when f > 1. dst is reallocated when the size changes.
// Output dimensions are rounded down to even numbers.
func toI420(src *capture.Frame, f int, dst *image.YCbCr) *image.YCbCr {
	w := (src.W / f) &^ 1
	h := (src.H / f) &^ 1
	if dst == nil || dst.Rect.Dx() != w || dst.Rect.Dy() != h {
		dst = image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)
	}
	ri, bi := 0, 2
	if src.BGRA {
		ri, bi = 2, 0
	}

	// Split rows (in pairs) across workers.
	workers := runtime.GOMAXPROCS(0)
	if workers > 8 {
		workers = 8
	}
	pairs := h / 2
	if workers > pairs {
		workers = max(pairs, 1)
	}
	per := (pairs + workers - 1) / workers
	var wg sync.WaitGroup
	for k := 0; k < workers; k++ {
		y0, y1 := k*per*2, min((k+1)*per*2, h)
		if y0 >= y1 {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			convertRows(src, f, dst, ri, bi, y0, y1, w)
		}()
	}
	wg.Wait()
	return dst
}

func convertRows(src *capture.Frame, f int, dst *image.YCbCr, ri, bi, y0, y1, w int) {
	pix, stride := src.Pix, src.Stride
	for y := y0; y < y1; y += 2 {
		r0 := pix[(y*f)*stride:]
		r1 := pix[((y+1)*f)*stride:]
		yo0 := y * dst.YStride
		yo1 := (y + 1) * dst.YStride
		co := (y / 2) * dst.CStride
		for x := 0; x < w; x += 2 {
			o0 := x * f * 4
			o1 := (x + 1) * f * 4
			// four pixels of the 2x2 block
			ra, ga, ba := int(r0[o0+ri]), int(r0[o0+1]), int(r0[o0+bi])
			rb, gb, bb := int(r0[o1+ri]), int(r0[o1+1]), int(r0[o1+bi])
			rc, gc, bc := int(r1[o0+ri]), int(r1[o0+1]), int(r1[o0+bi])
			rd, gd, bd := int(r1[o1+ri]), int(r1[o1+1]), int(r1[o1+bi])
			dst.Y[yo0+x] = uint8((66*ra+129*ga+25*ba+128)>>8 + 16)
			dst.Y[yo0+x+1] = uint8((66*rb+129*gb+25*bb+128)>>8 + 16)
			dst.Y[yo1+x] = uint8((66*rc+129*gc+25*bc+128)>>8 + 16)
			dst.Y[yo1+x+1] = uint8((66*rd+129*gd+25*bd+128)>>8 + 16)
			r := (ra + rb + rc + rd + 2) >> 2
			g := (ga + gb + gc + gd + 2) >> 2
			b := (ba + bb + bc + bd + 2) >> 2
			dst.Cb[co+x/2] = uint8((-38*r-74*g+112*b+128)>>8 + 128)
			dst.Cr[co+x/2] = uint8((112*r-94*g-18*b+128)>>8 + 128)
		}
	}
}
