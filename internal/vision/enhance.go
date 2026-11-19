package vision

import (
	"log"
	"math"
)

const (
	enhanceGammaMin   = 0.35
	enhanceGammaMax   = 3.0
	enhanceTargetMean = 0.5
	enhanceMinSpread  = 24
)

// gray is cv2.COLOR_BGR2GRAY for 8-bit: fixed point with 14 fractional bits.
func gray(p []uint8, i int) uint8 {
	return uint8((int(p[i])*1868 + int(p[i+1])*9617 + int(p[i+2])*4899 + 8192) >> 14)
}

// Enhance stretches p1..p99 of the luma to full range, then applies a gamma
// that pulls mean luminance toward mid-grey, as one LUT on every channel.
func Enhance(m Image) Image {
	var hist [256]int
	n := m.W * m.H
	for i := 0; i < len(m.Pix); i += 3 {
		hist[gray(m.Pix, i)]++
	}
	lo, hi := percentile(&hist, n, 1), percentile(&hist, n, 99)

	var ramp [256]float64
	for v := range ramp {
		ramp[v] = float64(v)
	}
	if hi-lo >= enhanceMinSpread {
		for v := range ramp {
			ramp[v] = clamp((float64(v)-lo)*(255/(hi-lo)), 0, 255)
		}
	}
	var dot float64
	for v := range ramp {
		dot += ramp[v] * float64(hist[v])
	}
	mean := clamp(dot/float64(n)/255, 0.02, 0.98)
	gamma := clamp(math.Log(enhanceTargetMean)/math.Log(mean), enhanceGammaMin, enhanceGammaMax)

	var lut [256]uint8
	for v := range lut {
		lut[v] = uint8(math.RoundToEven(clamp(255*math.Pow(ramp[v]/255, gamma), 0, 255)))
	}
	out := New(m.W, m.H)
	for i, p := range m.Pix {
		out.Pix[i] = lut[p]
	}
	if debugEnhance {
		log.Printf("[enhance] p1=%.0f p99=%.0f mean=%.2f gamma=%.2f", lo, hi, mean, gamma)
	}
	return out
}

var debugEnhance = false

// percentile is numpy's default ("linear") method, read off a histogram.
func percentile(hist *[256]int, n int, p float64) float64 {
	pos := p / 100 * float64(n-1)
	k := int(math.Floor(pos))
	a, b := float64(valueAtRank(hist, k)), float64(valueAtRank(hist, min(k+1, n-1)))
	return a + (pos-float64(k))*(b-a)
}

func valueAtRank(hist *[256]int, k int) int {
	seen := 0
	for v, c := range hist {
		seen += c
		if k < seen {
			return v
		}
	}
	return 255
}

func clamp(v, lo, hi float64) float64 { return math.Min(math.Max(v, lo), hi) }
