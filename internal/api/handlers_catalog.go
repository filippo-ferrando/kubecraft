package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/modrinth"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/servers"
)

// catalogSearch proxies a Modrinth search filtered by server type and game version.
func (s *Server) catalogSearch(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "the mod catalog is disabled")
		return
	}
	q := r.URL.Query()
	serverType := q.Get("type")
	if err := servers.ValidateType(serverType); err != nil || modrinth.KindFor(serverType) == "" {
		writeError(w, http.StatusBadRequest, "this server type does not support mods or plugins")
		return
	}
	version := q.Get("version")
	if version == "LATEST" || version == "" {
		version = ""
		if s.versions != nil {
			if l, err := s.versions.Get(r.Context()); err == nil {
				version = l.Latest
			}
		}
	} else if servers.ValidateVersion(version) != nil {
		writeError(w, http.StatusBadRequest, "invalid version")
		return
	}
	query := strings.TrimSpace(q.Get("q"))
	if len(query) > 100 {
		query = query[:100]
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	res, err := s.catalog.Search(r.Context(), serverType, version, query, min(max(offset, 0), 1000))
	if errors.Is(err, modrinth.ErrUnavailable) {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": modrinth.KindFor(serverType), "game_version": version, "result": res})
}

// catalogProjects returns details of chosen projects, for display.
func (s *Server) catalogProjects(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	var refs []string
	for _, p := range strings.Split(r.URL.Query().Get("ids"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			if !modrinth.ValidProjectRef(p) || len(refs) >= 100 {
				writeError(w, http.StatusBadRequest, "invalid project list")
				return
			}
			refs = append(refs, p)
		}
	}
	res, err := s.catalog.Projects(r.Context(), refs)
	if errors.Is(err, modrinth.ErrUnavailable) {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
