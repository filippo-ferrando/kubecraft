package servers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/db"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/db/sqlc"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/k8s"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/quota"
)

// Desired states stored in the database.
const (
	DesiredRunning  = "running"
	DesiredStopped  = "stopped"
	DesiredDeleting = "deleting"
)

var ErrSlugTaken = &ValidationError{Msg: "this name is already taken"}

// ContentValidator checks mod and plugin references against a catalog.
type ContentValidator interface {
	Validate(ctx context.Context, serverType string, refs []string) ([]string, error)
}

// Manager applies lifecycle actions to the database and the cluster.
type Manager struct {
	store *db.Store
	kube  *k8s.Client
	// content is optional; without it only the reference format is checked.
	content ContentValidator

	mu     sync.RWMutex
	status map[uuid.UUID]k8s.Status
}

func NewManager(store *db.Store, kube *k8s.Client) *Manager {
	return &Manager{store: store, kube: kube, status: map[uuid.UUID]k8s.Status{}}
}

func (m *Manager) Kube() *k8s.Client { return m.kube }

// SetContentValidator enables catalog checks for mods and plugins.
func (m *Manager) SetContentValidator(v ContentValidator) { m.content = v }

// checkAutoSleep rejects auto sleep when mc-router does not scale servers.
func (m *Manager) checkAutoSleep(s Settings) error {
	if s.AutoSleep && !m.kube.Options().AutoSleep {
		return invalid("auto sleep is not enabled on this installation")
	}
	return nil
}

// checkContent validates mod and plugin choices for a server type.
func (m *Manager) checkContent(ctx context.Context, serverType string, s *Settings) error {
	if len(s.ModrinthProjects) == 0 {
		return nil
	}
	if !SupportsContent(serverType) {
		return invalid("%s servers cannot use mods or plugins", serverType)
	}
	if m.content == nil {
		return nil
	}
	refs, err := m.content.Validate(ctx, serverType, s.ModrinthProjects)
	if err != nil {
		return invalid("%s", err.Error())
	}
	s.ModrinthProjects = refs
	return nil
}

// Status returns the last observed status, falling back to the stored state.
func (m *Manager) Status(s sqlc.Server) k8s.Status {
	m.mu.RLock()
	st, ok := m.status[s.ID]
	m.mu.RUnlock()
	if ok {
		return st
	}
	return k8s.Status{State: s.ObservedState, Message: s.StatusMessage}
}

// SetStatus stores a freshly observed status (used by the reconciler).
func (m *Manager) SetStatus(id uuid.UUID, st k8s.Status) {
	m.mu.Lock()
	m.status[id] = st
	m.mu.Unlock()
}

func (m *Manager) forgetStatus(id uuid.UUID) {
	m.mu.Lock()
	delete(m.status, id)
	m.mu.Unlock()
}

// Spec builds the k8s desired state for a server row and its plan.
func (m *Manager) Spec(s sqlc.Server, p sqlc.Plan) (k8s.ServerSpec, error) {
	settings, err := ParseSettings(s.Settings)
	if err != nil {
		return k8s.ServerSpec{}, fmt.Errorf("stored settings of %s: %w", s.Slug, err)
	}
	return k8s.ServerSpec{
		ID:            s.ID,
		OwnerID:       s.OwnerID,
		Slug:          s.Slug,
		Type:          s.Type,
		Version:       s.Version,
		CPURequestM:   p.CpuRequestM,
		CPULimitM:     p.CpuLimitM,
		MemoryLimitMB: p.MemoryLimitMb,
		StorageGB:     p.StorageGb,
		Running:       s.DesiredState == DesiredRunning,
		AutoSleep:     settings.AutoSleep,
		DisplayName:   s.DisplayName,
		Env:           settings.Env(),
	}, nil
}

