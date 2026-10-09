// Package files is the small file service that runs next to each server and gives
// the manager world backups and config file editing. All access is confined to the
// data directory with os.Root, so paths cannot escape it (also through symlinks).
package files

import (
	"archive/tar"
	"compress/gzip"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxEditSize = 1 << 20
	// Port the sidecar listens on.
	Port = 8081
)

// Extensions of text files that can be edited.
var editable = []string{".yml", ".yaml", ".json", ".json5", ".toml", ".properties", ".conf", ".cfg", ".txt", ".ini", ".snbt", ".mcmeta", ".csv", ".lang"}

// Skipped in full backups: downloaded again by the image on start.
var fullSkip = []string{"libraries", "versions", "cache", ".cache", "logs", "crash-reports", "bundler"}

func Editable(name string) bool {
	return slices.Contains(editable, strings.ToLower(path.Ext(name)))
}

// Handler serves the file API for root, authenticated with token.
type Handler struct {
	root  *os.Root
	token string
	mux   *http.ServeMux
}

func NewHandler(dir, token string) (*Handler, error) {
	if token == "" {
		return nil, errors.New("FILES_TOKEN is required")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	h := &Handler{root: root, token: token, mux: http.NewServeMux()}
	h.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h.mux.HandleFunc("GET /list", h.list)
	h.mux.HandleFunc("GET /read", h.read)
	h.mux.HandleFunc("PUT /write", h.write)
	h.mux.HandleFunc("GET /backup", h.backup)
	return h, nil
}

func (h *Handler) Close() error { return h.root.Close() }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/healthz" {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) != 1 {
			httpError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
	}
	h.mux.ServeHTTP(w, r)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// clean turns a user path into a root-relative one. os.Root still enforces the
// boundary; this only gives nicer errors and a canonical form.
func clean(p string) (string, bool) {
	p = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(p, "\\", "/")), "/")
	if p == "" {
		p = "."
	}
	if strings.ContainsRune(p, 0) {
		return "", false
	}
	return p, true
}

