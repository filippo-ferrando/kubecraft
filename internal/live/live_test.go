package live

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	metricsapi "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/testutil"
)

func TestParseList(t *testing.T) {
	cases := []struct {
		in     string
		online int
		max    int
		names  []string
	}{
		{"There are 0 of a max of 20 players online: ", 0, 20, nil},
		{"There are 2 of a max of 20 players online: Steve, Alex", 2, 20, []string{"Steve", "Alex"}},
		{"§6There are §c1§6 out of maximum §c10", -1, 0, nil},
		{"There are 1/10 players online:\ndefault: Notch", 1, 10, []string{"Notch"}},
		{"There are 1 of a max 5 players online: §aSteve", 1, 5, []string{"Steve"}},
	}
	for _, c := range cases {
		p, ok := ParseList(c.in)
		if c.online < 0 {
			if ok {
				t.Errorf("%q: expected no match", c.in)
			}
			continue
		}
		if !ok || p.Online != c.online || p.Max != c.max || len(p.Names) != len(c.names) {
			t.Errorf("%q: got %+v %v", c.in, p, ok)
			continue
		}
		for i := range c.names {
			if p.Names[i] != c.names[i] {
				t.Errorf("%q: names %v", c.in, p.Names)
			}
		}
	}
}

func TestStripColors(t *testing.T) {
	if StripColors("§aHello §lWorld§r") != "Hello World" {
		t.Fatal("colors not stripped")
	}
}

func rconServer(t *testing.T, handler func(cmd string) string) string {
	return testutil.RCONServer(t, "secret", handler)
}

func TestPoolExec(t *testing.T) {
	srv := rconServer(t, func(cmd string) string { return "§aran " + cmd })
	var dials atomic.Int32
	pool := NewPool(func(string) string { return srv }, func(context.Context, string) (string, error) {
		dials.Add(1)
		return "secret", nil
	})
	t.Cleanup(pool.Close)
	ctx := context.Background()
	out, err := pool.Exec(ctx, "s", "/say hi")
	if err != nil || out != "ran say hi" {
		t.Fatalf("got %q %v", out, err)
	}
	if _, err := pool.Exec(ctx, "s", "list"); err != nil || dials.Load() != 1 {
		t.Fatal("connection not reused")
	}
	for _, bad := range []string{"", "  ", "a\nb", string(make([]byte, MaxCommandLen+1))} {
		if _, err := pool.Exec(ctx, "s", bad); err == nil {
			t.Errorf("command %q accepted", bad)
		}
	}
	pool.Forget("s")
	if _, err := pool.Exec(ctx, "s", "list"); err != nil || dials.Load() != 2 {
		t.Fatal("forget did not drop the connection")
	}
}

func TestPoolUnavailable(t *testing.T) {
	pool := NewPool(func(string) string { return "127.0.0.1:1" }, func(context.Context, string) (string, error) { return "x", nil })
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(), "s", "list"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected unavailable, got %v", err)
	}
	srv := rconServer(t, func(string) string { return "" })
	pool = NewPool(func(string) string { return srv }, func(context.Context, string) (string, error) { return "wrong", nil })
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(), "s", "list"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("wrong password: expected unavailable, got %v", err)
	}
}

func TestPoller(t *testing.T) {
	srv := rconServer(t, func(string) string { return "There are 1 of a max of 20 players online: Steve" })
	pool := NewPool(func(string) string { return srv }, func(context.Context, string) (string, error) { return "secret", nil })
	t.Cleanup(pool.Close)
	id := uuid.New()
	pm := &metricsapi.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{Name: "mc-a-0", Namespace: "minecraft", Labels: map[string]string{
			"app.kubernetes.io/managed-by": "mc-manager", "mc-manager/server-id": id.String(),
		}},
		Containers: []metricsapi.ContainerMetrics{{Name: "minecraft", Usage: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("250m"),
			corev1.ResourceMemory: resource.MustParse("1Gi"),
		}}},
	}
	mc := metricsfake.NewSimpleClientset()
	// The fake tracker would guess "podmetricses"; the real resource is "pods".
	gvr := schema.GroupVersionResource{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods"}
	if err := mc.Tracker().Create(gvr, pm, "minecraft"); err != nil {
		t.Fatal(err)
	}
	p := NewPoller(pool, mc, "minecraft", "app.kubernetes.io/managed-by=mc-manager", func(context.Context) ([]Target, error) {
		return []Target{{ID: id, Slug: "a"}}, nil
	})
	p.Once(context.Background())
	st, ok := p.Get(id)
	if !ok || st.Players == nil || st.Players.Online != 1 || st.Players.Names[0] != "Steve" {
		t.Fatalf("players: %+v", st)
	}
	if st.CPUM == nil || *st.CPUM != 250 || *st.MemoryMB != 1024 {
		t.Fatalf("usage: %+v", st)
	}
}

func TestPollerSync(t *testing.T) {
	var cmds []string
	var mu sync.Mutex
	srv := rconServer(t, func(cmd string) string {
		mu.Lock()
		cmds = append(cmds, cmd)
		mu.Unlock()
		return ""
	})
	pool := NewPool(func(string) string { return srv }, func(context.Context, string) (string, error) { return "secret", nil })
	t.Cleanup(pool.Close)
	start := time.Now()
	target := Target{ID: uuid.New(), Slug: "a", StartedAt: start, Sync: []string{"difficulty hard"}}
	p := NewPoller(pool, nil, "minecraft", "", func(context.Context) ([]Target, error) { return []Target{target}, nil })
	p.Once(context.Background())
	p.Once(context.Background())
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, c := range cmds {
			if c == "difficulty hard" {
				n++
			}
		}
		return n
	}
	if count() != 1 {
		t.Fatalf("sync should run once per pod, ran %d times", count())
	}
	// A new pod gets the settings again.
	target.StartedAt = start.Add(time.Minute)
	p.Once(context.Background())
	if count() != 2 {
		t.Fatalf("sync should rerun for a new pod, ran %d times", count())
	}
}
