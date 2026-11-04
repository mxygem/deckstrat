// Package httpcache is a small on-disk cache for GET responses, for APIs
// like TCGdex that send no-cache headers but serve slow-changing data.
package httpcache

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"time"
)

// Transport caches successful GET responses on disk for TTL.
// Other methods and non-200 responses pass through uncached.
type Transport struct {
	Dir  string
	TTL  time.Duration
	Base http.RoundTripper // nil means http.DefaultTransport

	now func() time.Time // for tests
}

func New(dir string, ttl time.Duration) *Transport {
	return &Transport{Dir: dir, TTL: ttl}
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		return t.base().RoundTrip(req)
	}
	path := t.path(req)
	if resp := t.read(path, req); resp != nil {
		return resp, nil
	}

	resp, err := t.base().RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	raw, err := httputil.DumpResponse(resp, true)
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	// Caching is best effort; a write failure must not fail the request.
	if err := os.MkdirAll(t.Dir, 0o755); err == nil {
		tmp := path + ".tmp"
		if os.WriteFile(tmp, raw, 0o644) == nil {
			_ = os.Rename(tmp, path)
		}
	}
	return http.ReadResponse(bufio.NewReader(bytes.NewReader(raw)), req)
}

func (t *Transport) read(path string, req *http.Request) *http.Response {
	info, err := os.Stat(path)
	if err != nil || t.clock().Sub(info.ModTime()) > t.TTL {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(raw)), req)
	if err != nil {
		return nil
	}
	return resp
}

func (t *Transport) path(req *http.Request) string {
	sum := sha256.Sum256([]byte(req.URL.String()))
	return filepath.Join(t.Dir, hex.EncodeToString(sum[:]))
}

func (t *Transport) base() http.RoundTripper {
	if t.Base != nil {
		return t.Base
	}
	return http.DefaultTransport
}

func (t *Transport) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}