// Limits returns the effective quota and current usage of a user.
func Limits(ctx context.Context, q *sqlc.Queries, userID uuid.UUID, exclude *uuid.UUID) (quota.Limits, quota.Usage, error) {
	def, err := q.GetDefaultQuota(ctx)
	if err != nil {
		return quota.Limits{}, quota.Usage{}, fmt.Errorf("default quota: %w", err)
	}
	var uq *sqlc.UserQuota
	row, err := q.GetUserQuota(ctx, userID)
	switch {
	case err == nil:
		uq = &row
	case !db.IsNotFound(err):
		return quota.Limits{}, quota.Usage{}, err
	}
	var ex uuid.NullUUID
	if exclude != nil {
		ex = uuid.NullUUID{UUID: *exclude, Valid: true}
	}
	usage, err := q.GetUserUsage(ctx, sqlc.GetUserUsageParams{OwnerID: userID, ExcludeID: ex})
	if err != nil {
		return quota.Limits{}, quota.Usage{}, err
	}
	return quota.Effective(def, uq), quota.UsageFromRow(usage), nil
}

// CreateRequest is the input of the create wizard.
type CreateRequest struct {
	Slug        string          `json:"slug"`
	DisplayName string          `json:"display_name"`
	Type        string          `json:"type"`
	Version     string          `json:"version"`
	PlanID      uuid.UUID       `json:"plan_id"`
	Settings    json.RawMessage `json:"settings"`
	AcceptEULA  bool            `json:"accept_eula"`
}

// SlugAvailable checks format, database, and leftover volumes.
func (m *Manager) SlugAvailable(ctx context.Context, slug string) error {
	if err := ValidateSlug(slug); err != nil {
		return err
	}
	exists, err := m.store.SlugExists(ctx, slug)
	if err != nil {
		return err
	}
	if exists {
		return ErrSlugTaken
	}
	blocked, err := m.kube.SlugBlocked(ctx, slug)
	if err != nil {
		return fmt.Errorf("check volume: %w", err)
	}
	if blocked {
		return ErrSlugTaken
	}
	return nil
}

// Create validates, checks the quota under a user lock, stores the server, then
// creates its resources. A failed k8s call marks the server as error; the
// reconciler retries it.
func (m *Manager) Create(ctx context.Context, owner uuid.UUID, req CreateRequest) (sqlc.Server, error) {
	req.Slug = strings.TrimSpace(req.Slug)
	if req.DisplayName == "" {
		req.DisplayName = req.Slug
	}
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if !req.AcceptEULA {
		return sqlc.Server{}, invalid("the Minecraft EULA must be accepted")
	}
	for _, err := range []error{ValidateDisplayName(req.DisplayName), ValidateType(req.Type), ValidateVersion(req.Version)} {
		if err != nil {
			return sqlc.Server{}, err
		}
	}
	settings, err := ParseSettings(req.Settings)
	if err != nil {
		return sqlc.Server{}, err
	}
	if err := m.checkContent(ctx, req.Type, &settings); err != nil {
		return sqlc.Server{}, err
	}
	if err := m.checkAutoSleep(settings); err != nil {
		return sqlc.Server{}, err
	}
	settingsJSON, err := json.Marshal(settings)
	if err != nil {
		return sqlc.Server{}, err
	}
	if err := m.SlugAvailable(ctx, req.Slug); err != nil {
		return sqlc.Server{}, err
	}

	var srv sqlc.Server
	var plan sqlc.Plan
	err = m.store.InTx(ctx, func(q *sqlc.Queries) error {
		if _, err := q.LockUser(ctx, owner); err != nil {
			return err
		}
		plan, err = q.GetPlan(ctx, req.PlanID)
		if db.IsNotFound(err) || (err == nil && !plan.Active) {
			return invalid("unknown or inactive plan")
		}
		if err != nil {
			return err
		}
		limits, usage, err := Limits(ctx, q, owner, nil)
		if err != nil {
			return err
		}
		if err := quota.Check(limits, usage, quota.ForCreate(plan)); err != nil {
			return err
		}
		srv, err = q.CreateServer(ctx, sqlc.CreateServerParams{
			OwnerID:      owner,
			Slug:         req.Slug,
			DisplayName:  req.DisplayName,
			PlanID:       plan.ID,
			Type:         req.Type,
			Version:      req.Version,
			Settings:     settingsJSON,
			DesiredState: DesiredRunning,
		})
		if isUniqueViolation(err) {
			return ErrSlugTaken
		}
		return err
	})
	if err != nil {
		return sqlc.Server{}, err
	}
	m.apply(ctx, srv, plan)
	return srv, nil
}

