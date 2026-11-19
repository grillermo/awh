package vision

import "math"

// displayQuadRel: the display's corners inside the crop (TL, TR, BR, BL),
// copied from DISPLAY_QUAD_REL in auto_reset.py.
var displayQuadRel = [4][2]float64{
	{0.2759, 0.2895}, {0.7931, 0.1316}, {0.7586, 0.6579}, {0.2069, 0.8421},
}

const fixedSize = 200

// FixPerspective warps the display quad to a 200x200 square (bilinear,
// black outside the source), like cv2.warpPerspective in auto_reset.py.
func FixPerspective(m Image) Image {
	last := float64(fixedSize - 1)
	dst := [4][2]float64{{0, 0}, {last, 0}, {last, last}, {0, last}}
	var src [4][2]float64
	for i, q := range displayQuadRel {
		src[i] = [2]float64{q[0] * float64(m.W), q[1] * float64(m.H)}
	}
	h := homography(dst, src) // output pixel -> source pixel
	out := New(fixedSize, fixedSize)
	for y := 0; y < fixedSize; y++ {
		for x := 0; x < fixedSize; x++ {
			fx, fy := float64(x), float64(y)
			d := h[6]*fx + h[7]*fy + 1
			sx := (h[0]*fx + h[1]*fy + h[2]) / d
			sy := (h[3]*fx + h[4]*fy + h[5]) / d
			sample(m, sx, sy, out.Pix[(y*fixedSize+x)*3:])
		}
	}
	return out
}

// homography solves the 8 unknowns mapping from[i] -> to[i] (h33 = 1).
func homography(from, to [4][2]float64) [8]float64 {
	var a [8][9]float64
	for i := 0; i < 4; i++ {
		x, y, u, v := from[i][0], from[i][1], to[i][0], to[i][1]
		a[2*i] = [9]float64{x, y, 1, 0, 0, 0, -u * x, -u * y, u}
		a[2*i+1] = [9]float64{0, 0, 0, x, y, 1, -v * x, -v * y, v}
	}
	for c := 0; c < 8; c++ { // Gauss-Jordan, partial pivoting
		p := c
		for r := c + 1; r < 8; r++ {
			if math.Abs(a[r][c]) > math.Abs(a[p][c]) {
				p = r
			}
		}
		a[c], a[p] = a[p], a[c]
		for r := 0; r < 8; r++ {
			if r != c {
				f := a[r][c] / a[c][c]
				for k := c; k < 9; k++ {
					a[r][k] -= f * a[c][k]
				}
			}
		}
	}
	var h [8]float64
	for i := range h {
		h[i] = a[i][8] / a[i][i]
	}
	return h
}

func sample(m Image, x, y float64, out []uint8) {
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	fx, fy := x-float64(x0), y-float64(y0)
	for c := 0; c < 3; c++ {
		v := (1-fx)*(1-fy)*px(m, x0, y0, c) + fx*(1-fy)*px(m, x0+1, y0, c) +
			(1-fx)*fy*px(m, x0, y0+1, c) + fx*fy*px(m, x0+1, y0+1, c)
		out[c] = uint8(math.Round(clamp(v, 0, 255)))
	}
}

func px(m Image, x, y, c int) float64 {
	if x < 0 || y < 0 || x >= m.W || y >= m.H {
		return 0
	}
	return float64(m.Pix[(y*m.W+x)*3+c])
}
