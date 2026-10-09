package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/auth"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/db"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/db/sqlc"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/k8s"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/live"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/metrics"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/playit"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/quota"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/servers"
)

type serverJSON struct {
	ID            uuid.UUID `json:"id"`
	OwnerID       uuid.UUID `json:"owner_id"`
	OwnerUsername string    `json:"owner_username,omitempty"`
	Slug          string    `json:"slug"`
	DisplayName   string    `json:"display_name"`
	// Address is what players use: the playit address when the server has a tunnel.
	Address        string           `json:"address"`
	PrivateAddress string           `json:"private_address"`
	Playit         *playitJSON      `json:"playit,omitempty"`
	Plan           planJSON         `json:"plan"`
	Type           string           `json:"type"`
	Version        string           `json:"version"`
	Settings       servers.Settings `json:"settings"`
	DesiredState   string           `json:"desired_state"`
	Status         k8s.Status       `json:"status"`
	Live           *live.Stats      `json:"live,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

func (s *Server) toServerJSON(srv sqlc.Server, plan sqlc.Plan) serverJSON {
	settings, err := servers.ParseSettings(srv.Settings)
	if err != nil {
		settings = servers.DefaultSettings()
	}
	var stats *live.Stats
	if s.live != nil {
		if st, ok := s.live.Get(srv.ID); ok {
			stats = &st
		}
	}
	return s.withPlayit(serverJSON{
		Live:           stats,
		ID:             srv.ID,
		OwnerID:        srv.OwnerID,
		Slug:           srv.Slug,
		DisplayName:    srv.DisplayName,
		Address:        s.mgr.Kube().Options().Host(srv.Slug),
		PrivateAddress: s.mgr.Kube().Options().Host(srv.Slug),
		Plan:           toPlanJSON(plan),
		Type:           srv.Type,
		Version:        srv.Version,
		Settings:       settings,
		DesiredState:   srv.DesiredState,
		Status:         s.mgr.Status(srv),
		CreatedAt:      srv.CreatedAt,
		UpdatedAt:      srv.UpdatedAt,
	})
}

// playitJSON tells the UI about the server's tunnel, or how to create it.
type playitJSON struct {
	TunnelName    string   `json:"tunnel_name"`
	Found         bool     `json:"found"`
	Online        bool     `json:"online"`
	Address       string   `json:"address,omitempty"`
	LocalIP       string   `json:"local_ip"`
	LocalPort     int      `json:"local_port,omitempty"`
	SuggestedPort int      `json:"suggested_port,omitempty"`
	Problems      []string `json:"problems,omitempty"`
}

// withPlayit fills the playit block and, when a tunnel exists, uses its address.
func (s *Server) withPlayit(j serverJSON) serverJSON {
	t, found, enabled := s.mgr.Tunnel(j.Slug)
	if !enabled {
		return j
	}
	p := &playitJSON{TunnelName: playit.Prefix + j.Slug, Found: found, LocalIP: playit.LocalIP}
	if found {
		p.Online, p.Address, p.LocalPort, p.Problems = t.Online, t.Address, t.LocalPort, t.Problems
		if t.Address != "" {
			j.Address = t.Address
		}
	} else {
		p.SuggestedPort = playit.SuggestPort(s.mgr.Tunnels().UsedPorts(), s.cfg.PlayitFirstPort)
	}
	j.Playit = p
	return j
}

// writeServers renders a list with plans resolved in one query.
func (s *Server) writeServers(w http.ResponseWriter, r *http.Request, rows []sqlc.Server, withOwner bool) {
	plans, err := s.planMap(r)
	if err != nil {
		internalError(w, r, err)
		return
	}
	var owners map[uuid.UUID]string
	if withOwner {
		users, err := s.store.ListUsers(r.Context())
		if err != nil {
			internalError(w, r, err)
			return
		}
		owners = make(map[uuid.UUID]string, len(users))
		for _, u := range users {
			owners[u.ID] = u.Username
		}
	}
	out := make([]serverJSON, 0, len(rows))
	for _, srv := range rows {
		j := s.toServerJSON(srv, plans[srv.PlanID])
		j.OwnerUsername = owners[srv.OwnerID]
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) planMap(r *http.Request) (map[uuid.UUID]sqlc.Plan, error) {
	list, err := s.store.ListPlans(r.Context())
	if err != nil {
		return nil, err
	}
	m := make(map[uuid.UUID]sqlc.Plan, len(list))
	for _, p := range list {
		m[p.ID] = p
	}
	return m, nil
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// handleErr maps domain errors to HTTP responses.
func handleErr(w http.ResponseWriter, r *http.Request, err error) {
	var ex *quota.ExceededError
	switch {
	case servers.IsValidation(err):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.As(err, &ex):
		metrics.QuotaRejections.Inc()
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, k8s.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case db.IsNotFound(err):
		writeError(w, http.StatusNotFound, "not found")
	default:
		internalError(w, r, err)
	}
}

// loadServer fetches the {id} server if the user may access it. Other users'
// servers return 404 so their existence is not revealed.
func (s *Server) loadServer(w http.ResponseWriter, r *http.Request) (sqlc.Server, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return sqlc.Server{}, false
	}
	srv, err := s.store.GetServer(r.Context(), id)
	if err != nil {
		handleErr(w, r, err)
		return sqlc.Server{}, false
	}
	if !canAccess(mustIdentity(r), srv) {
		writeError(w, http.StatusNotFound, "not found")
		return sqlc.Server{}, false
	}
	return srv, true
}

func canAccess(id auth.Identity, srv sqlc.Server) bool {
	return srv.OwnerID == id.User.ID || id.IsAdmin()
}

func (s *Server) listServers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.ListServersByOwner(r.Context(), mustIdentity(r).User.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	s.writeServers(w, r, rows, false)
}

func (s *Server) writeServer(w http.ResponseWriter, r *http.Request, status int, srv sqlc.Server) {
	plan, err := s.store.GetPlan(r.Context(), srv.PlanID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, status, s.toServerJSON(srv, plan))
}

func (s *Server) createServer(w http.ResponseWriter, r *http.Request) {
	var req servers.CreateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	id := mustIdentity(r)
	srv, err := s.mgr.Create(r.Context(), id.User.ID, req)
	if err != nil {
		handleErr(w, r, err)
		return
	}
	metrics.ServerActions.WithLabelValues("create").Inc()
	s.auditAs(r, "server.create", srv.Slug, map[string]any{"type": srv.Type, "version": srv.Version, "plan_id": srv.PlanID})
	s.writeServer(w, r, http.StatusCreated, srv)
}

func (s *Server) slugAvailable(w http.ResponseWriter, r *http.Request) {
	err := s.mgr.SlugAvailable(r.Context(), r.URL.Query().Get("slug"))
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"available": true})
	case servers.IsValidation(err):
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "reason": err.Error()})
	default:
		internalError(w, r, err)
	}
}

func (s *Server) getServer(w http.ResponseWriter, r *http.Request) {
	srv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	s.writeServer(w, r, http.StatusOK, srv)
}

func (s *Server) updateServer(w http.ResponseWriter, r *http.Request) {
	srv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	var req servers.UpdateRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	id := mustIdentity(r)
	updated, err := s.mgr.Update(r.Context(), srv.ID, req, id.IsAdmin())
	if err != nil {
		handleErr(w, r, err)
		return
	}
	details := map[string]any{}
	if req.PlanID != nil && *req.PlanID != srv.PlanID {
		details["plan_id"] = *req.PlanID
	}
	if req.Version != nil {
		details["version"] = *req.Version
	}
	if len(req.Settings) > 0 {
		details["settings"] = true
	}
	metrics.ServerActions.WithLabelValues("update").Inc()
	s.auditAs(r, "server.update", srv.Slug, details)
	if liveChanged(srv, updated) {
		s.applyLive(r, updated)
	}
	s.writeServer(w, r, http.StatusOK, updated)
}

func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request) {
	srv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	var req struct {
		Confirm  string `json:"confirm"`
		KeepData bool   `json:"keep_data"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Confirm != srv.Slug {
		writeError(w, http.StatusBadRequest, "type the server name to confirm deletion")
		return
	}
	if err := s.mgr.Delete(r.Context(), srv.ID, req.KeepData); err != nil {
		handleErr(w, r, err)
		return
	}
	metrics.ServerActions.WithLabelValues("delete").Inc()
	s.auditAs(r, "server.delete", srv.Slug, map[string]any{"keep_data": req.KeepData})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serverAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		srv, ok := s.loadServer(w, r)
		if !ok {
			return
		}
		var err error
		switch action {
		case "start":
			srv, err = s.mgr.Start(r.Context(), srv.ID)
		case "stop":
			srv, err = s.mgr.Stop(r.Context(), srv.ID)
		case "restart":
			err = s.mgr.Restart(r.Context(), srv)
		}
		if err != nil {
			handleErr(w, r, err)
			return
		}
		metrics.ServerActions.WithLabelValues(action).Inc()
		s.auditAs(r, "server."+action, srv.Slug, nil)
		s.writeServer(w, r, http.StatusOK, srv)
	}
}