// apply pushes the server spec to the cluster and records failures.
func (m *Manager) apply(ctx context.Context, srv sqlc.Server, plan sqlc.Plan) {
	spec, err := m.Spec(srv, plan)
	if err == nil {
		err = m.kube.Ensure(ctx, spec)
	}
	if err != nil {
		slog.ErrorContext(ctx, "apply server resources failed", "server", srv.Slug, "err", err)
		st := k8s.Status{State: k8s.StateError, Message: err.Error()}
		m.SetStatus(srv.ID, st)
		if dbErr := m.store.SetObservedState(ctx, sqlc.SetObservedStateParams{ID: srv.ID, ObservedState: st.State, StatusMessage: st.Message}); dbErr != nil {
			slog.ErrorContext(ctx, "store server state failed", "err", dbErr)
		}
	}
}

// Start scales a stopped server up after checking CPU and memory quota.
func (m *Manager) Start(ctx context.Context, id uuid.UUID) (sqlc.Server, error) {
	var srv sqlc.Server
	var plan sqlc.Plan
	// wake is set when the server is already running, possibly asleep.
	wake := false
	err := m.store.InTx(ctx, func(q *sqlc.Queries) error {
		cur, err := q.GetServer(ctx, id)
		if err != nil {
			return err
		}
		if _, err := q.LockUser(ctx, cur.OwnerID); err != nil {
			return err
		}
		// Re-read after the lock so concurrent starts see each other.
		if cur, err = q.GetServer(ctx, id); err != nil {
			return err
		}
		if cur.DesiredState == DesiredDeleting {
			return invalid("server is being deleted")
		}
		if plan, err = q.GetPlan(ctx, cur.PlanID); err != nil {
			return err
		}
		if cur.DesiredState == DesiredRunning {
			srv = cur
			wake = true
			return nil
		}
		limits, usage, err := Limits(ctx, q, cur.OwnerID, &cur.ID)
		if err != nil {
			return err
		}
		if err := quota.Check(limits, usage, quota.ForStart(plan)); err != nil {
			return err
		}
		srv, err = q.SetDesiredState(ctx, sqlc.SetDesiredStateParams{ID: id, DesiredState: DesiredRunning})
		return err
	})
	if err != nil {
		return sqlc.Server{}, err
	}
	// A helper pod may hold the world volume.
	if err := m.kube.DeleteHelper(ctx, srv.Slug); err != nil {
		slog.WarnContext(ctx, "delete file helper failed", "server", srv.Slug, "err", err)
	}
	m.apply(ctx, srv, plan)
	if wake {
		if err := m.kube.Scale(ctx, srv.Slug, true); err != nil {
			slog.WarnContext(ctx, "wake up failed", "server", srv.Slug, "err", err)
		}
	}
	return srv, nil
}

// Stop scales a server to zero; the world stays on its volume.
func (m *Manager) Stop(ctx context.Context, id uuid.UUID) (sqlc.Server, error) {
	cur, err := m.store.GetServer(ctx, id)
	if err != nil {
		return sqlc.Server{}, err
	}
	if cur.DesiredState == DesiredDeleting {
		return sqlc.Server{}, invalid("server is being deleted")
	}
	srv, err := m.store.SetDesiredState(ctx, sqlc.SetDesiredStateParams{ID: id, DesiredState: DesiredStopped})
	if err != nil {
		return sqlc.Server{}, err
	}
	plan, err := m.store.GetPlan(ctx, srv.PlanID)
	if err != nil {
		return sqlc.Server{}, err
	}
	// Full apply: besides scaling down it turns off wake on join.
	m.apply(ctx, srv, plan)
	return srv, nil
}

// Restart recreates the pod, which also applies pending config changes.
func (m *Manager) Restart(ctx context.Context, srv sqlc.Server) error {
	if srv.DesiredState != DesiredRunning {
		return invalid("server is not running")
	}
	if m.Status(srv).State == k8s.StateSleeping {
		return invalid("server is sleeping, start it to wake it up")
	}
	return m.kube.Restart(ctx, srv.Slug)
}

// Delete marks the server and removes its resources. If cleanup fails the
// reconciler finishes it later.
func (m *Manager) Delete(ctx context.Context, id uuid.UUID, keepData bool) error {
	srv, err := m.store.MarkServerDeleting(ctx, sqlc.MarkServerDeletingParams{ID: id, KeepData: keepData})
	if err != nil {
		return err
	}
	if err := m.FinishDelete(ctx, srv); err != nil {
		slog.WarnContext(ctx, "delete cleanup failed, reconciler will retry", "server", srv.Slug, "err", err)
	}
	return nil
}

