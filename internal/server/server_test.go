package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grillermo/awh/internal/samples"
	"github.com/grillermo/awh/internal/store"
	"github.com/grillermo/awh/internal/vision"
)

type fakeCam struct{}

func (fakeCam) Size(context.Context) (int, int, error) { return 640, 480, nil }
func (fakeCam) Crop(_ context.Context, r vision.Rect) (vision.Image, error) {
	return vision.New(r.W, r.H), nil
}
func (fakeCam) FullJPEG(context.Context) ([]byte, error) { return []byte{0xFF, 0xD8, 0xFF}, nil }

type fakeOCR struct{ text string }

func (f fakeOCR) Read(vision.Image) (string, float64, error) { return f.text, 0.95, nil }

type fakeReset struct{ n int }

func (f *fakeReset) Reset(context.Context) error { f.n++; return nil }

func newTest(t *testing.T, dry bool) (*Server, *fakeReset, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	col, _ := samples.NewCollector(filepath.Join(dir, "s"), samples.MaxBytes, func() float64 { return 1 }, func() float64 { return 0 })
	rs := &fakeReset{}
	s, err := New(fakeCam{}, fakeOCR{"E1"}, rs, st, col, Options{DryRun: dry, SamplesDir: filepath.Join(dir, "s")})
	if err != nil {
		t.Fatal(err)
	}
	return s, rs, s.Handler()
}

func do(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestLastFrameBeforeAndAfterATick(t *testing.T) {
	s, _, h := newTest(t, true)
	if code, out := do(t, h, "GET", "/last_frame", ""); code != 503 || out["error"] != "no frame yet" {
		t.Fatalf("%d %v", code, out)
	}
	if err := s.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	code, out := do(t, h, "GET", "/last_frame", "")
	if code != 200 || out["ocr_kind"] != "error" || out["ocr_text"] != "E1" || out["frame_b64"] == "" {
		t.Fatalf("%d %v", code, out)
	}
}

func TestUpdateCropMovesOneEdgePersistsAndClearsTheFrame(t *testing.T) {
	s, _, h := newTest(t, true)
	s.tick(t.Context())
	before := s.store.Crop()
	code, out := do(t, h, "POST", "/api/update-crop", `{"edge":"left","dir":1}`)
	if code != 200 || out["ok"] != true {
		t.Fatalf("%d %v", code, out)
	}
	if got := s.store.Crop()[0]; got != before[0]+0.001 {
		t.Fatalf("left %v -> %v", before[0], got)
	}
	if code, _ := do(t, h, "GET", "/last_frame", ""); code != 503 {
		t.Fatal("frame not cleared")
	}
	if code, _ := do(t, h, "POST", "/api/update-crop", `{"edge":"nope","dir":1}`); code != 400 {
		t.Fatal("bad edge accepted")
	}
	if code, _ := do(t, h, "POST", "/api/update-crop", `{"edge":"left","dir":5}`); code != 400 {
		t.Fatal("bad dir accepted")
	}
}

func TestValidation(t *testing.T) {
	_, _, h := newTest(t, true)
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/save-view", `{"view":{"scale":1}}`},
		{"POST", "/api/save-crop", `{"crop":[1,2]}`},
		{"POST", "/api/ocr-samples/selection", `{"select":"x"}`},
		{"GET", "/api/display?display_crop=1,2,3", ""},
		{"GET", "/api/display?display_crop=0.7,0.5,0.6,0.6", ""},
	} {
		if code, out := do(t, h, c.method, c.path, c.body); code != 400 && code != 500 || out["ok"] != false {
			t.Errorf("%s %s: %d %v", c.method, c.path, code, out)
		}
	}
}

func TestResetHonoursDryRun(t *testing.T) {
	_, rs, h := newTest(t, true)
	do(t, h, "POST", "/reset", "")
	if rs.n != 0 {
		t.Fatal("dry run reset the plug")
	}
	_, rs, h = newTest(t, false)
	if code, out := do(t, h, "POST", "/reset", ""); code != 200 || out["message"] != "reset initiated" || rs.n != 1 {
		t.Fatalf("%d %v n=%d", code, out, rs.n)
	}
}

func TestSampleImagesRejectTraversal(t *testing.T) {
	_, _, h := newTest(t, true)
	for _, p := range []string{"/ocr-samples/nope/a.png", "/ocr-samples/text/a.txt", "/ocr-samples/text/../../db.sqlite3"} {
		if code, _ := do(t, h, "GET", p, ""); code != 404 {
			t.Errorf("%s: %d", p, code)
		}
	}
}

