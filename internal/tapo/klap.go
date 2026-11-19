// Package tapo power-cycles a Tapo P100 plug over its local KLAP protocol,
// replacing reset_tapo100() and the Python tapo package.
package tapo

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"
)

type Client struct {
	Base            string // "http://192.168.1.86"
	Email, Password string
	Wait            time.Duration // off -> on gap; 10 s in production
	HTTP            *http.Client
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func authHash(email, password string) []byte {
	e, p := sha1.Sum([]byte(email)), sha1.Sum([]byte(password))
	h := sha256.Sum256(cat(e[:], p[:]))
	return h[:]
}

// authHashV1 is the older KLAP seed, md5(md5(email) + md5(password)), which
// some firmware still uses.
func authHashV1(email, password string) []byte {
	e, p := md5.Sum([]byte(email)), md5.Sum([]byte(password))
	h := md5.Sum(cat(e[:], p[:]))
	return h[:]
}

type klapCipher struct {
	key, iv, sig []byte
	seq          int32
}

func newCipher(local, remote, auth []byte) *klapCipher {
	k := sha256.Sum256(cat([]byte("lsk"), local, remote, auth))
	iv := sha256.Sum256(cat([]byte("iv"), local, remote, auth))
	sig := sha256.Sum256(cat([]byte("ldk"), local, remote, auth))
	return &klapCipher{key: k[:16], iv: iv[:12], sig: sig[:28], seq: int32(binary.BigEndian.Uint32(iv[28:]))}
}

func (c *klapCipher) ivFor(seq int32) []byte {
	out := make([]byte, 16)
	copy(out, c.iv)
	binary.BigEndian.PutUint32(out[12:], uint32(seq))
	return out
}

func (c *klapCipher) encrypt(plain []byte, seq int32) []byte {
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	data := append(append([]byte{}, plain...), bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, _ := aes.NewCipher(c.key)
	cipher.NewCBCEncrypter(block, c.ivFor(seq)).CryptBlocks(data, data)
	return data
}

func (c *klapCipher) decrypt(data []byte, seq int32) ([]byte, error) {
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, errors.New("tapo: bad ciphertext length")
	}
	out := append([]byte{}, data...)
	block, _ := aes.NewCipher(c.key)
	cipher.NewCBCDecrypter(block, c.ivFor(seq)).CryptBlocks(out, out)
	pad := int(out[len(out)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(out) {
		return nil, errors.New("tapo: bad padding")
	}
	return out[:len(out)-pad], nil
}

type session struct {
	c      *Client
	cookie string
	ci     *klapCipher
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (c *Client) post(ctx context.Context, path, cookie string, body []byte) ([]byte, *http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+path, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err == nil && resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("tapo: %s returned %s", path, resp.Status)
	}
	return b, resp, err
}

func (c *Client) handshake(ctx context.Context) (*session, error) {
	if c.Email == "" || c.Password == "" {
		return nil, errors.New("tapo: email or password is empty (check TAPO_EMAIL / TAPO_PASSWORD)")
	}
	local := make([]byte, 16)
	rand.Read(local)
	body, resp, err := c.post(ctx, "/app/handshake1", "", local)
	if err != nil {
		return nil, err
	}
	if len(body) < 48 {
		return nil, fmt.Errorf("tapo: handshake1 answered %d bytes", len(body))
	}
	remote := body[:16]
	// KLAP v2 (newer firmware): sha256(local + remote + auth).
	// KLAP v1: sha256(local + auth) and handshake2 = sha256(remote + auth).
	var auth []byte
	v1 := false
	if want := sha256.Sum256(cat(local, remote, authHash(c.Email, c.Password))); bytes.Equal(body[16:48], want[:]) {
		auth = authHash(c.Email, c.Password)
	} else if want := sha256.Sum256(cat(local, authHashV1(c.Email, c.Password))); bytes.Equal(body[16:48], want[:]) {
		auth, v1 = authHashV1(c.Email, c.Password), true
	}
	if auth == nil {
		return nil, errors.New("tapo: handshake1 hash mismatch (wrong email or password?)")
	}
	cookie := ""
	for _, ck := range resp.Cookies() {
		if ck.Name == "TP_SESSIONID" {
			cookie = "TP_SESSIONID=" + ck.Value
		}
	}
	h2 := sha256.Sum256(cat(remote, local, auth))
	if v1 {
		h2 = sha256.Sum256(cat(remote, auth))
	}
	if _, _, err := c.post(ctx, "/app/handshake2", cookie, h2[:]); err != nil {
		return nil, err
	}
	return &session{c: c, cookie: cookie, ci: newCipher(local, remote, auth)}, nil
}

func (s *session) call(ctx context.Context, payload any) (map[string]any, error) {
	plain, _ := json.Marshal(payload)
	s.ci.seq++
	seq := s.ci.seq
	ct := s.ci.encrypt(plain, seq)
	var seqb [4]byte
	binary.BigEndian.PutUint32(seqb[:], uint32(seq))
	sig := sha256.Sum256(cat(s.ci.sig, seqb[:], ct))
	body, _, err := s.c.post(ctx, "/app/request?seq="+strconv.Itoa(int(seq)), s.cookie, cat(sig[:], ct))
	if err != nil {
		return nil, err
	}
	if len(body) < 32 {
		return nil, errors.New("tapo: short response")
	}
	dec, err := s.ci.decrypt(body[32:], seq)
	if err != nil {
		return nil, err
	}
	var out struct {
		ErrorCode int            `json:"error_code"`
		Result    map[string]any `json:"result"`
	}
	if err := json.Unmarshal(dec, &out); err != nil {
		return nil, err
	}
	if out.ErrorCode != 0 {
		return nil, fmt.Errorf("tapo: error_code %d", out.ErrorCode)
	}
	return out.Result, nil
}

func setOn(on bool) map[string]any {
	return map[string]any{"method": "set_device_info", "params": map[string]any{"device_on": on}}
}

// Info is read-only: it never changes the plug.
func (c *Client) Info(ctx context.Context) (map[string]any, error) {
	s, err := c.handshake(ctx)
	if err != nil {
		return nil, err
	}
	return s.call(ctx, map[string]any{"method": "get_device_info"})
}

// Reset turns the plug off, waits, and turns it back on, like reset_tapo100().
func (c *Client) Reset(ctx context.Context) error {
	s, err := c.handshake(ctx)
	if err != nil {
		return err
	}
	log.Printf("[tapo] Turning off %s", c.Base)
	if _, err := s.call(ctx, setOn(false)); err != nil {
		return err
	}
	log.Printf("[tapo] Waiting %s", c.Wait)
	select {
	case <-time.After(c.Wait):
	case <-ctx.Done():
	}
	// Always try to turn it back on, even if ctx was cancelled during the wait.
	onCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	log.Printf("[tapo] Turning on %s", c.Base)
	if _, err := s.call(onCtx, setOn(true)); err != nil {
		// The session may have expired: retry once with a fresh handshake.
		s2, err2 := c.handshake(onCtx)
		if err2 != nil {
			return fmt.Errorf("turn on: %v; re-handshake: %w", err, err2)
		}
		if _, err := s2.call(onCtx, setOn(true)); err != nil {
			return err
		}
	}
	log.Printf("[tapo] Reset complete")
	return nil
}