// FinishDelete removes cluster resources and then the database row.
func (m *Manager) FinishDelete(ctx context.Context, srv sqlc.Server) error {
	if err := m.kube.Delete(ctx, srv.Slug, srv.ID, srv.KeepData); err != nil {
		return err
	}
	if err := m.store.DeleteServer(ctx, srv.ID); err != nil {
		return err
	}
	m.forgetStatus(srv.ID)
	return nil
}

// UpdateRequest changes server config. Nil fields stay as they are.
type UpdateRequest struct {
	DisplayName *string         `json:"display_name"`
	Version     *string         `json:"version"`
	PlanID      *uuid.UUID      `json:"plan_id"`
	Settings    json.RawMessage `json:"settings"`
}

// Update stores config changes and applies them to the StatefulSet. Changes take
// effect on the next restart. Plan upgrades by non-admins must fit the quota.
func (m *Manager) Update(ctx context.Context, id uuid.UUID, req UpdateRequest, isAdmin bool) (sqlc.Server, error) {
	var srv sqlc.Server
	var plan sqlc.Plan
	err := m.store.InTx(ctx, func(q *sqlc.Queries) error {
		cur, err := q.GetServer(ctx, id)
		if err != nil {
			return err
		}
		if _, err := q.LockUser(ctx, cur.OwnerID); err != nil {
			return err
		}
		if cur, err = q.GetServer(ctx, id); err != nil {
			return err
		}
		if cur.DesiredState == DesiredDeleting {
			return invalid("server is being deleted")
		}
		params := sqlc.UpdateServerConfigParams{
			ID:          cur.ID,
			DisplayName: cur.DisplayName,
			PlanID:      cur.PlanID,
			Version:     cur.Version,
			Settings:    cur.Settings,
		}
		if req.DisplayName != nil {
			n := strings.TrimSpace(*req.DisplayName)
			if err := ValidateDisplayName(n); err != nil {
				return err
			}
			params.DisplayName = n
		}
		if req.Version != nil {
			if err := ValidateVersion(*req.Version); err != nil {
				return err
			}
			params.Version = *req.Version
		}
		if len(req.Settings) > 0 {
			s, err := ParseSettings(req.Settings)
			if err != nil {
				return err
			}
			if err := m.checkAutoSleep(s); err != nil {
				return err
			}
			// Only ask the catalog when the list changed.
			old, _ := ParseSettings(cur.Settings)
			if !slices.Equal(old.ModrinthProjects, s.ModrinthProjects) {
				if err := m.checkContent(ctx, cur.Type, &s); err != nil {
					return err
				}
			}
			if params.Settings, err = json.Marshal(s); err != nil {
				return err
			}
		}
		if plan, err = q.GetPlan(ctx, cur.PlanID); err != nil {
			return err
		}
		if req.PlanID != nil && *req.PlanID != cur.PlanID {
			next, err := q.GetPlan(ctx, *req.PlanID)
			if db.IsNotFound(err) || (err == nil && !next.Active) {
				return invalid("unknown or inactive plan")
			}
			if err != nil {
				return err
			}
			if next.StorageGb < plan.StorageGb {
				return invalid("the new plan has less storage than the current volume, volumes cannot shrink")
			}
			if !isAdmin {
				limits, usage, err := Limits(ctx, q, cur.OwnerID, &cur.ID)
				if err != nil {
					return err
				}
				// Usage excludes this server, so check its full new footprint.
				c := quota.Change{StorageGB: next.StorageGb}
				if cur.DesiredState == DesiredRunning {
					c.CPUM, c.MemoryMB = next.CpuLimitM, next.MemoryLimitMb
				}
				if err := quota.Check(limits, usage, c); err != nil {
					return err
				}
			}
			params.PlanID, plan = next.ID, next
		}
		srv, err = q.UpdateServerConfig(ctx, params)
		return err
	})
	if err != nil {
		return sqlc.Server{}, err
	}
	m.apply(ctx, srv, plan)
	return srv, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
