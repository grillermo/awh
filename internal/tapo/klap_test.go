package tapo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeDevice struct {
	auth          []byte
	local, remote []byte
	v1            bool
	on            bool
	calls         []string
	// offFault, when set, runs after a set_device_info(false) was applied
	// instead of answering, simulating a lost response.
	offFault func(w http.ResponseWriter, r *http.Request)
}

func (d *fakeDevice) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	switch r.URL.Path {
	case "/app/handshake1":
		d.local, d.remote = body, bytes.Repeat([]byte{7}, 16)
		http.SetCookie(w, &http.Cookie{Name: "TP_SESSIONID", Value: "abc"})
		h := sha256.Sum256(cat(d.local, d.remote, d.auth))
		if d.v1 {
			h = sha256.Sum256(cat(d.local, d.auth))
		}
		w.Write(cat(d.remote, h[:]))
	case "/app/handshake2":
		want := sha256.Sum256(cat(d.remote, d.local, d.auth))
		if d.v1 {
			want = sha256.Sum256(cat(d.remote, d.auth))
		}
		if !bytes.Equal(body, want[:]) {
			w.WriteHeader(403)
		}
	case "/app/request":
		seq, _ := strconv.Atoi(r.URL.Query().Get("seq"))
		c := newCipher(d.local, d.remote, d.auth)
		var seqb [4]byte
		binary.BigEndian.PutUint32(seqb[:], uint32(int32(seq)))
		plain, err := c.decrypt(body[32:], int32(seq))
		sig := sha256.Sum256(cat(c.sig, seqb[:], body[32:]))
		if err != nil || !bytes.Equal(sig[:], body[:32]) {
			w.WriteHeader(400)
			return
		}
		var req struct {
			Method string `json:"method"`
			Params struct {
				DeviceOn bool `json:"device_on"`
			} `json:"params"`
		}
		json.Unmarshal(plain, &req)
		d.calls = append(d.calls, req.Method+":"+strconv.FormatBool(req.Params.DeviceOn))
		if req.Method == "set_device_info" {
			d.on = req.Params.DeviceOn
		}
		if req.Method == "set_device_info" && !req.Params.DeviceOn && d.offFault != nil {
			d.offFault(w, r)
			return
		}
		resp, _ := json.Marshal(map[string]any{"error_code": 0, "result": map[string]any{"device_on": d.on, "model": "P100"}})
		ct := c.encrypt(resp, int32(seq))
		rsig := sha256.Sum256(cat(c.sig, seqb[:], ct))
		w.Write(cat(rsig[:], ct))
	}
}

func TestHandshakeInfoAndPowerCycle(t *testing.T) {
	dev := &fakeDevice{auth: authHash("me@example.com", "pw"), on: true}
	srv := httptest.NewServer(dev)
	defer srv.Close()
	c := &Client{Base: srv.URL, Email: "me@example.com", Password: "pw", Wait: 0}

	info, err := c.Info(t.Context())
	if err != nil || info["model"] != "P100" {
		t.Fatalf("info %v %v", info, err)
	}
	if err := c.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(dev.calls, ",")
	if got != "get_device_info:false,set_device_info:false,set_device_info:true" || !dev.on {
		t.Fatalf("calls %s, on=%v", got, dev.on)
	}
}

func TestWrongPasswordFailsTheHandshake(t *testing.T) {
	dev := &fakeDevice{auth: authHash("me@example.com", "pw")}
	srv := httptest.NewServer(dev)
	defer srv.Close()
	c := &Client{Base: srv.URL, Email: "me@example.com", Password: "nope"}
	if _, err := c.Info(t.Context()); err == nil {
		t.Fatal("want an auth error")
	}
}

func TestKlapV1Handshake(t *testing.T) {
	dev := &fakeDevice{auth: authHashV1("me@example.com", "pw"), v1: true, on: true}
	srv := httptest.NewServer(dev)
	defer srv.Close()
	c := &Client{Base: srv.URL, Email: "me@example.com", Password: "pw"}
	if info, err := c.Info(t.Context()); err != nil || info["model"] != "P100" {
		t.Fatalf("info %v %v", info, err)
	}
}

func TestEmptyCredentialsAreReportedAsSuch(t *testing.T) {
	c := &Client{Base: "http://127.0.0.1:1"}
	_, err := c.Info(t.Context())
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("got %v", err)
	}
}

func TestOffCallFailingAfterDeliveryStillTurnsOn(t *testing.T) {
	for name, tc := range map[string]struct {
		fault func(w http.ResponseWriter, r *http.Request)
		ctx   func() (context.Context, context.CancelFunc)
	}{
		"500 after applying": {
			fault: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) },
			ctx:   func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
		},
		"timeout after applying": {
			fault: func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() },
			ctx: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 300*time.Millisecond)
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			dev := &fakeDevice{auth: authHash("me@example.com", "pw"), on: true, offFault: tc.fault}
			srv := httptest.NewServer(dev)
			defer srv.Close()
			c := &Client{Base: srv.URL, Email: "me@example.com", Password: "pw", Wait: 0}
			ctx, cancel := tc.ctx()
			defer cancel()
			err := c.Reset(ctx)
			if err == nil || !strings.Contains(err.Error(), "turn off") {
				t.Fatalf("want a turn-off error, got %v", err)
			}
			got := strings.Join(dev.calls, ",")
			if got != "set_device_info:false,set_device_info:true" || !dev.on {
				t.Fatalf("calls %s, on=%v", got, dev.on)
			}
		})
	}
}

func TestHandshakeFailureSendsNothing(t *testing.T) {
	dev := &fakeDevice{auth: authHash("me@example.com", "pw"), on: true}
	srv := httptest.NewServer(dev)
	defer srv.Close()
	c := &Client{Base: srv.URL, Email: "me@example.com", Password: "wrong"}
	if err := c.Reset(t.Context()); err == nil || len(dev.calls) != 0 {
		t.Fatalf("err %v calls %v", err, dev.calls)
	}
}

func TestOnFailureIsReported(t *testing.T) {
	dev := &fakeDevice{auth: authHash("me@example.com", "pw"), on: true}
	srv := httptest.NewServer(dev)
	srv.Close() // everything after the handshake fails
	c := &Client{Base: srv.URL, Email: "me@example.com", Password: "pw"}
	if err := c.Reset(t.Context()); err == nil {
		t.Fatal("want an error")
	}
}
