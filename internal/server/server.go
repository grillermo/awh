// Package server is the HTTP API and live capture loop, a port of app.py.
package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/grillermo/awh/internal/reading"
	"github.com/grillermo/awh/internal/samples"
	"github.com/grillermo/awh/internal/store"
	"github.com/grillermo/awh/internal/vision"
	"github.com/grillermo/awh/web"
)

const (
	cropStep    = 0.001
	idleAfter   = 10 * time.Second // the loop sleeps when nobody polled /last_frame for this long
	jpegQuality = 85
)

type Camera interface {
	Size(ctx context.Context) (int, int, error)
	Crop(ctx context.Context, r vision.Rect) (vision.Image, error)
	FullJPEG(ctx context.Context) ([]byte, error)
}

type Reader interface {
	Read(vision.Image) (string, float64, error)
}

type Resetter interface {
	Reset(ctx context.Context) error
}

type Options struct {
	DryRun     bool   // never touch the plug
	SamplesDir string // where /ocr-samples/ images are served from
	ShadowDir  string // when set, record a crop + reading every 10 s (eval E3)
}

type frame struct{ b64, text, kind string }

type Server struct {
	cam     Camera
	ocr     Reader
	reset   Resetter
	store   *store.Store
	samples *samples.Collector
	opts    Options
	pages   *template.Template

	cropMu sync.Mutex
	crop   [4]float64

	sizeMu sync.Mutex
	w, h   int

	frameMu sync.Mutex
	frame   frame

	resetting atomic.Bool // single-flight guard for /reset

	lastReq atomic.Int64 // unix nanos of the last /last_frame
	shadow  *shadowRecorder
}

