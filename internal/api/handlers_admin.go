package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/auth"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/db"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/db/sqlc"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/quota"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/servers"
)

func (s *Server) mountAdmin(r chi.Router) {
	r.Use(requireAdmin)
	r.Get("/users", s.adminListUsers)
	r.Post("/users", s.adminCreateUser)
	r.Patch("/users/{id}", s.adminUpdateUser)
	r.Post("/users/{id}/password", s.adminResetPassword)
	r.Delete("/users/{id}", s.adminDeleteUser)

	r.Get("/quotas", s.adminGetQuotas)
	r.Put("/quotas/default", s.adminSetDefaultQuota)
	r.Put("/quotas/{id}", s.adminSetUserQuota)

	r.Get("/plans", s.adminListPlans)
	r.Post("/plans", s.adminCreatePlan)
	r.Patch("/plans/{id}", s.adminUpdatePlan)
	r.Delete("/plans/{id}", s.adminDeletePlan)

	r.Get("/servers", s.adminListServers)
	r.Get("/audit", s.adminAudit)
}

// quotaJSON uses nil for "not set" (user override) or "unlimited" (default).
type quotaJSON struct {
	MaxServers        *int32 `json:"max_servers"`
	MaxTotalCPUM      *int32 `json:"max_total_cpu_m"`
	MaxTotalMemoryMB  *int32 `json:"max_total_memory_mb"`
	MaxTotalStorageGB *int32 `json:"max_total_storage_gb"`
}

func fromInt4(v pgtype.Int4) *int32 {
	if !v.Valid {
		return nil
	}
	return &v.Int32
}

func toInt4(v *int32) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *v, Valid: true}
}

func (q quotaJSON) validate() error {
	for _, v := range []*int32{q.MaxServers, q.MaxTotalCPUM, q.MaxTotalMemoryMB, q.MaxTotalStorageGB} {
		if v != nil && *v < 0 {
			return errors.New("quota values cannot be negative")
		}
	}
	return nil
}

type adminUserJSON struct {
	userJSON
	Quota  quotaJSON    `json:"quota"`
	Limits quota.Limits `json:"limits"`
	Usage  quota.Usage  `json:"usage"`
}

func (s *Server) adminUser(r *http.Request, u sqlc.User) (adminUserJSON, error) {
	out := adminUserJSON{userJSON: toUserJSON(u)}
	uq, err := s.store.GetUserQuota(r.Context(), u.ID)
	switch {
	case err == nil:
		out.Quota = quotaJSON{fromInt4(uq.MaxServers), fromInt4(uq.MaxTotalCpuM), fromInt4(uq.MaxTotalMemoryMb), fromInt4(uq.MaxTotalStorageGb)}
	case !db.IsNotFound(err):
		return out, err
	}
	out.Limits, out.Usage, err = servers.Limits(r.Context(), s.store.Queries, u.ID, nil)
	return out, err
}

func (s *Server) adminListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := make([]adminUserJSON, 0, len(users))
	for _, u := range users {
		j, err := s.adminUser(r, u)
		if err != nil {
			internalError(w, r, err)
			return
		}
		out = append(out, j)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Role == "" {
		req.Role = auth.RoleUser
	}
	u, err := auth.CreateUser(r.Context(), s.store.Queries, req.Username, req.Password, req.Role, true)
	switch {
	case isUnique(err):
		writeError(w, http.StatusConflict, "username already exists")
		return
	case err != nil && isUserInputErr(err):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		internalError(w, r, err)
		return
	}
	s.auditAs(r, "admin.user_create", u.Username, map[string]any{"role": u.Role})
	j, err := s.adminUser(r, u)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, j)
}

func isUserInputErr(err error) bool {
	return errors.Is(err, auth.ErrBadUsername) || errors.Is(err, auth.ErrPasswordTooShort) ||
		errors.Is(err, auth.ErrPasswordTooLong) || strings.HasPrefix(err.Error(), "unknown role")
}

