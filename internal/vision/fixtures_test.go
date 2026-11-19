package vision

import (
	"path/filepath"
	"testing"
)

const td = "../testdata"

func load(t *testing.T, name string) Image {
	t.Helper()
	m, err := LoadPNG(filepath.Join(td, name))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func diff(a, b Image) (maxAbs int, meanAbs float64) {
	sum := 0
	for i := range a.Pix {
		d := int(a.Pix[i]) - int(b.Pix[i])
		if d < 0 {
			d = -d
		}
		sum += d
		maxAbs = max(maxAbs, d)
	}
	return maxAbs, float64(sum) / float64(len(a.Pix))
}

func TestResizeMatchesCv2(t *testing.T) {
	for _, n := range []string{"digit45", "noise"} {
		src, want := load(t, n+".png"), load(t, n+"_resize.png")
		got := ResizeLinear(src, want.W, want.H)
		mx, mean := diff(got, want)
		t.Logf("%s resize: max |diff| %d, mean %.4f", n, mx, mean)
		if mx != 0 {
			t.Errorf("%s: max |diff| %d grey levels, want exactly 0", n, mx)
		}
	}
}

// Display-only output; it never reaches OCR, so the bar is looser.
func TestFixedFrameCloseToPython(t *testing.T) {
	for _, n := range []string{"digit45", "noise"} {
		src, want := load(t, n+".png"), load(t, n+"_fixed.png")
		got := Enhance(FixPerspective(src))
		mx, mean := diff(got, want)
		t.Logf("%s fixed: max |diff| %d, mean %.4f", n, mx, mean)
		if mean > 2 {
			t.Errorf("%s: mean |diff| %.2f, want <= 2", n, mean)
		}
	}
}
