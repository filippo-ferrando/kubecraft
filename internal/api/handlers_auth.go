package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/auth"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/db/sqlc"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/metrics"
)

type userJSON struct {
	ID                 uuid.UUID `json:"id"`
	Username           string    `json:"username"`
	Role               string    `json:"role"`
	Disabled           bool      `json:"disabled"`
	MustChangePassword bool      `json:"must_change_password"`
	ConsoleEnabled     bool      `json:"console_enabled"`
	CreatedAt          time.Time `json:"created_at"`
}

func toUserJSON(u sqlc.User) userJSON {
	return userJSON{
		ID:                 u.ID,
		Username:           u.Username,
		Role:               u.Role,
		Disabled:           u.Disabled,
		MustChangePassword: u.MustChangePassword,
		ConsoleEnabled:     u.ConsoleEnabled,
		CreatedAt:          u.CreatedAt,
	}
}

type meJSON struct {
	User      userJSON `json:"user"`
	CSRFToken string   `json:"csrf_token"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Username) > 64 || len(req.Password) > auth.MaxPasswordLen*4 {
		writeError(w, http.StatusUnauthorized, auth.ErrInvalidCredentials.Error())
		return
	}
	token, u, err := s.auth.Login(r.Context(), req.Username, req.Password, clientIP(r), r.UserAgent())
	var rl *auth.RateLimitedError
	switch {
	case errors.As(err, &rl):
		metrics.Logins.WithLabelValues("rate_limited").Inc()
		w.Header().Set("Retry-After", strconv.Itoa(int(rl.RetryAfter.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, rl.Error())
		return
	case errors.Is(err, auth.ErrInvalidCredentials):
		metrics.Logins.WithLabelValues("failed").Inc()
		s.audit(r, uuid.NullUUID{}, "auth.login_failed", auth.NormalizeUsername(req.Username), nil)
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	case err != nil:
		internalError(w, r, err)
		return
	}
	metrics.Logins.WithLabelValues("success").Inc()
	s.setCookie(w, token)
	id, err := s.auth.Session(r.Context(), token)
	if err != nil {
		internalError(w, r, err)
		return
	}
	s.audit(r, uuid.NullUUID{UUID: u.ID, Valid: true}, "auth.login", u.Username, nil)
	writeJSON(w, http.StatusOK, meJSON{User: toUserJSON(id.User), CSRFToken: id.Session.CsrfToken})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.auth.Logout(r.Context(), c.Value); err != nil {
			internalError(w, r, err)
			return
		}
	}
	s.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	id := mustIdentity(r)
	writeJSON(w, http.StatusOK, meJSON{User: toUserJSON(id.User), CSRFToken: id.Session.CsrfToken})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	id := mustIdentity(r)
	if req.Current == req.New {
		writeError(w, http.StatusBadRequest, "new password must differ from the current one")
		return
	}
	err := s.auth.ChangePassword(r.Context(), id, req.Current, req.New)
	switch {
	case errors.Is(err, auth.ErrWrongPassword):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, auth.ErrPasswordTooShort), errors.Is(err, auth.ErrPasswordTooLong):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		internalError(w, r, err)
		return
	}
	s.audit(r, uuid.NullUUID{UUID: id.User.ID, Valid: true}, "user.password_changed", id.User.Username, nil)
	w.WriteHeader(http.StatusNoContent)
}