func New(cam Camera, ocr Reader, reset Resetter, st *store.Store, col *samples.Collector, opts Options) (*Server, error) {
	pages, err := template.ParseFS(web.Templates, "templates/index.html")
	if err != nil {
		return nil, err
	}
	s := &Server{cam: cam, ocr: ocr, reset: reset, store: st, samples: col, opts: opts, pages: pages, crop: st.Crop()}
	if opts.ShadowDir != "" {
		s.shadow = newShadowRecorder(opts.ShadowDir, 10*time.Second)
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /live", s.live)
	mux.HandleFunc("GET /last_frame", s.lastFrame)
	mux.HandleFunc("GET /api/display", s.display)
	mux.HandleFunc("POST /api/save-crop", s.saveCrop)
	mux.HandleFunc("POST /api/save-view", s.saveView)
	mux.HandleFunc("POST /api/update-crop", s.updateCrop)
	mux.HandleFunc("POST /reset", s.resetPlug)
	mux.HandleFunc("GET /api/ocr-samples", s.listSamples)
	mux.HandleFunc("POST /api/ocr-samples/selection", s.selectSamples)
	mux.HandleFunc("GET /ocr-samples/{kind}/{file...}", s.sampleImage)
	mux.HandleFunc("POST /report-error", s.reportError)
	return recoverJSON(noDotDot(mux))
}

// noDotDot answers 404 for any path with a ".." segment; ServeMux would
// otherwise redirect it to the cleaned path.
func noDotDot(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, seg := range strings.Split(r.URL.Path, "/") {
			if seg == ".." {
				http.NotFound(w, r)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// --- JSON helpers -----------------------------------------------------------

type obj = map[string]any

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, obj{"ok": false, "error": msg})
}

// readBody decodes the request like Flask's get_json(force=True, silent=True)
// followed by `or {}`: anything that is not a JSON object becomes empty.
func readBody(r *http.Request) map[string]any {
	var m map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

// pyFloat mirrors Python's float(v) for the JSON types.
func pyFloat(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case bool:
		if x {
			return 1, nil
		}
		return 0, nil
	case string:
		return strconv.ParseFloat(strings.TrimSpace(x), 64)
	}
	return 0, fmt.Errorf("float() argument must be a string or a real number, not %T", v)
}

func recoverJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				log.Printf("[http] panic on %s %s: %v", r.Method, r.URL.Path, v)
				fail(w, 500, fmt.Sprint(v))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// --- state ------------------------------------------------------------------

func (s *Server) currentCrop() [4]float64 {
	s.cropMu.Lock()
	defer s.cropMu.Unlock()
	return s.crop
}

func (s *Server) setFrame(f frame) { s.frameMu.Lock(); s.frame = f; s.frameMu.Unlock() }
func (s *Server) clearFrame()      { s.setFrame(frame{}) }

func (s *Server) frameSize(ctx context.Context) (int, int, error) {
	s.sizeMu.Lock()
	defer s.sizeMu.Unlock()
	if s.w == 0 {
		w, h, err := s.cam.Size(ctx)
		if err != nil {
			return 0, 0, err
		}
		s.w, s.h = w, h
	}
	return s.w, s.h, nil
}

func (s *Server) forgetSize() { s.sizeMu.Lock(); s.w, s.h = 0, 0; s.sizeMu.Unlock() }

func (s *Server) grab(ctx context.Context, c [4]float64) (vision.Image, error) {
	w, h, err := s.frameSize(ctx)
	if err != nil {
		return vision.Image{}, err
	}
	r, err := vision.CropRect(w, h, c)
	if err != nil {
		return vision.Image{}, err
	}
	img, err := s.cam.Crop(ctx, r)
	if err != nil {
		s.forgetSize() // the stream may have changed resolution
	}
	return img, err
}

// --- live loop ----------------------------------------------------------------

// Loop runs the capture loop until ctx ends, like app._capture_loop.
func (s *Server) Loop(ctx context.Context) {
	for ctx.Err() == nil {
		if time.Since(time.Unix(0, s.lastReq.Load())) > idleAfter {
			sleep(ctx, time.Second)
			continue
		}
		if err := s.tick(ctx); err != nil {
			log.Printf("[live] capture error: %v", err)
			sleep(ctx, time.Second)
		}
	}
}

// Run serves hs on ln and runs the capture loop until ctx ends. It then shuts
// the HTTP server down, waiting up to grace for in-flight requests (a /reset
// is off, wait, on and must finish or the heater stays off), and waits for the
// loop to exit, so the caller can safely close the recognizer and store when
// Run returns.
func (s *Server) Run(ctx context.Context, hs *http.Server, ln net.Listener, grace time.Duration) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	loopDone := make(chan struct{})
	go func() { defer close(loopDone); s.Loop(ctx) }()

	shutDone := make(chan struct{})
	go func() {
		defer close(shutDone)
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		if err := hs.Shutdown(shut); err != nil {
			log.Printf("[http] shutdown: %v", err)
			hs.Close()
		}
	}()

	err := hs.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	} else {
		cancel() // Serve failed on its own: stop the loop and the shutdown goroutine
	}
	// Serve returns as soon as Shutdown starts; wait for it to drain.
	<-shutDone
	<-loopDone
	return err
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

func (s *Server) tick(ctx context.Context) error {
	cropped, err := s.grab(ctx, s.currentCrop())
	if err != nil {
		return err
	}
	// The enhanced, perspective-fixed frame is for people; OCR reads the raw crop.
	fixed := vision.Enhance(vision.FixPerspective(cropped))
	text, score, err := s.ocr.Read(cropped)
	if err != nil {
		return err
	}
	kind, value := reading.Classify(text, score)
	if s.samples != nil {
		if _, err := s.samples.MaybeSave(text, cropped, fixed, score); err != nil {
			log.Printf("[samples] save failed: %v", err)
		}
	}
	if s.shadow != nil {
		s.shadow.maybeRecord(cropped, text, score, kind)
	}
	b64, err := vision.EncodeJPEGBase64(fixed, jpegQuality)
	if err != nil {
		return err
	}
	s.setFrame(frame{b64: b64, text: value, kind: kind})
	return nil
}

// --- handlers -----------------------------------------------------------------

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	data := struct {
		FrameB64    string
		Crop        []float64
		InitialView any // nil renders as JS null
		Error       string
	}{}
	c := s.store.Crop()
	data.Crop = c[:]
	if v := s.store.View(); v != nil {
		data.InitialView = v
	}
	if jpg, err := s.cam.FullJPEG(r.Context()); err != nil {
		log.Printf("[index] %v", err)
		data.Error = err.Error()
	} else {
		data.FrameB64 = base64.StdEncoding.EncodeToString(jpg)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.pages.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("[index] template: %v", err)
	}
}