func TestSampleImage404IsHTML(t *testing.T) {
	_, _, h := newTest(t, true)
	req := httptest.NewRequest("GET", "/ocr-samples/nope/x.png", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 404 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestIndexRendersCropAsJSON(t *testing.T) {
	_, _, h := newTest(t, true)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	body := w.Body.String()
	// html/template pads the JS value with spaces: compare ignoring them.
	if w.Code != 200 || !strings.Contains(body, "const INITIAL_RECT = [0.631,0.585,0.676,0.691]") ||
		!regexp.MustCompile(`const INITIAL_VIEW =\s+null\s*;`).MatchString(body) {
		t.Fatalf("%d\n%s", w.Code, body[:min(400, len(body))])
	}
}

func grepLines(b string) string {
	var o []string
	for _, l := range strings.Split(b, "\n") {
		if strings.Contains(l, "INITIAL_") || strings.Contains(l, "IMG_SRC") {
			o = append(o, l)
		}
	}
	return strings.Join(o, "\n")
}

func TestIndexRendersSavedView(t *testing.T) {
	_, _, h := newTest(t, true)
	if code, out := do(t, h, "POST", "/api/save-view", `{"view":{"scale":2,"offsetXFrac":0.1,"offsetYFrac":"0.2","extra":9}}`); code != 200 ||
		out["ok"] != true {
		t.Fatalf("%d %v", code, out)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if !regexp.MustCompile(`const INITIAL_VIEW =\s+\{"offsetXFrac":0.1,"offsetYFrac":0.2,"scale":2\}\s*;`).MatchString(w.Body.String()) {
		t.Fatal("saved view not rendered")
	}
}

func TestBadDisplayCropIs500AndGoodOneRenders(t *testing.T) {
	_, _, h := newTest(t, true)
	if code, out := do(t, h, "GET", "/api/display?display_crop=1,2,3", ""); code != 500 || out["ok"] != false || out["error"] == nil {
		t.Fatalf("%d %v", code, out)
	}
	if code, out := do(t, h, "GET", "/api/display?display_crop=0.6,0.5,0.7,0.6", ""); code != 200 || out["fixed_frame_b64"] == nil {
		t.Fatalf("%d %v", code, out)
	}
	if code, out := do(t, h, "GET", "/api/display", ""); code != 200 || out["ok"] != true {
		t.Fatalf("%d %v", code, out)
	}
}

func TestSelectionFlowsIntoTheSampleList(t *testing.T) {
	s, _, h := newTest(t, true)
	s.samples, _ = samples.NewCollector(s.opts.SamplesDir, samples.MaxBytes, func() float64 { return 1 }, func() float64 { return 1000 })
	if _, err := s.samples.MaybeSave("E1", vision.New(8, 8), vision.New(8, 8), 0.9); err != nil {
		t.Fatal(err)
	}
	list := func() []any {
		_, out := do(t, h, "GET", "/api/ocr-samples", "")
		return out["samples"].([]any)
	}
	l := list()
	if len(l) != 1 || l[0].(map[string]any)["selected"] != false {
		t.Fatalf("%v", l)
	}
	id := l[0].(map[string]any)["id"].(string)
	req := httptest.NewRequest("GET", "/ocr-samples/"+l[0].(map[string]any)["ocr_image"].(string), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "public, max-age=86400" || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("sample image: %d %v", rec.Code, rec.Header())
	}
	if code, out := do(t, h, "POST", "/api/ocr-samples/selection", `{"select":["`+id+`"]}`); code != 200 || out["selected_count"] != float64(1) {
		t.Fatalf("%d %v", code, out)
	}
	if list()[0].(map[string]any)["selected"] != true {
		t.Fatal("not selected")
	}
	if code, out := do(t, h, "POST", "/api/ocr-samples/selection", `{"deselect":["`+id+`"]}`); code != 200 || out["selected_count"] != float64(0) {
		t.Fatalf("%d %v", code, out)
	}
	if code, _ := do(t, h, "POST", "/api/ocr-samples/selection", `{"select":null}`); code != 400 {
		t.Fatal("null select accepted")
	}
	if code, _ := do(t, h, "POST", "/api/ocr-samples/selection", `{"select":[1]}`); code != 400 {
		t.Fatal("non-string id accepted")
	}
}

func TestSaveCropAndSampleImageServing(t *testing.T) {
	s, _, h := newTest(t, true)
	if code, out := do(t, h, "POST", "/api/save-crop", `{"crop":[0.1,"0.2",0.5,0.6]}`); code != 200 || out["ok"] != true {
		t.Fatalf("%d %v", code, out)
	}
	if got := s.store.Crop(); got != [4]float64{0.1, 0.2, 0.5, 0.6} {
		t.Fatalf("%v", got)
	}
	for _, body := range []string{`{"crop":"x"}`, `{"crop":[1,2,3,null]}`, `nope`, ``} {
		if code, _ := do(t, h, "POST", "/api/save-crop", body); code != 400 {
			t.Errorf("%q: %d", body, code)
		}
	}
}

// The loop must only capture while /last_frame was asked for recently.
func TestLoopOnlyRunsWhileSomeonePolls(t *testing.T) {
	s, _, h := newTest(t, true)
	cam := &countCam{}
	s.cam = cam
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go s.Loop(ctx)
	time.Sleep(300 * time.Millisecond)
	if cam.n.Load() != 0 {
		t.Fatal("captured with nobody polling")
	}
	do(t, h, "GET", "/last_frame", "")
	deadline := time.Now().Add(3 * time.Second)
	for cam.n.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if cam.n.Load() == 0 {
		t.Fatal("no capture after /last_frame")
	}
}

type countCam struct {
	fakeCam
	n atomic.Int32
}

func (c *countCam) Crop(ctx context.Context, r vision.Rect) (vision.Image, error) {
	c.n.Add(1)
	return c.fakeCam.Crop(ctx, r)
}
