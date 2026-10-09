package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/db/sqlc"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/k8s"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/metrics"
)

// FilesAccess finds a server's file sidecar and its token.
type FilesAccess interface {
	FilesAddr(ctx context.Context, s k8s.ServerSpec, allowHelper bool) (string, error)
	FilesToken(ctx context.Context, slug string) (string, error)
}

// filesRequest calls the file sidecar of srv. Starting a helper pod for a stopped
// server can take up to a minute.
func (s *Server) filesRequest(ctx context.Context, srv sqlc.Server, method, path string, q url.Values, body io.Reader) (*http.Response, error) {
	plan, err := s.store.GetPlan(ctx, srv.PlanID)
	if err != nil {
		return nil, err
	}
	spec, err := s.mgr.Spec(srv, plan)
	if err != nil {
		return nil, err
	}
	actx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	addr, err := s.files.FilesAddr(actx, spec, true)
	if err != nil {
		return nil, err
	}
	token, err := s.files.FilesToken(ctx, srv.Slug)
	if err != nil {
		return nil, err
	}
	// addr is a pod IP from the API server, never user input; the sidecar confines paths.
	req, err := http.NewRequestWithContext(ctx, method, "http://"+addr+path+"?"+q.Encode(), body) //nolint:gosec
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return http.DefaultClient.Do(req) //nolint:gosec
}

func (s *Server) filesErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, k8s.ErrNoFiles) || errors.Is(err, context.DeadlineExceeded) {
		writeError(w, http.StatusConflict, "the server files are not reachable right now, try again when the server is running or stopped")
		return
	}
	internalError(w, r, err)
}

// relay copies a sidecar JSON response, keeping its status code.
func relay(w http.ResponseWriter, res *http.Response) {
	defer res.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(res.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(res.Body, 4<<20))
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	srv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	res, err := s.filesRequest(r.Context(), srv, "GET", "/list", url.Values{"path": {r.URL.Query().Get("path")}}, nil)
	if err != nil {
		s.filesErr(w, r, err)
		return
	}
	relay(w, res)
}

func (s *Server) readFile(w http.ResponseWriter, r *http.Request) {
	srv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	res, err := s.filesRequest(r.Context(), srv, "GET", "/read", url.Values{"path": {r.URL.Query().Get("path")}}, nil)
	if err != nil {
		s.filesErr(w, r, err)
		return
	}
	relay(w, res)
}

func (s *Server) writeFile(w http.ResponseWriter, r *http.Request) {
	srv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	res, err := s.filesRequest(r.Context(), srv, "PUT", "/write", url.Values{"path": {req.Path}}, strings.NewReader(req.Content))
	if err != nil {
		s.filesErr(w, r, err)
		return
	}
	if res.StatusCode == http.StatusNoContent {
		_ = res.Body.Close()
		s.auditAs(r, "server.file_write", srv.Slug, map[string]any{"path": req.Path, "bytes": len(req.Content)})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	relay(w, res)
}

// backup streams a tar.gz. For a running server, autosave is paused and the world
// flushed first so the archive is consistent.
func (s *Server) backup(w http.ResponseWriter, r *http.Request) {
	srv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "world"
	}
	if scope != "world" && scope != "full" {
		writeError(w, http.StatusBadRequest, "scope must be world or full")
		return
	}
	if s.mgr.Status(srv).State == k8s.StateRunning && s.rcon != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		_, offErr := s.rcon.Exec(ctx, srv.Slug, "save-off")
		_, flushErr := s.rcon.Exec(ctx, srv.Slug, "save-all flush")
		cancel()
		if offErr == nil {
			defer func() {
				// Always turn autosave back on, even if the download was cut.
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if _, err := s.rcon.Exec(ctx, srv.Slug, "save-on"); err != nil {
					slog.Error("could not re-enable autosave", "server", srv.Slug, "err", err)
				}
			}()
		}
		if offErr != nil || flushErr != nil {
			slog.Warn("backup without save flush", "server", srv.Slug, "off", offErr, "flush", flushErr)
		}
	}
	res, err := s.filesRequest(r.Context(), srv, "GET", "/backup", url.Values{"scope": {scope}}, nil)
	if err != nil {
		s.filesErr(w, r, err)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		relay(w, res)
		return
	}
	name := fmt.Sprintf("%s-%s-%s.tar.gz", srv.Slug, scope, time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	s.auditAs(r, "server.backup", srv.Slug, map[string]any{"scope": scope})
	metrics.ServerActions.WithLabelValues("backup").Inc()
	if _, err := io.Copy(w, res.Body); err != nil {
		slog.Warn("backup download interrupted", "server", srv.Slug, "err", err)
	}
}
