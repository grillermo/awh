package vision

import (
	"strings"
	"testing"
)

func TestCropRectMatchesPythonIntTruncation(t *testing.T) {
	// Python: l, t, r, b = int(w*0.6035), int(h*0.5494), int(w*0.6426), int(h*0.6233)
	r, err := CropRect(2560, 1440, [4]float64{0.6035, 0.5494, 0.6426, 0.6233})
	if err != nil {
		t.Fatal(err)
	}
	want := Rect{X: 1544, Y: 791, W: 1645 - 1544, H: 897 - 791}
	if r != want {
		t.Fatalf("got %+v, want %+v", r, want)
	}
}

func TestCropRectEmptyIsAnError(t *testing.T) {
	_, err := CropRect(640, 480, [4]float64{0.5, 0.5, 0.5, 0.6})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("want empty-crop error, got %v", err)
	}
}

func TestCropCopiesPixels(t *testing.T) {
	m := New(4, 3)
	for i := range m.Pix {
		m.Pix[i] = uint8(i)
	}
	c := m.Crop(Rect{X: 1, Y: 1, W: 2, H: 2})
	// pixel (1,1) of the source starts at (1*4+1)*3 = 15
	if c.W != 2 || c.H != 2 || c.Pix[0] != 15 || c.Pix[3] != 18 || c.Pix[6] != 27 {
		t.Fatalf("unexpected crop %+v", c)
	}
}

func TestJPEGBase64RoundTrips(t *testing.T) {
	s, err := EncodeJPEGBase64(New(8, 8), 85)
	if err != nil || !strings.HasPrefix(s, "/9j/") { // base64 of FF D8 FF
		t.Fatalf("got %q, %v", s[:min(8, len(s))], err)
	}
}
