// awh-ocr-eval runs the Go OCR over eval/manifest.jsonl and writes one JSON
// line per image with the same keys as eval/ocr_reference.py.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"log"
	"math"
	"os"
	"time"

	"github.com/grillermo/awh/internal/ocr"
	"github.com/grillermo/awh/internal/reading"
	"github.com/grillermo/awh/internal/vision"
)

func main() {
	manifest := flag.String("manifest", "eval/manifest.jsonl", "")
	out := flag.String("out", "eval/results/after/ocr.jsonl", "")
	model := flag.String("model", "models/PP-OCRv6_rec_small.onnx", "")
	keys := flag.String("keys", "models/ppocrv6_rec_keys.json", "")
	lib := flag.String("ort-lib", "/opt/homebrew/lib/libonnxruntime.dylib", "")
	threads := flag.Int("threads", 1, "onnxruntime intra-op threads")
	hold := flag.Duration("hold", 0, "after the run, sleep this long (to measure footprint)")
	flag.Parse()

	if err := ocr.Init(*lib); err != nil {
		log.Fatal(err)
	}
	rec, err := ocr.NewRecognizer(*model, *keys, *threads)
	if err != nil {
		log.Fatal(err)
	}
	type row struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	var rows []row
	in, err := os.Open(*manifest)
	if err != nil {
		log.Fatal(err)
	}
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		var r row
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			log.Fatal(err)
		}
		rows = append(rows, r)
	}
	first, err := vision.LoadPNG(rows[0].Path)
	if err != nil {
		log.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		rec.Read(first)
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, r := range rows {
		img, err := vision.LoadPNG(r.Path)
		if err != nil {
			log.Fatal(err)
		}
		t0 := time.Now()
		text, score, err := rec.Read(img)
		ms := float64(time.Since(t0).Microseconds()) / 1000
		if err != nil {
			log.Fatalf("%s: %v", r.ID, err)
		}
		kind, value := reading.Classify(text, score)
		enc.Encode(map[string]any{"id": r.ID, "text": text, "score": score,
			"kind": kind, "value": value, "ms": math.Round(ms*100) / 100})
	}
	f.Close()
	log.Printf("wrote %d results to %s", len(rows), *out)
	if *hold > 0 {
		log.Printf("pid %d holding for %s", os.Getpid(), *hold)
		time.Sleep(*hold)
	}
}
