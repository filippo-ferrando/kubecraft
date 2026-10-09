package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/db/sqlc"
)

// audit writes an audit log entry. Failures are logged but do not fail the request.
func (s *Server) audit(r *http.Request, userID uuid.NullUUID, action, target string, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	details["ip"] = s.clientIP(r)
	b, err := json.Marshal(details)
	if err != nil {
		b = []byte("{}")
	}
	err = s.store.InsertAudit(r.Context(), sqlc.InsertAuditParams{UserID: userID, Action: action, Target: target, Details: b})
	if err != nil {
		slog.ErrorContext(r.Context(), "audit write failed", "action", action, "err", err)
	}
}

// auditAs is audit for the logged in user.
func (s *Server) auditAs(r *http.Request, action, target string, details map[string]any) {
	id := mustIdentity(r)
	s.audit(r, uuid.NullUUID{UUID: id.User.ID, Valid: true}, action, target, details)
}
