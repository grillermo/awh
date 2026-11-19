// Package vision holds the few image operations the server needs, on 8-bit
// BGR images laid out like OpenCV's (so ports from auto_reset.py read the same).
package vision

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
)

// Image is 8-bit BGR, row-major, 3 bytes per pixel, no padding.
type Image struct {
	W, H int
	Pix  []uint8
}

type Rect struct{ X, Y, W, H int }

func New(w, h int) Image { return Image{W: w, H: h, Pix: make([]uint8, w*h*3)} }

// CropRect turns crop fractions (left, top, right, bottom) into pixels exactly
// like auto_reset.crop_frame: int() truncation, numpy-style clamping at the edge.
func CropRect(w, h int, c [4]float64) (Rect, error) {
	l, t := int(float64(w)*c[0]), int(float64(h)*c[1])
	r, b := min(int(float64(w)*c[2]), w), min(int(float64(h)*c[3]), h)
	if r <= l || b <= t {
		return Rect{}, fmt.Errorf("crop produced an empty image: frame=%dx%d, crop=l%d:r%d,t%d:b%d from coords=%v",
			w, h, l, r, t, b, c)
	}
	return Rect{X: l, Y: t, W: r - l, H: b - t}, nil
}

func (m Image) Crop(r Rect) Image {
	out := New(r.W, r.H)
	for y := 0; y < r.H; y++ {
		src := ((r.Y+y)*m.W + r.X) * 3
		copy(out.Pix[y*r.W*3:(y+1)*r.W*3], m.Pix[src:src+r.W*3])
	}
	return out
}

// FromImage converts any decoded image (PNG from cv2.imwrite, etc.) to BGR.
func FromImage(src image.Image) Image {
	b := src.Bounds()
	out := New(b.Dx(), b.Dy())
	for y := 0; y < out.H; y++ {
		for x := 0; x < out.W; x++ {
			r, g, bl, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			i := (y*out.W + x) * 3
			out.Pix[i], out.Pix[i+1], out.Pix[i+2] = uint8(bl>>8), uint8(g>>8), uint8(r>>8)
		}
	}
	return out
}

func LoadPNG(path string) (Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return Image{}, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return Image{}, fmt.Errorf("%s: %w", path, err)
	}
	return FromImage(img), nil
}

func (m Image) rgba() *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, m.W, m.H))
	for i, j := 0, 0; i < len(m.Pix); i, j = i+3, j+4 {
		out.Pix[j], out.Pix[j+1], out.Pix[j+2], out.Pix[j+3] = m.Pix[i+2], m.Pix[i+1], m.Pix[i], 255
	}
	return out
}

func EncodeJPEGBase64(m Image, quality int) (string, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, m.rgba(), &jpeg.Options{Quality: quality}); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func SavePNG(m Image, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, m.rgba()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
