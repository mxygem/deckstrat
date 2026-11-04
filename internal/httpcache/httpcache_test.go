package httpcache

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestTransport(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, "body:"+r.URL.Path)
	}))
	defer srv.Close()

	now := time.Now()
	tr := New(t.TempDir(), time.Hour)
	tr.now = func() time.Time { return now }
	client := &http.Client{Transport: tr}

	get := func(path string) (int, string) {
		t.Helper()
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	if _, body := get("/a"); body != "body:/a" {
		t.Fatalf("first body = %q", body)
	}
	if _, body := get("/a"); body != "body:/a" {
		t.Fatalf("cached body = %q", body)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("hits after cached read = %d, want 1", n)
	}

	now = now.Add(2 * time.Hour)
	get("/a")
	if n := hits.Load(); n != 2 {
		t.Errorf("hits after expiry = %d, want 2", n)
	}

	get("/missing")
	if code, _ := get("/missing"); code != http.StatusNotFound {
		t.Errorf("code = %d", code)
	}
	if n := hits.Load(); n != 4 {
		t.Errorf("404s should not be cached; hits = %d, want 4", n)
	}
}
