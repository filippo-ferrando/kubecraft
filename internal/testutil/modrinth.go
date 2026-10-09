package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// FakeModrinth serves a tiny catalog: "luckperms" (plugin) and "lithium" (fabric mod).
// Every search query is sent to queries.
func FakeModrinth(t *testing.T, queries chan<- string) string {
	t.Helper()
	projects := map[string]map[string]any{
		"luckperms":  {"id": "Vebnzrzj", "slug": "luckperms", "title": "LuckPerms", "server_side": "required", "loaders": []string{"bukkit", "paper", "fabric"}},
		"lithium":    {"id": "gvQqBUqZ", "slug": "lithium", "title": "Lithium", "server_side": "optional", "loaders": []string{"fabric", "neoforge"}},
		"clientonly": {"id": "c1", "slug": "clientonly", "title": "Client Only", "server_side": "unsupported", "loaders": []string{"fabric"}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/search":
			if queries != nil {
				select {
				case queries <- r.URL.RawQuery:
				default:
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"total_hits": 1, "offset": 0, "hits": []map[string]any{
				{"project_id": "Vebnzrzj", "slug": "luckperms", "title": "LuckPerms", "server_side": "required", "categories": []string{"paper"}},
			}})
		case "/projects":
			var ids []string
			_ = json.Unmarshal([]byte(r.URL.Query().Get("ids")), &ids)
			out := []map[string]any{}
			for _, id := range ids {
				for _, p := range projects {
					if p["slug"] == id || p["id"] == id {
						out = append(out, p)
					}
				}
			}
			_ = json.NewEncoder(w).Encode(out)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return strings.TrimSuffix(srv.URL, "/")
}
