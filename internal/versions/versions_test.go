package versions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const manifest = `{"latest":{"release":"1.21.4","snapshot":"25w01a"},"versions":[
{"id":"25w01a","type":"snapshot","releaseTime":"2025-01-01T00:00:00Z"},
{"id":"1.21.4","type":"release","releaseTime":"2024-12-03T00:00:00Z"},
{"id":"1.21.3","type":"release","releaseTime":"2024-10-23T00:00:00Z"}]}`

func TestCache(t *testing.T) {
	var hits atomic.Int32
	fail := atomic.Bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(manifest))
	}))
	defer srv.Close()

	c := NewCache(srv.URL, time.Hour)
	l, err := c.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if l.Latest != "1.21.4" || len(l.Versions) != 2 || l.Versions[0].ID != "1.21.4" {
		t.Fatalf("unexpected list: %+v", l)
	}
	if _, err := c.Get(context.Background()); err != nil || hits.Load() != 1 {
		t.Fatal("second call should hit the cache")
	}

	// Stale data is served when the upstream fails.
	c.ttl = 0
	fail.Store(true)
	if l, err := c.Get(context.Background()); err != nil || l.Latest != "1.21.4" {
		t.Fatalf("expected stale data, got %v %v", l, err)
	}
	if _, err := NewCache(srv.URL, time.Hour).Get(context.Background()); err == nil {
		t.Fatal("expected error without cached data")
	}
}
