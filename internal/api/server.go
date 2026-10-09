// Package api serves the JSON API, WebSockets, and the embedded UI.
package api

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/auth"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/config"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/db"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/live"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/modrinth"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/servers"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/versions"
)

// Deps are the services the API needs.
type Deps struct {
	Config *config.Config
	Store  *db.Store
	Auth   *auth.Service
	Mgr    *servers.Manager
	RCON   *live.Pool
	Live   *live.Poller
	// Files reaches the file sidecars; defaults to the k8s client.
	Files FilesAccess
	// Catalog is optional; nil disables mod and plugin search.
	Catalog *modrinth.Client
	// Versions is optional; nil returns an empty list.
	Versions *versions.Cache
	// UI holds the built frontend; nil disables static serving.
	UI fs.FS
	// Ready reports extra readiness checks (for example the k8s client).
	Ready func(ctx context.Context) error
}

type Server struct {
	cfg      *config.Config
	store    *db.Store
	auth     *auth.Service
	mgr      *servers.Manager
	rcon     *live.Pool
	live     *live.Poller
	catalog  *modrinth.Client
	files    FilesAccess
	versions *versions.Cache
	ui       fs.FS
	ready    func(ctx context.Context) error
	origins  []string
}

func New(d Deps) *Server {
	if d.Files == nil && d.Mgr != nil {
		d.Files = d.Mgr.Kube()
	}
	return &Server{
		cfg:      d.Config,
		store:    d.Store,
		auth:     d.Auth,
		mgr:      d.Mgr,
		rcon:     d.RCON,
		live:     d.Live,
		catalog:  d.Catalog,
		files:    d.Files,
		versions: d.Versions,
		ui:       d.UI,
		ready:    d.Ready,
		origins:  d.Config.Origins(),
	}
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, logRequests, recoverer, securityHeaders)

	r.Get("/healthz", s.healthz)
	r.Get("/readyz", s.readyz)

	r.Route("/api", func(r chi.Router) {
		r.Use(middleware.NoCache, s.loadSession, s.checkCSRF)
		r.Post("/auth/login", s.login)

		r.Group(func(r chi.Router) {
			r.Use(requireAuth)
			r.Post("/auth/logout", s.logout)
			r.Get("/me", s.me)
			r.Post("/me/password", s.changePassword)

			r.Group(func(r chi.Router) {
				r.Use(requirePasswordChanged)
				s.mountProtected(r)
			})
		})
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "not found")
		})
	})

	if s.ui != nil {
		r.Handle("/*", spaHandler(s.ui))
	}
	return r
}

// mountProtected registers routes that need a logged in user with a valid password.
func (s *Server) mountProtected(r chi.Router) {
	r.Get("/plans", s.listActivePlans)
	r.Get("/meta", s.meta)
	r.Get("/versions", s.listVersions)
	r.Get("/me/quota", s.myQuota)
	r.Get("/catalog/search", s.catalogSearch)
	r.Get("/catalog/projects", s.catalogProjects)

	r.Get("/servers", s.listServers)
	r.Post("/servers", s.createServer)
	r.Get("/servers/slug-available", s.slugAvailable)
	r.Route("/servers/{id}", func(r chi.Router) {
		r.Get("/", s.getServer)
		r.Patch("/", s.updateServer)
		r.Delete("/", s.deleteServer)
		r.Get("/status", s.getServer)
		r.Post("/start", s.serverAction("start"))
		r.Post("/stop", s.serverAction("stop"))
		r.Post("/restart", s.serverAction("restart"))
		r.Post("/players", s.playerAction)
		r.Get("/files", s.listFiles)
		r.Get("/files/content", s.readFile)
		r.Put("/files/content", s.writeFile)
		r.Get("/backup", s.backup)
		r.Get("/logs", s.logsWS)
		r.Get("/console", s.consoleWS)
	})
	r.Route("/admin", s.mountAdmin)
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	if s.ready != nil {
		if err := s.ready(ctx); err != nil {
			// The endpoint is public through the ingress, so the detail only goes to the log.
			slog.WarnContext(ctx, "readiness check failed", "err", err)
			writeError(w, http.StatusServiceUnavailable, "not ready")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
