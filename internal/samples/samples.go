// Package samples keeps example frames of OCR misses for later tuning, and
// lists them for the iOS sample picker. Port of ocr_samples.py, same files.
package samples

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/grillermo/awh/internal/vision"
)

const (
	textMaxPerText   = 3
	textMinInterval  = 60.0
	emptyProbability = 0.02
	emptyMinInterval = 600.0
	emptyMaxTotal    = 40
	MaxBytes         = 50 * 1024 * 1024
)

var (
	number  = regexp.MustCompile(`^\p{Nd}+$`)
	slugBad = regexp.MustCompile(`[^A-Za-z0-9]+`)
)

func compact(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// Kind is "number", "digit" (a lone digit), "empty" or "text".
func Kind(text string) string {
	c := compact(text)
	switch {
	case c == "":
		return "empty"
	case number.MatchString(c) && utf8.RuneCountInString(c) == 1:
		return "digit"
	case number.MatchString(c):
		return "number"
	}
	return "text"
}

type Record struct {
	At      float64  `json:"at"`
	Kind    string   `json:"kind"`
	OCRText string   `json:"ocr_text"`
	Raw     string   `json:"raw"`
	OCR     string   `json:"ocr"`
	Score   *float64 `json:"score,omitempty"`
}

type seen struct {
	count int
	last  float64
}

type Collector struct {
	root       string
	maxBytes   int64
	rnd, clock func() float64
	mu         sync.Mutex
	used       int64
	warned     bool
	text       map[string]seen
	emptyCount int
	emptyLast  float64
}

func NewCollector(root string, maxBytes int64, rnd, clock func() float64) (*Collector, error) {
	c := &Collector{root: root, maxBytes: maxBytes, rnd: rnd, clock: clock, text: map[string]seen{}}
	filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				c.used += info.Size()
			}
		}
		return nil
	})
	for _, rec := range readIndex(root) {
		switch rec.Kind {
		case "text", "digit":
			s := c.text[rec.OCRText]
			c.text[rec.OCRText] = seen{s.count + 1, math.Max(s.last, rec.At)}
		case "empty":
			c.emptyCount++
			c.emptyLast = math.Max(c.emptyLast, rec.At)
		}
	}
	return c, nil
}

func readIndex(root string) []Record {
	f, err := os.Open(filepath.Join(root, "index.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Record
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			out = append(out, r)
		}
	}
	return out
}

func (c *Collector) shouldSave(text string) string {
	kind := Kind(text)
	if kind == "number" {
		return ""
	}
	now := c.clock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.used >= c.maxBytes {
		if !c.warned {
			log.Printf("[samples] disk budget of %d MB used up; not saving more samples", c.maxBytes>>20)
			c.warned = true
		}
		return ""
	}
	if kind == "text" || kind == "digit" {
		key := strings.TrimSpace(text)
		s := c.text[key]
		if s.count >= textMaxPerText || now-s.last < textMinInterval {
			return ""
		}
		c.text[key] = seen{s.count + 1, now}
		return kind
	}
	if c.emptyCount >= emptyMaxTotal || now-c.emptyLast < emptyMinInterval || c.rnd() >= emptyProbability {
		return ""
	}
	c.emptyCount++
	c.emptyLast = now
	return kind
}

func slug(text string) string {
	s := strings.Trim(slugBad.ReplaceAllString(text, "-"), "-")
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		return "x"
	}
	return s
}

// MaybeSave stores raw (the crop OCR read) and processed (the enhanced frame)
// when this reading is worth keeping. It returns nil when it is not.
func (c *Collector) MaybeSave(text string, raw, processed vision.Image, score float64) (*Record, error) {
	kind := c.shouldSave(text)
	if kind == "" {
		return nil, nil
	}
	now := c.clock()
	name := "empty"
	if kind != "empty" {
		name = slug(text)
	}
	base := time.Unix(0, int64(now*1e9)).Local().Format("20060102-150405") + "_" + name
	dir := filepath.Join(c.root, kind)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	rawPath, ocrPath := filepath.Join(dir, base+"_raw.png"), filepath.Join(dir, base+"_ocr.png")
	if err := vision.SavePNG(raw, rawPath); err != nil {
		return nil, err
	}
	if err := vision.SavePNG(processed, ocrPath); err != nil {
		return nil, err
	}
	sc := math.Round(score*1000) / 1000
	rec := &Record{At: now, Kind: kind, OCRText: strings.TrimSpace(text), Raw: rawPath, OCR: ocrPath, Score: &sc}
	line, _ := json.Marshal(rec)
	line = append(line, '\n')
	written := int64(len(line))
	for _, p := range []string{rawPath, ocrPath} {
		if info, err := os.Stat(p); err == nil {
			written += info.Size()
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	f, err := os.OpenFile(filepath.Join(c.root, "index.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		return nil, err
	}
	c.used += written
	log.Printf("[samples] saved %s sample %q -> %s", kind, rec.OCRText, ocrPath)
	return rec, nil
}

// ID is "<kind>/<file stem>", e.g. "text/20261106-182945_4C".
func ID(r Record) string {
	return fmt.Sprintf("%s/%s", r.Kind, strings.TrimSuffix(filepath.Base(r.OCR), "_ocr.png"))
}

type Listed struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	OCRText  string  `json:"ocr_text"`
	At       float64 `json:"at"`
	OCRImage string  `json:"ocr_image"`
	RawImage string  `json:"raw_image"`
	Selected bool    `json:"selected"`
}

func rel(root, p string) string {
	ar, _ := filepath.Abs(root)
	ap, _ := filepath.Abs(p)
	r, err := filepath.Rel(ar, ap)
	if err != nil {
		return p
	}
	return r
}

// List returns every sample whose image still exists, newest first.
func List(root string) []Listed {
	out := []Listed{}
	for _, r := range readIndex(root) {
		if _, err := os.Stat(r.OCR); err != nil {
			continue
		}
		out = append(out, Listed{ID: ID(r), Kind: r.Kind, OCRText: r.OCRText, At: r.At,
			OCRImage: rel(root, r.OCR), RawImage: rel(root, r.Raw)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At > out[j].At })
	return out
}
