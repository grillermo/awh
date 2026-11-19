// Package capture grabs single frames from the camera's RTSP stream with
// ffmpeg subprocesses. Each call is a short-lived child process, so decoding a
// full frame never grows the server's own heap; the live loop asks ffmpeg for
// the display crop only.
package capture

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/grillermo/awh/internal/vision"
)

// Camera is an RTSP URL (or, in tests, a local video file path).
type Camera struct {
	URL     string
	Timeout time.Duration
}

func (c Camera) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// input returns the input options and the input itself. The TCP transport
// option only exists for RTSP; ffmpeg rejects it for plain files.
func (c Camera) input() []string {
	if strings.HasPrefix(strings.ToLower(c.URL), "rtsp://") {
		return []string{"-rtsp_transport", "tcp", "-i", c.URL}
	}
	return []string{"-i", c.URL}
}

// Size probes the stream's frame size.
func (c Camera) Size(ctx context.Context) (int, int, error) {
	args := []string{"-v", "error"}
	args = append(args, c.input()[:len(c.input())-2]...)
	args = append(args, "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=p=0", c.URL)
	out, err := c.run(ctx, "ffprobe", args...)
	if err != nil {
		return 0, 0, err
	}
	var w, h int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d,%d", &w, &h); err != nil || w == 0 || h == 0 {
		return 0, 0, fmt.Errorf("ffprobe: unexpected size %q", out)
	}
	return w, h, nil
}

// Crop returns one frame's rect as BGR. It converts to bgr24 before cropping,
// so the pixels equal a crop of the fully converted frame.
func (c Camera) Crop(ctx context.Context, r vision.Rect) (vision.Image, error) {
	args := append([]string{"-nostdin", "-loglevel", "error"}, c.input()...)
	args = append(args, "-frames:v", "1",
		"-vf", fmt.Sprintf("format=bgr24,crop=%d:%d:%d:%d", r.W, r.H, r.X, r.Y),
		"-f", "rawvideo", "-pix_fmt", "bgr24", "-")
	out, err := c.run(ctx, "ffmpeg", args...)
	if err != nil {
		return vision.Image{}, err
	}
	if len(out) != r.W*r.H*3 {
		return vision.Image{}, fmt.Errorf("ffmpeg: got %d bytes, want %d", len(out), r.W*r.H*3)
	}
	return vision.Image{W: r.W, H: r.H, Pix: out}, nil
}

// FullJPEG returns one full frame as JPEG, encoded by ffmpeg (for the crop editor).
func (c Camera) FullJPEG(ctx context.Context) ([]byte, error) {
	args := append([]string{"-nostdin", "-loglevel", "error"}, c.input()...)
	args = append(args, "-frames:v", "1", "-f", "image2pipe", "-vcodec", "mjpeg", "-q:v", "3", "-")
	return c.run(ctx, "ffmpeg", args...)
}
