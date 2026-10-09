package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/servers"
)

// playerAction changes the whitelist or ops, or kicks a player. Changes are stored
// in the settings and applied over RCON right away when the server runs.
func (s *Server) playerAction(w http.ResponseWriter, r *http.Request) {
	srv, ok := s.loadServer(w, r)
	if !ok {
		return
	}
	var req struct {
		Action string `json:"action"`
		Name   string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if !servers.ValidPlayerName(name) {
		writeError(w, http.StatusBadRequest, "not a valid Minecraft username")
		return
	}
	settings, err := servers.ParseSettings(srv.Settings)
	if err != nil {
		internalError(w, r, err)
		return
	}
	has := func(list []string) int {
		return slices.IndexFunc(list, func(p string) bool { return strings.EqualFold(p, name) })
	}
	changed := true
	var cmd string
	switch req.Action {
	case "whitelist_add":
		cmd = "whitelist add " + name
		if has(settings.Whitelist) < 0 {
			settings.Whitelist = append(settings.Whitelist, name)
		}
	case "whitelist_remove":
		cmd = "whitelist remove " + name
		if i := has(settings.Whitelist); i >= 0 {
			settings.Whitelist = slices.Delete(settings.Whitelist, i, i+1)
		}
	case "op":
		cmd = "op " + name
		if has(settings.Ops) < 0 {
			settings.Ops = append(settings.Ops, name)
		}
	case "deop":
		cmd = "deop " + name
		if i := has(settings.Ops); i >= 0 {
			settings.Ops = slices.Delete(settings.Ops, i, i+1)
		}
	case "kick":
		cmd, changed = "kick "+name, false
	default:
		writeError(w, http.StatusBadRequest, "unknown action")
		return
	}

	running := srv.DesiredState == servers.DesiredRunning
	if !changed && !running {
		writeError(w, http.StatusConflict, "server is not running")
		return
	}
	if changed {
		raw, err := json.Marshal(settings)
		if err != nil {
			internalError(w, r, err)
			return
		}
		if srv, err = s.mgr.Update(r.Context(), srv.ID, servers.UpdateRequest{Settings: raw}, mustIdentity(r).IsAdmin()); err != nil {
			handleErr(w, r, err)
			return
		}
	}

	resp := map[string]any{"applied_live": false}
	if running {
		out, err := s.rcon.Exec(r.Context(), srv.Slug, cmd)
		if err == nil {
			resp["applied_live"], resp["output"] = true, out
		} else if !changed {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	}
	s.auditAs(r, "players."+req.Action, srv.Slug, map[string]any{"player": name})
	plan, err := s.store.GetPlan(r.Context(), srv.PlanID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	resp["server"] = s.toServerJSON(srv, plan)
	writeJSON(w, http.StatusOK, resp)
}
