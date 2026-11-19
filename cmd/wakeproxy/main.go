// wakeproxy keeps a heavy HTTP server asleep until someone uses it.
//
// It listens on the public address and proxies to the real server on a
// private one. The real server is not running until the first request comes
// in; that request waits while it starts. Every request start and end counts
// as activity, and a request still in flight keeps the server awake. Once
// there has been no activity for -idle, the server's whole process group gets
// SIGTERM (SIGKILL after -stop-grace) and its memory goes back to the system.
// The next request wakes it again.
//
//	wakeproxy -listen 0.0.0.0:3060 -backend 127.0.0.1:3070 -idle 30m -- ./serve
//
// The command gets HOST and PORT in its environment, taken from -backend.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

func main() {
	listen := flag.String("listen", "0.0.0.0:3060", "public address to listen on")
	backendAddr := flag.String("backend", "127.0.0.1:3070", "private address the server is started on (passed as HOST and PORT)")
	idle := flag.Duration("idle", 30*time.Minute, "stop the server after this long without requests")
	startTimeout := flag.Duration("start-timeout", 2*time.Minute, "give up if the server does not accept connections within this long")
	stopGrace := flag.Duration("stop-grace", 10*time.Second, "time between SIGTERM and SIGKILL when stopping the server")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [flags] -- command [args...]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(64)
	}
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("[wakeproxy] ")

	b, err := newBackend(flag.Args(), *backendAddr, *startTimeout, *stopGrace)
	if err != nil {
		log.Fatal(err)
	}
	p := newProxy(b, *idle)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{Addr: *listen, Handler: p}
	go p.watchIdle(ctx)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on %s, server sleeps until the first request (idle limit %s)", *listen, *idle)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	b.stopNow()
}

// proxy forwards every request to the backend, waking it first if needed,
// and puts it to sleep once requests stop.
type proxy struct {
	b        *backend
	rp       *httputil.ReverseProxy
	idle     time.Duration
	inflight atomic.Int64
	lastSeen atomic.Int64 // unix nanoseconds of the last request start or end
}

func newProxy(b *backend, idle time.Duration) *proxy {
	target := &url.URL{Scheme: "http", Host: b.addr}
	p := &proxy{b: b, idle: idle}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
		},
		FlushInterval: -1, // stream responses as they come
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("proxy error on %s %s: %v", r.Method, r.URL.Path, err)
			http.Error(w, "backend unavailable", http.StatusBadGateway)
		},
	}
	p.touch()
	return p
}

func (p *proxy) touch() { p.lastSeen.Store(time.Now().UnixNano()) }

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// inflight goes up before ensure, so the idle watcher can never stop the
	// server between this request waking it and being proxied.
	p.inflight.Add(1)
	p.touch()
	defer func() {
		p.touch()
		p.inflight.Add(-1)
	}()

	if err := p.b.ensure(r.Context(), r.Method+" "+r.URL.Path); err != nil {
		if r.Context().Err() == nil {
			http.Error(w, "server failed to start: "+err.Error(), http.StatusServiceUnavailable)
		}
		return
	}
	p.rp.ServeHTTP(w, r)
}

// watchIdle stops the backend once nothing has come in for p.idle.
func (p *proxy) watchIdle(ctx context.Context) {
	every := min(max(p.idle/20, 50*time.Millisecond), 30*time.Second)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.b.stopIf(func() bool {
				return p.inflight.Load() == 0 &&
					time.Since(time.Unix(0, p.lastSeen.Load())) >= p.idle
			}, fmt.Sprintf("no requests for %s", p.idle))
		}
	}
}

// backend owns the server process. At most one run is current; a new run
// waits for the previous one to exit so they never fight over the port.
type backend struct {
	argv         []string
	addr         string
	env          []string
	startTimeout time.Duration
	stopGrace    time.Duration

	mu   sync.Mutex
	cur  *run // nil while asleep
	prev *run // last run that was stopped, possibly still exiting
}

type run struct {
	cmd     *exec.Cmd
	ready   chan struct{} // closed once err is final: nil means accepting connections
	err     error
	exited  chan struct{}
	started time.Time
}

