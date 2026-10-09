package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/auth"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/metrics"
)

const (
	sessionCookie = "kubecraft_session"
	csrfHeader    = "X-CSRF-Token"
)

type ctxKey int

const identityKey ctxKey = iota

func identityFrom(ctx context.Context) (auth.Identity, bool) {
	id, ok := ctx.Value(identityKey).(auth.Identity)
	return id, ok
}

// mustIdentity is used by handlers mounted behind requireAuth.
func mustIdentity(r *http.Request) auth.Identity {
	id, ok := identityFrom(r.Context())
	if !ok {
		panic("handler mounted without requireAuth")
	}
	return id
}

// logRequests writes one structured log line per request.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		observeRequest(r, ww.Status(), time.Since(start))
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			return
		}
		slog.LogAttrs(r.Context(), slog.LevelInfo, "http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", ww.Status()),
			slog.Duration("duration", time.Since(start)),
			slog.String("request_id", middleware.GetReqID(r.Context())),
		)
	})
}

// observeRequest records HTTP metrics using the chi route pattern, so ids in paths
// do not create new series.
func observeRequest(r *http.Request, status int, d time.Duration) {
	route := "other"
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if p := rc.RoutePattern(); p != "" {
			route = p
		}
	}
	if route == "/*" {
		route = "static"
	} else if len(route) > 1 {
		// Subrouters report "/x/" and "/x" for the same route.
		route = strings.TrimSuffix(route, "/")
	}
	if status == 0 {
		status = http.StatusOK
	}
	metrics.HTTPRequests.WithLabelValues(r.Method, route, strconv.Itoa(status)).Inc()
	// Sockets stay open for minutes and would distort latency.
	if !strings.HasSuffix(route, "/logs") && !strings.HasSuffix(route, "/console") {
		metrics.HTTPDuration.WithLabelValues(r.Method, route).Observe(d.Seconds())
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https://cdn.modrinth.com; style-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// loadSession attaches the identity to the context when a valid cookie is present.
func (s *Server) loadSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		id, err := s.auth.Session(r.Context(), c.Value)
		if errors.Is(err, auth.ErrNoSession) {
			s.clearCookie(w)
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			internalError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey, id)))
	})
}

func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := identityFrom(r.Context()); !ok {
			writeError(w, http.StatusUnauthorized, "login required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requirePasswordChanged blocks everything but the password change until it is done.
func requirePasswordChanged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := mustIdentity(r); id.User.MustChangePassword {
			writeError(w, http.StatusForbidden, "password change required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !mustIdentity(r).IsAdmin() {
			writeError(w, http.StatusForbidden, "admin only")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// checkCSRF rejects state-changing requests from other origins or without the session token.
func (s *Server) checkCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if !s.originAllowed(r) {
			writeError(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		if id, ok := identityFrom(r.Context()); ok {
			got := r.Header.Get(csrfHeader)
			if subtle.ConstantTimeCompare([]byte(got), []byte(id.Session.CsrfToken)) != 1 {
				writeError(w, http.StatusForbidden, "missing or invalid CSRF token")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// originAllowed checks Origin (or Referer as a fallback) against the configured UI origins.
func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		ref := r.Header.Get("Referer")
		if ref == "" {
			// Non-browser clients send neither; cookies are SameSite=Lax anyway.
			return r.Header.Get("Sec-Fetch-Site") == ""
		}
		if i := strings.Index(ref, "://"); i >= 0 {
			if j := strings.IndexByte(ref[i+3:], '/'); j >= 0 {
				ref = ref[:i+3+j]
			}
		}
		origin = ref
	}
	return slices.Contains(s.origins, strings.TrimSuffix(origin, "/"))
}

func (s *Server) setCookie(w http.ResponseWriter, token string) {
	// Secure is configurable for local development over plain HTTP.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(s.auth.TTL().Seconds()),
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	// Secure is configurable for local development over plain HTTP.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// clientIP trusts proxy headers only when the direct peer is a private address (the ingress).
// The configured client IP header wins over X-Forwarded-For.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
		return host
	}
	if s.cfg.ClientIPHeader != "" {
		if v := strings.TrimSpace(r.Header.Get(s.cfg.ClientIPHeader)); net.ParseIP(v) != nil {
			return v
		}
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if last := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(last) != nil {
			return last
		}
	}
	return host
}

// recoverer turns panics into a 500 and logs them.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}
				slog.ErrorContext(r.Context(), "panic in handler", "panic", rec, "path", r.URL.Path)
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
