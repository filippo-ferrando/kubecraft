package api

import (
	"net/http/httptest"
	"testing"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/config"
)

func TestClientIP(t *testing.T) {
	cases := []struct {
		name   string
		header string
		peer   string
		set    map[string]string
		want   string
	}{
		{"public peer ignores headers", "Cf-Connecting-Ip", "203.0.113.9:1234",
			map[string]string{"Cf-Connecting-Ip": "198.51.100.1", "X-Forwarded-For": "198.51.100.2"}, "203.0.113.9"},
		{"private peer uses configured header", "Cf-Connecting-Ip", "10.42.0.7:1234",
			map[string]string{"Cf-Connecting-Ip": "198.51.100.1", "X-Forwarded-For": "10.42.0.3"}, "198.51.100.1"},
		{"invalid header falls back to forwarded for", "Cf-Connecting-Ip", "10.42.0.7:1234",
			map[string]string{"Cf-Connecting-Ip": "nope", "X-Forwarded-For": "1.1.1.1, 198.51.100.2"}, "198.51.100.2"},
		{"header unused when not configured", "", "10.42.0.7:1234",
			map[string]string{"Cf-Connecting-Ip": "198.51.100.1"}, "10.42.0.7"},
		{"private peer without headers", "Cf-Connecting-Ip", "10.42.0.7:1234", nil, "10.42.0.7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{cfg: &config.Config{ClientIPHeader: tc.header}}
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.peer
			for k, v := range tc.set {
				r.Header.Set(k, v)
			}
			if got := s.clientIP(r); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
