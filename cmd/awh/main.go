// awh serves the water-heater monitor: live OCR of the heater display and a
// Tapo plug reset. Go port of app.py; see docs/superpowers/plans/2026-11-18-go-server-rewrite.md.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/grillermo/awh/internal/capture"
	"github.com/grillermo/awh/internal/ocr"
	"github.com/grillermo/awh/internal/samples"
	"github.com/grillermo/awh/internal/server"
	"github.com/grillermo/awh/internal/store"
	"github.com/grillermo/awh/internal/tapo"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	addr := flag.String("addr", net.JoinHostPort(env("HOST", "0.0.0.0"), env("PORT", "5000")), "listen address")
	db := flag.String("db", "errors.sqlite3", "SQLite file shared with the Python server")
	samplesDir := flag.String("samples", "ocr_samples", "OCR sample folder")
	model := flag.String("model", "models/PP-OCRv6_rec_small.onnx", "")
	keys := flag.String("keys", "models/ppocrv6_rec_keys.json", "")
	ortLib := flag.String("ort-lib", "/opt/homebrew/lib/libonnxruntime.dylib", "")
	threads := flag.Int("ocr-threads", 1, "onnxruntime intra-op threads")
	dryRun := flag.Bool("dry-run", false, "never switch the Tapo plug")
	shadowDir := flag.String("shadow-dir", "", "record a crop + reading every 10 s here (eval)")
	flag.Parse()

	rtsp := os.Getenv("AWH_RTSP_URL")
	if rtsp == "" {
		log.Fatal("AWH_RTSP_URL is not set")
	}
	plug := &tapo.Client{Base: "http://" + os.Getenv("TAPO_IP"), Email: os.Getenv("TAPO_EMAIL"),
		Password: os.Getenv("TAPO_PASSWORD"), Wait: 10 * time.Second}
	if !*dryRun && (os.Getenv("TAPO_IP") == "" || plug.Email == "" || plug.Password == "") {
		log.Fatal("TAPO_IP, TAPO_EMAIL and TAPO_PASSWORD are required unless -dry-run")
	}

	if err := ocr.Init(*ortLib); err != nil {
		log.Fatal(err)
	}
	rec, err := ocr.NewRecognizer(*model, *keys, *threads)
	if err != nil {
		log.Fatal(err)
	}
	defer rec.Close()
	st, err := store.Open(*db)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	clock := func() float64 { return float64(time.Now().UnixNano()) / 1e9 }
	col, err := samples.NewCollector(*samplesDir, samples.MaxBytes, rand.Float64, clock)
	if err != nil {
		log.Fatal(err)
	}
	cam := capture.Camera{URL: rtsp, Timeout: 20 * time.Second}
	srv, err := server.New(cam, rec, plug, st, col, server.Options{DryRun: *dryRun, SamplesDir: *samplesDir, ShadowDir: *shadowDir})
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go srv.Loop(ctx)
	hs := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hs.Shutdown(shut)
	}()
	log.Printf("awh listening on %s (dry-run=%v)", *addr, *dryRun)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
