package api

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spaHandler serves built assets and falls back to index.html for client routes.
func spaHandler(ui fs.FS) http.Handler {
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(ui, p); errors.Is(err, fs.ErrNotExist) {
			if path.Ext(p) != "" {
				http.NotFound(w, r)
				return
			}
			serveIndex(w, ui)
			return
		}
		if strings.HasPrefix(p, "assets/") {
			// Vite puts a content hash in asset names.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, ui fs.FS) {
	b, err := fs.ReadFile(ui, "index.html")
	if err != nil {
		http.Error(w, "UI not built, run make web", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}
