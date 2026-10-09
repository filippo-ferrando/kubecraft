// Package reconcile keeps the cluster aligned with the database.
package reconcile

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/db"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/db/sqlc"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/k8s"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/metrics"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/servers"
)

// Reconciler periodically compares desired and observed state.
type Reconciler struct {
	store *db.Store
	mgr   *servers.Manager
	kube  *k8s.Client
}

func New(store *db.Store, mgr *servers.Manager) *Reconciler {
	return &Reconciler{store: store, mgr: mgr, kube: mgr.Kube()}
}

// Run reconciles every interval until ctx ends.
func (r *Reconciler) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := r.Once(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("reconcile failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Once runs a single reconcile pass.
func (r *Reconciler) Once(ctx context.Context) error {
	start := time.Now()
	err := r.once(ctx)
	metrics.ReconcileDuration.Observe(time.Since(start).Seconds())
	result := "ok"
	if err != nil {
		result = "error"
	}
	metrics.ReconcileRuns.WithLabelValues(result).Inc()
	return err
}

func (r *Reconciler) once(ctx context.Context) error {
	snap, err := r.kube.Snapshot(ctx)
	if err != nil {
		return err
	}
	rows, err := r.store.ListServers(ctx)
	if err != nil {
		return err
	}
	planList, err := r.store.ListPlans(ctx)
	if err != nil {
		return err
	}
	plans := make(map[uuid.UUID]sqlc.Plan, len(planList))
	for _, p := range planList {
		plans[p.ID] = p
	}

	known := make(map[uuid.UUID]bool, len(rows))
	for _, s := range rows {
		known[s.ID] = true
		r.server(ctx, s, plans[s.PlanID], snap)
	}
	r.orphans(ctx, snap, known)
	return nil
}

func (r *Reconciler) server(ctx context.Context, s sqlc.Server, plan sqlc.Plan, snap *k8s.Snapshot) {
	log := slog.With("server", s.Slug)
	if s.DesiredState == servers.DesiredDeleting {
		if err := r.mgr.FinishDelete(ctx, s); err != nil {
			log.Warn("delete cleanup failed", "err", err)
		}
		return
	}

	obs := snap.Servers[s.ID]
	var applyErr error
	if r.drifted(s, plan, obs, snap) {
		spec, err := r.mgr.Spec(s, plan)
		if err == nil {
			err = r.kube.Ensure(ctx, spec)
		}
		if err != nil {
			log.Warn("ensure resources failed", "err", err)
			metrics.ReconcileApplyErrors.Inc()
			applyErr = err
		} else {
			log.Debug("resources ensured")
		}
	}

	sleeping := false
	if spec, err := r.mgr.Spec(s, plan); err == nil {
		sleeping = r.kube.ReplicasManaged(spec)
	}
	st := k8s.ComputeStatus(s.DesiredState, sleeping, obs)
	r.cleanHelper(ctx, s, obs, snap)
	if applyErr != nil {
		st.State, st.Message = k8s.StateError, applyErr.Error()
	}
	r.mgr.SetStatus(s.ID, st)
	if err := r.store.SetObservedState(ctx, sqlc.SetObservedStateParams{ID: s.ID, ObservedState: st.State, StatusMessage: st.Message}); err != nil {
		log.Warn("store state failed", "err", err)
	}
}

// drifted reports whether any resource is missing or the StatefulSet differs from the spec.
func (r *Reconciler) drifted(s sqlc.Server, plan sqlc.Plan, obs *k8s.Observed, snap *k8s.Snapshot) bool {
	if obs == nil || obs.StatefulSet == nil {
		return true
	}
	if _, ok := snap.Services[s.ID]; !ok {
		return true
	}
	if _, ok := snap.Secrets[s.ID]; !ok {
		return true
	}
	if _, ok := snap.PVCs[s.ID]; !ok {
		return true
	}
	spec, err := r.mgr.Spec(s, plan)
	if err != nil {
		return true
	}
	wantPort := ""
	if spec.PlayitPort > 0 {
		wantPort = strconv.Itoa(spec.PlayitPort)
	}
	if snap.PlayitPorts[s.ID] != wantPort {
		return true
	}
	want := k8s.BuildStatefulSet(r.kube.Options(), spec)
	cur := obs.StatefulSet
	if cur.Annotations[k8s.AnnotationSpecHash] != want.Annotations[k8s.AnnotationSpecHash] {
		return true
	}
	if r.kube.ReplicasManaged(spec) {
		return false
	}
	return cur.Spec.Replicas == nil || *cur.Spec.Replicas != *want.Spec.Replicas
}

// cleanHelper removes the file helper pod once it finished or the server pod exists,
// so the server can mount its volume.
func (r *Reconciler) cleanHelper(ctx context.Context, s sqlc.Server, obs *k8s.Observed, snap *k8s.Snapshot) {
	h, ok := snap.Helpers[s.ID]
	if !ok {
		return
	}
	done := h.Status.Phase == corev1.PodSucceeded || h.Status.Phase == corev1.PodFailed
	if done || (obs != nil && obs.Pod != nil) {
		if err := r.kube.DeleteHelper(ctx, s.Slug); err != nil {
			slog.Warn("delete file helper failed", "server", s.Slug, "err", err)
		}
	}
}

// orphans removes resources of servers that no longer exist in the database.
// Volumes are retained rather than deleted, in case the database was restored.
func (r *Reconciler) orphans(ctx context.Context, snap *k8s.Snapshot, known map[uuid.UUID]bool) {
	seen := map[uuid.UUID]string{}
	for id, o := range snap.Servers {
		if o.StatefulSet != nil {
			seen[id] = o.StatefulSet.Labels[k8s.LabelSlug]
		}
	}
	for _, m := range []map[uuid.UUID]string{snap.Services, snap.Secrets, snap.PVCs} {
		for id, slug := range m {
			seen[id] = slug
		}
	}
	for id, h := range snap.Helpers {
		if !known[id] {
			if err := r.kube.DeleteHelper(ctx, h.Labels[k8s.LabelSlug]); err != nil {
				slog.Warn("orphan helper cleanup failed", "err", err)
			}
		}
	}
	for id, slug := range seen {
		if known[id] || slug == "" {
			continue
		}
		slog.Warn("removing orphaned server resources, keeping the volume", "slug", slug, "server_id", id)
		if err := r.kube.Delete(ctx, slug, id, true); err != nil {
			slog.Warn("orphan cleanup failed", "slug", slug, "err", err)
		}
	}
}