// liveChanged reports whether the settings applied over RCON differ.
func liveChanged(a, b sqlc.Server) bool {
	sa, errA := servers.ParseSettings(a.Settings)
	sb, errB := servers.ParseSettings(b.Settings)
	if errA != nil || errB != nil {
		return true
	}
	return !slices.Equal(sa.LiveCommands(), sb.LiveCommands())
}

// applyLive pushes world settings over RCON when the server runs. Best effort: the
// poller applies them again after the next start.
func (s *Server) applyLive(r *http.Request, srv sqlc.Server) bool {
	if s.rcon == nil || srv.DesiredState != servers.DesiredRunning {
		return false
	}
	settings, err := servers.ParseSettings(srv.Settings)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	return live.ApplyAll(ctx, s.rcon, srv.Slug, settings.LiveCommands())
}

func (s *Server) myQuota(w http.ResponseWriter, r *http.Request) {
	limits, usage, err := servers.Limits(r.Context(), s.store.Queries, mustIdentity(r).User.ID, nil)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"limits": limits, "usage": usage})
}

func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	levels := make([]string, 0, len(servers.LevelTypes))
	for _, l := range []string{"normal", "flat", "large_biomes", "amplified"} {
		if _, ok := servers.LevelTypes[l]; ok {
			levels = append(levels, l)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"server_base_domain": s.cfg.ServerBaseDomain,
		"types":              servers.Types,
		"difficulties":       servers.Difficulties,
		"gamemodes":          servers.Gamemodes,
		"level_types":        levels,
		"default_settings":   servers.DefaultSettings(),
		"jvm_heap_ratio":     s.cfg.JVMHeapRatio,
		"auto_sleep":         s.cfg.AutoSleep,
		"auto_sleep_idle":    s.cfg.AutoSleepIdle,
		"files":              s.cfg.FilesImage != "",
		"playit":             s.playitMeta(r),
	})
}

func (s *Server) listVersions(w http.ResponseWriter, r *http.Request) {
	if s.versions == nil {
		writeJSON(w, http.StatusOK, map[string]any{"latest": "", "versions": []any{}})
		return
	}
	l, err := s.versions.Get(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "version list unavailable, type a version instead")
		return
	}
	writeJSON(w, http.StatusOK, l)
}

// playitMeta says whether playit is on and, for admins, whether listing tunnels works.
func (s *Server) playitMeta(r *http.Request) map[string]any {
	src := s.mgr.Tunnels()
	if src == nil {
		return map[string]any{"enabled": false}
	}
	m := map[string]any{"enabled": true, "first_port": s.cfg.PlayitFirstPort}
	if err := src.LastError(); err != nil && mustIdentity(r).IsAdmin() {
		m["error"] = err.Error()
	}
	return m
}