func newBackend(argv []string, addr string, startTimeout, stopGrace time.Duration) (*backend, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("bad -backend %q: %w", addr, err)
	}
	env := append(os.Environ(), "HOST="+host, "PORT="+port)
	return &backend{argv: argv, addr: addr, env: env, startTimeout: startTimeout, stopGrace: stopGrace}, nil
}

// ensure returns once the backend accepts connections, starting it if asleep.
func (b *backend) ensure(ctx context.Context, reason string) error {
	b.mu.Lock()
	if b.cur == nil {
		log.Printf("waking server (request %s)", reason)
		b.cur = b.start(b.prev)
	}
	r := b.cur
	b.mu.Unlock()

	select {
	case <-r.ready:
		return r.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *backend) start(prev *run) *run {
	r := &run{ready: make(chan struct{}), exited: make(chan struct{})}
	go func() {
		if prev != nil {
			<-prev.exited
		}
		if b.accepting() {
			b.fail(r, fmt.Errorf("something else is already listening on %s", b.addr))
			return
		}

		cmd := exec.Command(b.argv[0], b.argv[1:]...)
		cmd.Env = b.env
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // so stop reaches its children too
		r.cmd = cmd
		r.started = time.Now()
		if err := cmd.Start(); err != nil {
			b.fail(r, err)
			return
		}
		go func() {
			err := cmd.Wait()
			close(r.exited)
			b.mu.Lock()
			wasCurrent := b.cur == r
			if wasCurrent {
				b.cur = nil
			}
			b.mu.Unlock()
			if wasCurrent {
				log.Printf("server exited on its own after %s: %v", time.Since(r.started).Round(time.Second), err)
			}
		}()

		deadline := time.After(b.startTimeout)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for !b.accepting() {
			select {
			case <-r.exited:
				r.err = errors.New("server exited before accepting connections")
				close(r.ready)
				return
			case <-deadline:
				r.err = fmt.Errorf("server did not accept connections on %s within %s", b.addr, b.startTimeout)
				close(r.ready)
				b.mu.Lock()
				if b.cur == r {
					b.cur, b.prev = nil, r
				}
				b.mu.Unlock()
				b.kill(r)
				return
			case <-tick.C:
			}
		}
		log.Printf("server ready in %s (pid %d)", time.Since(r.started).Round(10*time.Millisecond), cmd.Process.Pid)
		close(r.ready)
	}()
	return r
}

// fail ends a run that never got a process.
func (b *backend) fail(r *run, err error) {
	log.Printf("cannot start server: %v", err)
	r.err = err
	close(r.ready)
	close(r.exited)
	b.mu.Lock()
	if b.cur == r {
		b.cur = nil
	}
	b.mu.Unlock()
}

func (b *backend) accepting() bool {
	c, err := net.DialTimeout("tcp", b.addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// stopIf puts the backend to sleep if it is up and cond holds. cond runs under
// the lock, so no request can wake the current run in between.
func (b *backend) stopIf(cond func() bool, why string) {
	b.mu.Lock()
	r := b.cur
	if r == nil || !isClosed(r.ready) || r.err != nil || !cond() {
		b.mu.Unlock()
		return
	}
	b.cur, b.prev = nil, r
	b.mu.Unlock()

	log.Printf("%s, putting server to sleep (pid %d, up %s)", why, r.cmd.Process.Pid, time.Since(r.started).Round(time.Second))
	b.kill(r)
}

// stopNow stops whatever is running, used when wakeproxy itself exits.
func (b *backend) stopNow() {
	b.mu.Lock()
	r := b.cur
	b.cur = nil
	b.mu.Unlock()
	if r != nil {
		<-r.ready
		if r.cmd != nil && r.cmd.Process != nil {
			b.kill(r)
		}
	}
}

// kill sends SIGTERM to the run's process group, then SIGKILL after the grace.
func (b *backend) kill(r *run) {
	pgid := r.cmd.Process.Pid
	syscall.Kill(-pgid, syscall.SIGTERM)
	select {
	case <-r.exited:
	case <-time.After(b.stopGrace):
		log.Printf("server ignored SIGTERM for %s, sending SIGKILL", b.stopGrace)
		syscall.Kill(-pgid, syscall.SIGKILL)
		<-r.exited
	}
	// The leader may exit before its children; sweep the group once more.
	syscall.Kill(-pgid, syscall.SIGKILL)
}

func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