func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	page, err := web.Templates.ReadFile("templates/live.html")
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(page)
}

func (s *Server) lastFrame(w http.ResponseWriter, r *http.Request) {
	s.lastReq.Store(time.Now().UnixNano())
	s.frameMu.Lock()
	f := s.frame
	s.frameMu.Unlock()
	if f.b64 == "" {
		fail(w, 503, "no frame yet")
		return
	}
	writeJSON(w, 200, obj{"ok": true, "frame_b64": f.b64, "ocr_text": f.text, "ocr_kind": f.kind})
}

// parseCropArg mirrors app.parse_display_crop_arg (empty means DISPLAY_CROP).
func parseCropArg(v string) ([4]float64, error) {
	if v == "" {
		return store.DefaultCrop, nil
	}
	parts := strings.Split(v, ",")
	if len(parts) != 4 {
		return [4]float64{}, errors.New("display_crop must have 4 comma-separated values")
	}
	var c [4]float64
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return c, err
		}
		if f < 0 || f > 1 {
			return c, errors.New("display_crop values must be between 0 and 1")
		}
		c[i] = f
	}
	if c[0] >= c[2] || c[1] >= c[3] {
		return c, errors.New("display_crop must satisfy left < right and top < bottom")
	}
	return c, nil
}

func (s *Server) display(w http.ResponseWriter, r *http.Request) {
	c, err := parseCropArg(r.URL.Query().Get("display_crop"))
	if err != nil {
		fail(w, 500, err.Error()) // app.py answers 500 here too
		return
	}
	img, err := s.grab(r.Context(), c)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	b64, err := vision.EncodeJPEGBase64(vision.Enhance(vision.FixPerspective(img)), jpegQuality)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, obj{"ok": true, "fixed_frame_b64": b64})
}

func (s *Server) saveCrop(w http.ResponseWriter, r *http.Request) {
	raw, ok := readBody(r)["crop"].([]any)
	if !ok || len(raw) != 4 {
		fail(w, 400, "crop must be array of 4 floats")
		return
	}
	var c [4]float64
	for i, v := range raw {
		f, err := pyFloat(v)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		c[i] = f
	}
	if err := s.store.SaveCrop(c); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.cropMu.Lock()
	s.crop = c
	s.cropMu.Unlock()
	s.clearFrame()
	writeJSON(w, 200, obj{"ok": true, "crop": c[:]})
}

func (s *Server) saveView(w http.ResponseWriter, r *http.Request) {
	view, ok := readBody(r)["view"].(map[string]any)
	keys := []string{"scale", "offsetXFrac", "offsetYFrac"}
	for _, k := range keys {
		if _, has := view[k]; !has {
			ok = false
		}
	}
	if !ok {
		fail(w, 400, "view must have scale, offsetXFrac, offsetYFrac")
		return
	}
	safe := map[string]float64{}
	for _, k := range keys {
		f, err := pyFloat(view[k])
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		safe[k] = f
	}
	if err := s.store.SaveView(safe); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, obj{"ok": true, "view": safe})
}

