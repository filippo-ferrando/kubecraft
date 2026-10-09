package live

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

// Stats are the live numbers of a running server.
type Stats struct {
	Players   *Players  `json:"players,omitempty"`
	CPUM      *int64    `json:"cpu_m,omitempty"`
	MemoryMB  *int64    `json:"memory_mb,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Target is a running server to poll.
type Target struct {
	ID   uuid.UUID
	Slug string
	// StartedAt identifies the pod; Sync runs once for each new value.
	StartedAt time.Time
	Sync      []string
}

// Poller refreshes Stats for running servers in the background.
type Poller struct {
	pool      *Pool
	metrics   metricsv.Interface
	namespace string
	selector  string
	targets   func(ctx context.Context) ([]Target, error)

	mu    sync.RWMutex
	stats map[uuid.UUID]Stats
	// synced holds the pod start time whose settings were last applied.
	synced map[uuid.UUID]time.Time
}

// NewPoller creates a poller. metrics may be nil when metrics-server is not installed.
func NewPoller(pool *Pool, metrics metricsv.Interface, namespace, selector string, targets func(ctx context.Context) ([]Target, error)) *Poller {
	return &Poller{pool: pool, metrics: metrics, namespace: namespace, selector: selector, targets: targets, stats: map[uuid.UUID]Stats{}, synced: map[uuid.UUID]time.Time{}}
}

// Get returns the latest stats of a server, if any.
func (p *Poller) Get(id uuid.UUID) (Stats, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	s, ok := p.stats[id]
	return s, ok
}

// Run polls every interval until ctx ends.
func (p *Poller) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		p.Once(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Once runs a single polling pass.
func (p *Poller) Once(ctx context.Context) {
	targets, err := p.targets(ctx)
	if err != nil {
		slog.Debug("live poll: list targets failed", "err", err)
		return
	}
	usage := p.usage(ctx)

	next := make(map[uuid.UUID]Stats, len(targets))
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := Stats{UpdatedAt: time.Now()}
			if u, ok := usage[t.ID]; ok {
				st.CPUM, st.MemoryMB = &u.cpu, &u.mem
			}
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			p.sync(cctx, t)
			if out, err := p.pool.Exec(cctx, t.Slug, "list"); err == nil {
				if pl, ok := ParseList(out); ok {
					st.Players = &pl
				}
			}
			mu.Lock()
			next[t.ID] = st
			mu.Unlock()
		}()
	}
	wg.Wait()

	p.mu.Lock()
	p.stats = next
	// Forget servers that stopped or were deleted; they sync again on their next start.
	for id := range p.synced {
		if _, ok := next[id]; !ok {
			delete(p.synced, id)
		}
	}
	p.mu.Unlock()
}

// sync applies the target's live settings once per pod.
func (p *Poller) sync(ctx context.Context, t Target) {
	if len(t.Sync) == 0 {
		return
	}
	p.mu.RLock()
	done := p.synced[t.ID].Equal(t.StartedAt)
	p.mu.RUnlock()
	if done {
		return
	}
	if !ApplyAll(ctx, p.pool, t.Slug, t.Sync) {
		return
	}
	p.mu.Lock()
	p.synced[t.ID] = t.StartedAt
	p.mu.Unlock()
	slog.Debug("live settings applied", "server", t.Slug)
}

// ApplyAll runs commands in order. Rejected commands (like an unknown gamerule on old
// versions) are fine; it returns false only when RCON is unreachable.
func ApplyAll(ctx context.Context, pool *Pool, slug string, cmds []string) bool {
	for _, c := range cmds {
		if _, err := pool.Exec(ctx, slug, c); errors.Is(err, ErrUnavailable) {
			return false
		}
	}
	return true
}

type podUsage struct{ cpu, mem int64 }

func (p *Poller) usage(ctx context.Context) map[uuid.UUID]podUsage {
	out := map[uuid.UUID]podUsage{}
	if p.metrics == nil {
		return out
	}
	list, err := p.metrics.MetricsV1beta1().PodMetricses(p.namespace).List(ctx, metav1.ListOptions{LabelSelector: p.selector})
	if err != nil {
		slog.Debug("live poll: metrics unavailable", "err", err)
		return out
	}
	for _, pm := range list.Items {
		id, err := uuid.Parse(pm.Labels["mc-manager/server-id"])
		if err != nil {
			continue
		}
		var u podUsage
		for _, c := range pm.Containers {
			u.cpu += c.Usage.Cpu().MilliValue()
			u.mem += c.Usage.Memory().Value() / (1024 * 1024)
		}
		out[id] = u
	}
	return out
}
