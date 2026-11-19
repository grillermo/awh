package capture

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/grillermo/awh/internal/vision"
)

// testCamera generates a local 640x480 video with ffmpeg's lavfi testsrc; the
// real camera is never contacted from tests.
func testCamera(t *testing.T) Camera {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.mp4")
	out, err := exec.Command("ffmpeg", "-nostdin", "-loglevel", "error", "-f", "lavfi",
		"-i", "testsrc=size=640x480:rate=5:duration=1", "-pix_fmt", "yuv420p", path).CombinedOutput()
	if err != nil {
		t.Skipf("cannot generate test video: %v: %s", err, out)
	}
	return Camera{URL: path, Timeout: 20 * time.Second}
}

func TestSizeCropAndJPEG(t *testing.T) {
	c := testCamera(t)
	w, h, err := c.Size(t.Context())
	if err != nil || w != 640 || h != 480 {
		t.Fatalf("size %dx%d %v", w, h, err)
	}
	r, err := vision.CropRect(w, h, [4]float64{0.6035, 0.5494, 0.6426, 0.6233})
	if err != nil {
		t.Fatal(err)
	}
	img, err := c.Crop(t.Context(), r)
	if err != nil || img.W != r.W || img.H != r.H {
		t.Fatalf("crop %dx%d %v", img.W, img.H, err)
	}
	jpg, err := c.FullJPEG(t.Context())
	if err != nil || !bytes.HasPrefix(jpg, []byte{0xFF, 0xD8}) {
		t.Fatalf("jpeg %d bytes %v", len(jpg), err)
	}
	t.Logf("frame %dx%d, crop %+v", w, h, r)
}

func TestCropMatchesFullFrameCrop(t *testing.T) {
	c := testCamera(t)
	r := vision.Rect{X: 100, Y: 50, W: 40, H: 30}
	got, err := c.Crop(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	full, err := c.Crop(t.Context(), vision.Rect{X: 0, Y: 0, W: 640, H: 480})
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < r.H; y++ {
		want := full.Pix[((r.Y+y)*640+r.X)*3 : ((r.Y+y)*640+r.X+r.W)*3]
		if !bytes.Equal(got.Pix[y*r.W*3:(y+1)*r.W*3], want) {
			t.Fatalf("row %d differs from crop of the full frame", y)
		}
	}
}

func TestBadInput(t *testing.T) {
	c := Camera{URL: "/nonexistent.mp4", Timeout: 5 * time.Second}
	if _, _, err := c.Size(t.Context()); err == nil {
		t.Fatal("want error")
	}
}