func (s *Server) updateCrop(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	edge, _ := body["edge"].(string)
	idx, ok := map[string]int{"left": 0, "top": 1, "right": 2, "bottom": 3}[edge]
	if !ok {
		fail(w, 400, "edge must be left/top/right/bottom")
		return
	}
	// Python: `dir in (-1, 1)`, so 1.0 and true pass too.
	var dir float64
	switch d := body["dir"].(type) {
	case float64:
		dir = d
	case bool:
		if d {
			dir = 1
		}
	}
	if dir != 1 && dir != -1 {
		fail(w, 400, "dir must be -1 or 1")
		return
	}
	s.cropMu.Lock()
	c := s.crop
	c[idx] = math.Min(1, math.Max(0, math.Round((c[idx]+dir*cropStep)*1e4)/1e4))
	if c[0] >= c[2] || c[1] >= c[3] {
		s.cropMu.Unlock()
		fail(w, 400, "invalid crop bounds")
		return
	}
	s.crop = c
	s.cropMu.Unlock()
	if err := s.store.SaveCrop(c); err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.clearFrame()
	writeJSON(w, 200, obj{"ok": true, "crop": c[:]})
}

// sameOrigin is true when the request has no Origin header (curl, the kiosk
// page's same-origin POSTs in some browsers) or the Origin host is this server's.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host != "" && u.Host == r.Host
}

func (s *Server) resetPlug(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		fail(w, 403, "cross-origin request refused")
		return
	}
	if !s.resetting.CompareAndSwap(false, true) {
		fail(w, 409, "reset already in progress")
		return
	}
	defer s.resetting.Store(false)
	// Never let a dropped client connection cut a reset short: that could
	// leave the heater off.
	resetCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
	defer cancel()
	if s.opts.DryRun {
		log.Printf("[tapo] dry run: reset requested, plug left alone")
	} else if err := s.reset.Reset(resetCtx); err != nil {
		log.Printf("[tapo] ERROR: %v", err) // app.py logs and still answers ok
	}
	writeJSON(w, 200, obj{"ok": true, "message": "reset initiated"})
}

func (s *Server) listSamples(w http.ResponseWriter, r *http.Request) {
	sel, err := s.store.Selection()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	list := samples.List(s.opts.SamplesDir)
	for i := range list {
		list[i].Selected = sel[list[i].ID]
	}
	writeJSON(w, 200, obj{"ok": true, "samples": list})
}

// idList reads body[key] as Python's isinstance(v, list) of str; absent means [].
func idList(body map[string]any, key string) ([]string, bool) {
	v, present := body[key]
	if !present {
		return nil, true
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		str, ok := e.(string)
		if !ok {
			return nil, false
		}
		out = append(out, str)
	}
	return out, true
}

func (s *Server) selectSamples(w http.ResponseWriter, r *http.Request) {
	body := readBody(r)
	sel, ok1 := idList(body, "select")
	desel, ok2 := idList(body, "deselect")
	if !ok1 || !ok2 {
		fail(w, 400, "select and deselect must be arrays of sample ids")
		return
	}
	if err := s.store.UpdateSelection(sel, desel, float64(time.Now().UnixNano())/1e9); err != nil {
		fail(w, 500, err.Error())
		return
	}
	cur, err := s.store.Selection()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, obj{"ok": true, "selected_count": len(cur)})
}

func (s *Server) sampleImage(w http.ResponseWriter, r *http.Request) {
	kind, file := r.PathValue("kind"), r.PathValue("file")
	if (kind != "text" && kind != "digit" && kind != "empty") || !strings.HasSuffix(file, ".png") ||
		strings.Contains(file, "..") {
		notFoundHTML(w)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, filepath.Join(s.opts.SamplesDir, kind, filepath.Clean("/"+file)))
}

func (s *Server) reportError(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	log.Printf("[report-error] %s", body)
	writeJSON(w, 200, obj{"ok": true})
}

// notFoundHTML mimics Flask's HTML 404 page, which is what the Python server sends.
func notFoundHTML(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(404)
	io.WriteString(w, "<!doctype html>\n<html lang=en>\n<title>404 Not Found</title>\n<h1>Not Found</h1>\n<p>The requested URL was not found on the server. If you entered the URL manually please check your spelling and try again.</p>\n")
}
