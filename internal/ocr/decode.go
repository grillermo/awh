package ocr

import (
	"math"
	"strings"
)

// Decode is CTCLabelDecode with remove_duplicate=True: argmax per step, drop
// repeats and blanks (index 0); the score is the mean of the kept steps' max
// probabilities, each rounded to 5 decimals, the mean rounded again.
func Decode(probs []float32, steps, classes int, keys []string) (string, float64) {
	var sb strings.Builder
	var sum float64
	kept, prev := 0, -1
	for t := 0; t < steps; t++ {
		row := probs[t*classes : (t+1)*classes]
		best := 0
		for c := 1; c < classes; c++ {
			if row[c] > row[best] { // strict: first max wins, like numpy argmax
				best = c
			}
		}
		if best != prev && best != 0 {
			sb.WriteString(keys[best])
			sum += round5(float64(row[best]))
			kept++
		}
		prev = best
	}
	if kept == 0 {
		return "", 0
	}
	return sb.String(), round5(sum / float64(kept))
}

func round5(x float64) float64 { return math.RoundToEven(x*1e5) / 1e5 }
