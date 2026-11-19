package tapo

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

type fakeDevice struct {
	auth          []byte
	local, remote []byte
	on            bool
	calls         []string
}

func (d *fakeDevice) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	switch r.URL.Path {
	case "/app/handshake1":
		d.local, d.remote = body, bytes.Repeat([]byte{7}, 16)
		http.SetCookie(w, &http.Cookie{Name: "TP_SESSIONID", Value: "abc"})
		h := sha256.Sum256(cat(d.local, d.remote, d.auth))
		w.Write(cat(d.remote, h[:]))
	case "/app/handshake2":
		want := sha256.Sum256(cat(d.remote, d.local, d.auth))
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
