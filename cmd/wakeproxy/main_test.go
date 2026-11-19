package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMain doubles as the fake server: with WAKEPROXY_FAKE set, the test
// binary serves on HOST:PORT instead of running tests.
func TestMain(m *testing.M) {
	if os.Getenv("WAKEPROXY_FAKE") == "" {
		os.Exit(m.Run())
	}
	if d, err := time.ParseDuration(os.Getenv("WAKEPROXY_FAKE_DELAY")); err == nil {
		time.Sleep(d) // a slow boot, like Python importing cv2
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/pid", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, os.Getpid())
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(600 * time.Millisecond)
		fmt.Fprint(w, os.Getpid())
	})
	mux.HandleFunc("/crash", func(w http.ResponseWriter, r *http.Request) {
		os.Exit(3)
	})
	addr := net.JoinHostPort(os.Getenv("HOST"), os.Getenv("PORT"))
	fmt.Fprintln(os.Stderr, http.ListenAndServe(addr, mux))
	os.Exit(1)
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

type harness struct {
	t   *testing.T
	b   *backend
	url string
}

func setup(t *testing.T, idle time.Duration, argv []string, extraEnv ...string) *harness {
	t.Helper()
	b, err := newBackend(argv, freeAddr(t), 5*time.Second, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	b.env = append(b.env, extraEnv...)
	p := newProxy(b, idle)
	ctx, cancel := context.WithCancel(context.Background())
	go p.watchIdle(ctx)
	srv := httptest.NewServer(p)
	t.Cleanup(func() {
		srv.Close()
		cancel()
		b.stopNow()
	})
	return &harness{t: t, b: b, url: srv.URL}
}

func fakeServer(t *testing.T, idle time.Duration, extraEnv ...string) *harness {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return setup(t, idle, []string{exe}, append(extraEnv, "WAKEPROXY_FAKE=1")...)
}

func (h *harness) get(path string) (int, string) {
	h.t.Helper()
	resp, err := http.Get(h.url + path)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(body))
}

func (h *harness) mustPid(path string) string {
	h.t.Helper()
	code, body := h.get(path)
	if code != 200 {
		h.t.Fatalf("GET %s: %d %s", path, code, body)
	}
	return body
}

func (h *harness) awake() bool {
	h.b.mu.Lock()
	defer h.b.mu.Unlock()
	return h.b.cur != nil
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSleepsUntilFirstRequest(t *testing.T) {
	h := fakeServer(t, time.Minute, "WAKEPROXY_FAKE_DELAY=300ms")
	time.Sleep(200 * time.Millisecond)
	if h.awake() || h.b.accepting() {
		t.Fatal("server started before any request")
	}
	first := h.mustPid("/pid") // waits through the 300ms boot
	if second := h.mustPid("/pid"); second != first {
		t.Fatalf("second request hit a new server: %s then %s", first, second)
	}
}

func TestConcurrentWakeStartsOneServer(t *testing.T) {
	h := fakeServer(t, time.Minute, "WAKEPROXY_FAKE_DELAY=300ms")
	pids := make(chan string, 8)
	for range 8 {
		go func() {
			resp, err := http.Get(h.url + "/pid")
			if err != nil {
				pids <- "err: " + err.Error()
				return
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			pids <- string(b)
		}()
	}
	first := <-pids
	for range 7 {
		if p := <-pids; p != first {
			t.Fatalf("got %q and %q: more than one server", first, p)
		}
	}
}

func TestSleepsAfterIdleAndWakesAgain(t *testing.T) {
	h := fakeServer(t, 300*time.Millisecond)
	first := h.mustPid("/pid")
	waitFor(t, "server to sleep", 3*time.Second, func() bool { return !h.awake() && !h.b.accepting() })
	if second := h.mustPid("/pid"); second == first {
		t.Fatal("expected a fresh server after sleeping")
	}
}

func TestSteadyRequestsKeepItAwake(t *testing.T) {
	h := fakeServer(t, 300*time.Millisecond)
	first := h.mustPid("/pid")
	for range 10 { // 1s of traffic, well past the 300ms idle limit
		time.Sleep(100 * time.Millisecond)
		if p := h.mustPid("/pid"); p != first {
			t.Fatalf("server restarted during steady traffic: %s then %s", first, p)
		}
	}
}

func TestInflightRequestKeepsItAwake(t *testing.T) {
	h := fakeServer(t, 200*time.Millisecond)
	first := h.mustPid("/pid")
	// /slow takes 600ms, three idle limits; being killed mid-way would 502.
	if p := h.mustPid("/slow"); p != first {
		t.Fatalf("slow request ran on a new server: %s then %s", first, p)
	}
}

func TestRestartsAfterCrash(t *testing.T) {
	h := fakeServer(t, time.Minute)
	first := h.mustPid("/pid")
	http.Get(h.url + "/crash")
	waitFor(t, "crash to be noticed", 3*time.Second, func() bool { return !h.awake() })
	if second := h.mustPid("/pid"); second == first {
		t.Fatal("expected a new server after the crash")
	}
}

func TestStartFailureReturns503AndRetries(t *testing.T) {
	h := setup(t, time.Minute, []string{"/bin/sh", "-c", "exit 1"})
	for range 2 { // the second request must try again, not stay stuck
		if code, body := h.get("/pid"); code != http.StatusServiceUnavailable {
			t.Fatalf("got %d %s, want 503", code, body)
		}
	}
}

func TestStopsChildrenOfWrapperScripts(t *testing.T) {
	// Like ./serve: a shell that starts the real server as a child and does
	// not exec it. Sleeping must kill the child too, or it keeps the port.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h := setup(t, 300*time.Millisecond, []string{"/bin/sh", "-c", "\"$0\"; echo done", exe}, "WAKEPROXY_FAKE=1")
	first := h.mustPid("/pid")
	waitFor(t, "server to sleep", 3*time.Second, func() bool { return !h.awake() && !h.b.accepting() })
	if second := h.mustPid("/pid"); second == first {
		t.Fatal("expected a fresh server after sleeping")
	}
}
