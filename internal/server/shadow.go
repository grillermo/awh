package server

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/grillermo/awh/internal/vision"
)

// shadowRecorder saves one crop and its Go reading every interval, as
// manifest lines ({"id", "path", ...}) that eval/ocr_reference.py and
// awh-ocr-eval can both replay (eval E3).
type shadowRecorder struct {
	dir   string
	every time.Duration
	mu    sync.Mutex
	last  time.Time
}

func newShadowRecorder(dir string, every time.Duration) *shadowRecorder {
	return &shadowRecorder{dir: dir, every: every}
}

func (r *shadowRecorder) maybeRecord(crop vision.Image, text string, score float64, kind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if now.Sub(r.last) < r.every {
		return
	}
	r.last = now
	stamp := now.Format("20060102-150405")
	crops := filepath.Join(r.dir, "crops")
	if err := os.MkdirAll(crops, 0o755); err != nil {
		log.Printf("[shadow] %v", err)
		return
	}
	path, _ := filepath.Abs(filepath.Join(crops, stamp+".png"))
	if err := vision.SavePNG(crop, path); err != nil {
		log.Printf("[shadow] %v", err)
		return
	}
	line, _ := json.Marshal(map[string]any{"id": "shadow/" + stamp, "path": path,
		"go_text": text, "go_score": score, "go_kind": kind, "at": now.Unix()})
	f, err := os.OpenFile(filepath.Join(r.dir, "manifest.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("[shadow] %v", err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", line)
}
