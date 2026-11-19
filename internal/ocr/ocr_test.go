package ocr

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/grillermo/awh/internal/vision"
)

const td = "../testdata"

func TestPreprocessMatchesRapidOCRTensor(t *testing.T) {
	for _, n := range []string{"digit45", "noise"} {
		img, err := vision.LoadPNG(filepath.Join(td, n+".png"))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(td, n+"_tensor.f32"))
		if err != nil {
			t.Fatal(err)
		}
		got, width := Preprocess(img)
		if len(raw)/4 != len(got) || len(got) != 3*recH*width {
			t.Fatalf("%s: len python %d, go %d (width %d)", n, len(raw)/4, len(got), width)
		}
		worst := 0.0
		for i := range got {
			want := math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
			worst = math.Max(worst, math.Abs(float64(got[i]-want)))
		}
		t.Logf("%s: max |diff| %.7f (limit %.5f)", n, worst, 2.0/255)
		if worst > 2.0/255+1e-6 { // one grey level, after (x/255-0.5)/0.5
			t.Errorf("%s: max |diff| %.5f", n, worst)
		}
	}
}

func TestDecodeCollapsesRepeatsAndBlanks(t *testing.T) {
	keys := []string{"blank", "4", "5", " "}
	// timesteps: 4 4 blank 5 5 -> "45"; confs 0.9, 0.8 (first of each run)
	probs := []float32{
		0.05, 0.9, 0.03, 0.02,
		0.1, 0.7, 0.1, 0.1,
		0.9, 0.05, 0.03, 0.02,
		0.1, 0.05, 0.8, 0.05,
		0.1, 0.05, 0.6, 0.25,
	}
	text, score := Decode(probs, 5, 4, keys)
	if text != "45" || math.Abs(score-0.85) > 1e-9 {
		t.Fatalf("got %q %.5f", text, score)
	}
}

func TestDecodeAllBlankIsEmptyZero(t *testing.T) {
	text, score := Decode([]float32{1, 0, 1, 0}, 2, 2, []string{"blank", "x"})
	if text != "" || score != 0 {
		t.Fatalf("got %q %v", text, score)
	}
}

func TestRecognizerReadsTheFixture45(t *testing.T) {
	lib := "/opt/homebrew/lib/libonnxruntime.dylib"
	if _, err := os.Stat(lib); err != nil {
		t.Skip("onnxruntime not installed")
	}
	if err := Init(lib); err != nil {
		t.Fatal(err)
	}
	r, err := NewRecognizer("../../models/PP-OCRv6_rec_small.onnx", "../../models/ppocrv6_rec_keys.json", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	img, _ := vision.LoadPNG(filepath.Join(td, "digit45.png"))
	text, score, err := r.Read(img)
	if err != nil || text == "" || score < 0.7 {
		t.Fatalf("got %q %.3f %v", text, score, err)
	}
	t.Logf("read %q at %.5f", text, score)
}
