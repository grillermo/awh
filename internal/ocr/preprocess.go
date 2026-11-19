// Package ocr runs RapidOCR's PP-OCRv6 recogniser (no detection, no
// classifier) the way rapidocr/ch_ppocr_rec does, on onnxruntime.
package ocr

import (
	"math"

	"github.com/grillermo/awh/internal/vision"
)

const (
	recH = 48 // rec_img_shape [3, 48, 320]
	recW = 320
)

// Preprocess mirrors TextRecognizer.resize_norm_img for a batch of one:
// resize to height 48 keeping aspect, scale to [-1, 1], pad right with 0.
// The channel order stays BGR, exactly what RapidOCR feeds the model from an ndarray.
func Preprocess(m vision.Image) ([]float32, int) {
	maxWH := math.Max(float64(recW)/float64(recH), float64(m.W)*1.0/float64(m.H))
	width := int(float64(recH) * maxWH)
	resizedW := int(math.Ceil(float64(recH) * (float64(m.W) / float64(m.H))))
	if resizedW > width {
		resizedW = width
	}
	r := vision.ResizeLinear(m, resizedW, recH)
	data := make([]float32, 3*recH*width)
	for y := 0; y < recH; y++ {
		for x := 0; x < resizedW; x++ {
			for c := 0; c < 3; c++ {
				v := float32(r.Pix[(y*resizedW+x)*3+c]) / 255
				data[c*recH*width+y*width+x] = (v - 0.5) / 0.5
			}
		}
	}
	return data, width
}