func (s *Server) loadUser(w http.ResponseWriter, r *http.Request) (sqlc.User, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return sqlc.User{}, false
	}
	u, err := s.store.GetUser(r.Context(), id)
	if err != nil {
		handleErr(w, r, err)
		return sqlc.User{}, false
	}
	return u, true
}

// otherActiveAdmins counts enabled admins besides id, to prevent locking everyone out.
func (s *Server) otherActiveAdmins(r *http.Request, id uuid.UUID) (int, error) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		return 0, err
	}
	n := 0
	for _, u := range users {
		if u.ID != id && u.Role == auth.RoleAdmin && !u.Disabled {
			n++
		}
	}
	return n, nil
}

func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.loadUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Role           *string `json:"role"`
		Disabled       *bool   `json:"disabled"`
		ConsoleEnabled *bool   `json:"console_enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	p := sqlc.UpdateUserFlagsParams{ID: u.ID, Role: u.Role, Disabled: u.Disabled, ConsoleEnabled: u.ConsoleEnabled}
	if req.Role != nil {
		if *req.Role != auth.RoleAdmin && *req.Role != auth.RoleUser {
			writeError(w, http.StatusBadRequest, "role must be admin or user")
			return
		}
		p.Role = *req.Role
	}
	if req.Disabled != nil {
		p.Disabled = *req.Disabled
	}
	if req.ConsoleEnabled != nil {
		p.ConsoleEnabled = *req.ConsoleEnabled
	}
	losesAdmin := u.Role == auth.RoleAdmin && !u.Disabled && (p.Role != auth.RoleAdmin || p.Disabled)
	if losesAdmin {
		if u.ID == mustIdentity(r).User.ID {
			writeError(w, http.StatusBadRequest, "you cannot demote or disable yourself")
			return
		}
		n, err := s.otherActiveAdmins(r, u.ID)
		if err != nil {
			internalError(w, r, err)
			return
		}
		if n == 0 {
			writeError(w, http.StatusBadRequest, "at least one active admin is required")
			return
		}
	}
	updated, err := s.store.UpdateUserFlags(r.Context(), p)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if updated.Disabled && !u.Disabled {
		if err := s.store.DeleteUserSessions(r.Context(), u.ID); err != nil {
			internalError(w, r, err)
			return
		}
	}
	s.auditAs(r, "admin.user_update", u.Username, map[string]any{"role": updated.Role, "disabled": updated.Disabled, "console_enabled": updated.ConsoleEnabled})
	j, err := s.adminUser(r, updated)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) adminResetPassword(w http.ResponseWriter, r *http.Request) {
	u, ok := s.loadUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.auth.ResetPassword(r.Context(), u.ID, req.Password)
	switch {
	case errors.Is(err, auth.ErrPasswordTooShort), errors.Is(err, auth.ErrPasswordTooLong):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		internalError(w, r, err)
		return
	}
	s.auditAs(r, "admin.password_reset", u.Username, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.loadUser(w, r)
	if !ok {
		return
	}
	if u.ID == mustIdentity(r).User.ID {
		writeError(w, http.StatusBadRequest, "you cannot delete yourself")
		return
	}
	if u.Role == auth.RoleAdmin && !u.Disabled {
		n, err := s.otherActiveAdmins(r, u.ID)
		if err != nil {
			internalError(w, r, err)
			return
		}
		if n == 0 {
			writeError(w, http.StatusBadRequest, "at least one active admin is required")
			return
		}
	}
	n, err := s.store.CountServersByOwner(r.Context(), u.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if n > 0 {
		writeError(w, http.StatusConflict, "delete the user's servers first ("+strconv.FormatInt(n, 10)+" left)")
		return
	}
	if err := s.store.DeleteUser(r.Context(), u.ID); err != nil {
		internalError(w, r, err)
		return
	}
	s.auditAs(r, "admin.user_delete", u.Username, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminGetQuotas(w http.ResponseWriter, r *http.Request) {
	def, err := s.store.GetDefaultQuota(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"default": quotaJSON{fromInt4(def.MaxServers), fromInt4(def.MaxTotalCpuM), fromInt4(def.MaxTotalMemoryMb), fromInt4(def.MaxTotalStorageGb)},
	})
}

func (s *Server) adminSetDefaultQuota(w http.ResponseWriter, r *http.Request) {
	var q quotaJSON
	if !decodeJSON(w, r, &q) {
		return
	}
	if err := q.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_, err := s.store.UpdateDefaultQuota(r.Context(), sqlc.UpdateDefaultQuotaParams{
		MaxServers: toInt4(q.MaxServers), MaxTotalCpuM: toInt4(q.MaxTotalCPUM),
		MaxTotalMemoryMb: toInt4(q.MaxTotalMemoryMB), MaxTotalStorageGb: toInt4(q.MaxTotalStorageGB),
	})
	if err != nil {
		internalError(w, r, err)
		return
	}
	s.auditAs(r, "admin.quota_default", "", map[string]any{"quota": q})
	writeJSON(w, http.StatusOK, q)
}

func (s *Server) adminSetUserQuota(w http.ResponseWriter, r *http.Request) {
	u, ok := s.loadUser(w, r)
	if !ok {
		return
	}
	var q quotaJSON
	if !decodeJSON(w, r, &q) {
		return
	}
	if err := q.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_, err := s.store.UpsertUserQuota(r.Context(), sqlc.UpsertUserQuotaParams{
		UserID: u.ID, MaxServers: toInt4(q.MaxServers), MaxTotalCpuM: toInt4(q.MaxTotalCPUM),
		MaxTotalMemoryMb: toInt4(q.MaxTotalMemoryMB), MaxTotalStorageGb: toInt4(q.MaxTotalStorageGB),
	})
	if err != nil {
		internalError(w, r, err)
		return
	}
	s.auditAs(r, "admin.quota_user", u.Username, map[string]any{"quota": q})
	j, err := s.adminUser(r, u)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

type planInput struct {
	Name          string `json:"name"`
	CPURequestM   int32  `json:"cpu_request_m"`
	CPULimitM     int32  `json:"cpu_limit_m"`
	MemoryLimitMB int32  `json:"memory_limit_mb"`
	StorageGB     int32  `json:"storage_gb"`
	Active        bool   `json:"active"`
}

func (p *planInput) validate() error {
	p.Name = strings.TrimSpace(p.Name)
	switch {
	case p.Name == "" || utf8.RuneCountInString(p.Name) > 32:
		return errors.New("name must be 1-32 characters")
	case p.CPURequestM < 100 || p.CPURequestM > 64000:
		return errors.New("CPU request must be between 100m and 64000m")
	case p.CPULimitM < p.CPURequestM || p.CPULimitM > 64000:
		return errors.New("CPU limit must be at least the request and at most 64000m")
	case p.MemoryLimitMB < 1024 || p.MemoryLimitMB > 262144:
		return errors.New("memory must be between 1024 and 262144 MB")
	case p.StorageGB < 1 || p.StorageGB > 10000:
		return errors.New("storage must be between 1 and 10000 GB")
	}
	return nil
}

func (s *Server) adminListPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.store.ListPlans(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := make([]planJSON, len(plans))
	for i, p := range plans {
		out[i] = toPlanJSON(p)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminCreatePlan(w http.ResponseWriter, r *http.Request) {
	var in planInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := in.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := s.store.CreatePlan(r.Context(), sqlc.CreatePlanParams{
		Name: in.Name, CpuRequestM: in.CPURequestM, CpuLimitM: in.CPULimitM,
		MemoryLimitMb: in.MemoryLimitMB, StorageGb: in.StorageGB, Active: in.Active,
	})
	if isUnique(err) {
		writeError(w, http.StatusConflict, "a plan with this name exists")
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	s.auditAs(r, "admin.plan_create", p.Name, map[string]any{"plan": in})
	writeJSON(w, http.StatusCreated, toPlanJSON(p))
}

func (s *Server) loadPlan(w http.ResponseWriter, r *http.Request) (sqlc.Plan, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return sqlc.Plan{}, false
	}
	p, err := s.store.GetPlan(r.Context(), id)
	if err != nil {
		handleErr(w, r, err)
		return sqlc.Plan{}, false
	}
	return p, true
}

// adminUpdatePlan changes a plan. Servers on it pick up new limits on their next restart.
func (s *Server) adminUpdatePlan(w http.ResponseWriter, r *http.Request) {
	cur, ok := s.loadPlan(w, r)
	if !ok {
		return
	}
	in := planInput{
		Name: cur.Name, CPURequestM: cur.CpuRequestM, CPULimitM: cur.CpuLimitM,
		MemoryLimitMB: cur.MemoryLimitMb, StorageGB: cur.StorageGb, Active: cur.Active,
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := in.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.StorageGB < cur.StorageGb {
		n, err := s.store.CountServersOnPlan(r.Context(), cur.ID)
		if err != nil {
			internalError(w, r, err)
			return
		}
		if n > 0 {
			writeError(w, http.StatusBadRequest, "storage cannot shrink while servers use this plan")
			return
		}
	}
	p, err := s.store.UpdatePlan(r.Context(), sqlc.UpdatePlanParams{
		ID: cur.ID, Name: in.Name, CpuRequestM: in.CPURequestM, CpuLimitM: in.CPULimitM,
		MemoryLimitMb: in.MemoryLimitMB, StorageGb: in.StorageGB, Active: in.Active,
	})
	if isUnique(err) {
		writeError(w, http.StatusConflict, "a plan with this name exists")
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	s.auditAs(r, "admin.plan_update", p.Name, map[string]any{"plan": in})
	writeJSON(w, http.StatusOK, toPlanJSON(p))
}

func (s *Server) adminDeletePlan(w http.ResponseWriter, r *http.Request) {
	p, ok := s.loadPlan(w, r)
	if !ok {
		return
	}
	n, err := s.store.CountServersOnPlan(r.Context(), p.ID)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if n > 0 {
		writeError(w, http.StatusConflict, "servers use this plan, deactivate it instead")
		return
	}
	if err := s.store.DeletePlan(r.Context(), p.ID); err != nil {
		internalError(w, r, err)
		return
	}
	s.auditAs(r, "admin.plan_delete", p.Name, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminListServers(w http.ResponseWriter, r *http.Request) {
	var rows []sqlc.Server
	var err error
	if owner := r.URL.Query().Get("owner"); owner != "" {
		id, perr := uuid.Parse(owner)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "invalid owner id")
			return
		}
		rows, err = s.store.ListServersByOwner(r.Context(), id)
	} else {
		rows, err = s.store.ListServers(r.Context())
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	s.writeServers(w, r, rows, true)
}

type auditJSON struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Details   rawJSON   `json:"details"`
	CreatedAt time.Time `json:"created_at"`
}

type rawJSON []byte

func (j *rawJSON) UnmarshalJSON(b []byte) error {
	*j = append((*j)[:0], b...)
	return nil
}

func (j rawJSON) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("{}"), nil
	}
	return j, nil
}

func (s *Server) adminAudit(w http.ResponseWriter, r *http.Request) {
	limit := int32(100)
	if v, err := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 32); err == nil && v > 0 && v <= 500 {
		limit = int32(v)
	}
	p := sqlc.ListAuditParams{Limit: limit}
	if v, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64); err == nil {
		p.BeforeID = pgtype.Int8{Int64: v, Valid: true}
	}
	rows, err := s.store.ListAudit(r.Context(), p)
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := make([]auditJSON, len(rows))
	for i, a := range rows {
		out[i] = auditJSON{ID: a.ID, Username: a.Username, Action: a.Action, Target: a.Target, Details: a.Details, CreatedAt: a.CreatedAt}
	}
	writeJSON(w, http.StatusOK, out)
}
