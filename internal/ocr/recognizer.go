package ocr

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/grillermo/awh/internal/vision"
)

// Init loads the onnxruntime shared library once per process.
func Init(libPath string) error {
	ort.SetSharedLibraryPath(libPath)
	return ort.InitializeEnvironment()
}

type Recognizer struct {
	mu   sync.Mutex
	sess *ort.DynamicAdvancedSession
	keys []string
}

func NewRecognizer(modelPath, keysPath string, threads int) (*Recognizer, error) {
	raw, err := os.ReadFile(keysPath)
	if err != nil {
		return nil, err
	}
	var chars []string
	if err := json.Unmarshal(raw, &chars); err != nil {
		return nil, fmt.Errorf("%s: %w", keysPath, err)
	}
	// rapidocr: insert " " at the end, then "blank" at index 0.
	keys := append(append([]string{"blank"}, chars...), " ")

	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	defer opts.Destroy()
	for _, set := range []func() error{
		func() error { return opts.SetIntraOpNumThreads(threads) },
		func() error { return opts.SetInterOpNumThreads(1) },
		func() error { return opts.SetCpuMemArena(false) },
		func() error { return opts.SetMemPattern(false) },
	} {
		if err := set(); err != nil {
			return nil, err
		}
	}
	sess, err := ort.NewDynamicAdvancedSession(modelPath, []string{"x"}, []string{"fetch_name_0"}, opts)
	if err != nil {
		return nil, err
	}
	return &Recognizer{sess: sess, keys: keys}, nil
}

// Read returns RapidOCR's (text, score) for the whole image, or ("", 0) when
// nothing was read, like auto_reset.read_display.
func (r *Recognizer) Read(m vision.Image) (string, float64, error) {
	data, width := Preprocess(m)
	in, err := ort.NewTensor(ort.NewShape(1, 3, recH, int64(width)), data)
	if err != nil {
		return "", 0, err
	}
	defer in.Destroy()
	outs := []ort.Value{nil}
	r.mu.Lock()
	err = r.sess.Run([]ort.Value{in}, outs)
	r.mu.Unlock()
	if err != nil {
		return "", 0, err
	}
	defer outs[0].Destroy()
	t, ok := outs[0].(*ort.Tensor[float32])
	if !ok {
		return "", 0, fmt.Errorf("unexpected output type %T", outs[0])
	}
	shape := t.GetShape()
	if len(shape) != 3 || int(shape[2]) != len(r.keys) {
		return "", 0, fmt.Errorf("output shape %v does not match %d keys", shape, len(r.keys))
	}
	text, score := Decode(t.GetData(), int(shape[1]), int(shape[2]), r.keys)
	if strings.TrimSpace(text) == "" {
		return "", 0, nil
	}
	return text, score, nil
}

func (r *Recognizer) Close() { r.sess.Destroy() }
