package vision

import "math"

// OpenCV's INTER_LINEAR for 8-bit images: half-pixel centres, 11-bit fixed
// point weights (INTER_RESIZE_COEF_BITS), edge clamping. RapidOCR resizes the
// display crop with this before recognition, so it decides what the model sees.
const (
	coefBits  = 11
	coefScale = 1 << coefBits
)

type tap struct {
	i0, i1 int
	a0, a1 int64
}

func linearTaps(dst, src int) []tap {
	scale := float64(src) / float64(dst)
	taps := make([]tap, dst)
	for d := range taps {
		f := float32((float64(d)+0.5)*scale - 0.5)
		s := int(math.Floor(float64(f)))
		f -= float32(s)
		if s < 0 {
			s, f = 0, 0
		}
		if s >= src-1 {
			s, f = src-1, 0
		}
		a0 := int64(math.RoundToEven(float64((1 - f) * coefScale))) // cvRound
		taps[d] = tap{i0: s, i1: min(s+1, src-1), a0: a0, a1: coefScale - a0}
	}
	return taps
}

func ResizeLinear(m Image, w, h int) Image {
	xs, ys := linearTaps(w, m.W), linearTaps(h, m.H)
	out := New(w, h)
	r0, r1 := make([]int64, w*3), make([]int64, w*3)
	row := func(y int, buf []int64) {
		base := y * m.W * 3
		for x, t := range xs {
			for c := 0; c < 3; c++ {
				buf[x*3+c] = int64(m.Pix[base+t.i0*3+c])*t.a0 + int64(m.Pix[base+t.i1*3+c])*t.a1
			}
		}
	}
	for y, t := range ys {
		row(t.i0, r0)
		row(t.i1, r1)
		for i := range r0 {
			v := (r0[i]*t.a0 + r1[i]*t.a1 + 1<<(2*coefBits-1)) >> (2 * coefBits)
			out.Pix[y*w*3+i] = uint8(min(max(v, 0), 255))
		}
	}
	return out
}