// Entry is one item in a directory listing.
type Entry struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Dir      bool      `json:"dir"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Editable bool      `json:"editable"`
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	p, ok := clean(r.URL.Query().Get("path"))
	if !ok {
		httpError(w, http.StatusBadRequest, "invalid path")
		return
	}
	entries, err := fs.ReadDir(h.root.FS(), p)
	if err != nil {
		fileError(w, err)
		return
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		// Symlinks are listed but not followed or edited.
		isLink := info.Mode()&fs.ModeSymlink != 0
		ep := path.Join(p, e.Name())
		out = append(out, Entry{
			Name: e.Name(), Path: ep, Dir: e.IsDir(), Size: info.Size(), Modified: info.ModTime(),
			Editable: !e.IsDir() && !isLink && Editable(e.Name()) && info.Size() <= MaxEditSize,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"path": p, "entries": out})
}

func fileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		httpError(w, http.StatusNotFound, "not found")
	case errors.Is(err, fs.ErrPermission):
		httpError(w, http.StatusForbidden, "permission denied")
	default:
		// os.Root reports escapes as a generic path error.
		httpError(w, http.StatusBadRequest, "invalid path")
	}
}

// checkEditable verifies p is an existing, regular, editable file.
func (h *Handler) checkEditable(p string) (fs.FileInfo, error) {
	if !Editable(p) {
		return nil, errors.New("this file type cannot be edited")
	}
	info, err := h.root.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if info.Size() > MaxEditSize {
		return nil, errors.New("file is too large to edit")
	}
	return info, nil
}

func (h *Handler) read(w http.ResponseWriter, r *http.Request) {
	p, ok := clean(r.URL.Query().Get("path"))
	if !ok {
		httpError(w, http.StatusBadRequest, "invalid path")
		return
	}
	info, err := h.checkEditable(p)
	if err != nil {
		editError(w, err)
		return
	}
	b, err := h.root.ReadFile(p)
	if err != nil {
		fileError(w, err)
		return
	}
	if !utf8.Valid(b) {
		httpError(w, http.StatusUnprocessableEntity, "file is not UTF-8 text")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"path": p, "content": string(b), "size": info.Size(), "modified": info.ModTime()})
}

func editError(w http.ResponseWriter, err error) {
	var pe *fs.PathError
	if errors.Is(err, fs.ErrNotExist) || errors.As(err, &pe) {
		fileError(w, err)
		return
	}
	httpError(w, http.StatusBadRequest, err.Error())
}

// write replaces an existing editable file atomically.
func (h *Handler) write(w http.ResponseWriter, r *http.Request) {
	p, ok := clean(r.URL.Query().Get("path"))
	if !ok {
		httpError(w, http.StatusBadRequest, "invalid path")
		return
	}
	info, err := h.checkEditable(p)
	if err != nil {
		editError(w, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxEditSize+1))
	if err != nil || len(body) > MaxEditSize {
		httpError(w, http.StatusRequestEntityTooLarge, "file is too large")
		return
	}
	if !utf8.Valid(body) {
		httpError(w, http.StatusBadRequest, "content must be UTF-8 text")
		return
	}
	tmp := path.Join(path.Dir(p), ".kubecraft-"+path.Base(p)+".tmp")
	if err := h.root.WriteFile(tmp, body, info.Mode().Perm()); err != nil {
		fileError(w, err)
		return
	}
	if err := h.root.Rename(tmp, p); err != nil {
		_ = h.root.Remove(tmp)
		fileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// worldDirs are top-level directories holding a level.dat.
func (h *Handler) worldDirs() ([]string, error) {
	entries, err := fs.ReadDir(h.root.FS(), ".")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := h.root.Stat(path.Join(e.Name(), "level.dat")); err == nil {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// backup streams a tar.gz of the worlds (scope=world) or of the whole server
// without downloadable caches (scope=full).
func (h *Handler) backup(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	var roots []string
	switch scope {
	case "", "world":
		dirs, err := h.worldDirs()
		if err != nil {
			fileError(w, err)
			return
		}
		if len(dirs) == 0 {
			httpError(w, http.StatusNotFound, "no world found yet, start the server once")
			return
		}
		// Player lists are small and useful when restoring.
		roots = append(dirs, "whitelist.json", "ops.json", "banned-players.json", "server.properties")
	case "full":
		roots = []string{"."}
	default:
		httpError(w, http.StatusBadRequest, "scope must be world or full")
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	fsys := h.root.FS()
	for _, top := range roots {
		if _, err := fs.Stat(fsys, top); err != nil {
			continue
		}
		err := fs.WalkDir(fsys, top, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if p == "." {
				return nil
			}
			if scope == "full" && !strings.Contains(p, "/") {
				if d.IsDir() && slices.Contains(fullSkip, p) {
					return fs.SkipDir
				}
				// The server jar is downloaded again on start.
				if !d.IsDir() && strings.HasSuffix(p, ".jar") {
					return nil
				}
			}
			if d.Name() == "session.lock" || strings.HasPrefix(d.Name(), ".kubecraft-") {
				return nil
			}
			return addTar(tw, fsys, p, d)
		})
		if err != nil {
			slog.Warn("backup aborted", "err", err)
			return
		}
	}
	if err := tw.Close(); err != nil {
		return
	}
	_ = gz.Close()
}

func addTar(tw *tar.Writer, fsys fs.FS, p string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return nil
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return nil // skip symlinks, sockets, devices
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = p
	if info.IsDir() {
		hdr.Name += "/"
	}
	hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", ""
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if info.IsDir() {
		return nil
	}
	f, err := fsys.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	// The file may change while copying; write exactly the header size.
	_, err = io.CopyN(tw, io.MultiReader(f, zeroReader{}), hdr.Size)
	return err
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// Serve runs the file service until the process ends.
func Serve(addr, dir, token string) error {
	h, err := NewHandler(dir, token)
	if err != nil {
		return err
	}
	defer h.Close()
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	slog.Info("file service listening", "addr", addr, "dir", dir)
	return srv.ListenAndServe()
}
