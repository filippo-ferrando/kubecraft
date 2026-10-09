// Command manager runs the kubecraft API, UI, and reconciler.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/api"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/auth"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/config"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/db"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/db/sqlc"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/files"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/k8s"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/live"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/metrics"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/modrinth"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/reconcile"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/servers"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/versions"
	"git.fferrando.cc/rdfilippo/kubecraft/web"
	"github.com/google/uuid"
	"k8s.io/client-go/kubernetes"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

var version = "dev"

func main() {
	// The same image runs the file sidecar next to each server.
	if len(os.Args) > 1 && os.Args[1] == "files" {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
		if err := files.Serve(envOr("FILES_ADDR", ":8081"), envOr("FILES_DIR", "/data"), os.Getenv("FILES_TOKEN")); err != nil {
			slog.Error("fatal", "err", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := openStore(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return err
	}
	if err := store.Seed(ctx, cfg.DefaultMaxServer); err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	if err := auth.Bootstrap(ctx, store, cfg.BootstrapUser, cfg.BootstrapPass); err != nil {
		return err
	}

	authSvc := auth.NewService(store, auth.LocalAuthenticator{Store: store}, cfg.SessionSecret, cfg.SessionTTL)
	go authSvc.Cleanup(ctx, 10*time.Minute)

	restCfg, err := k8s.RestConfig(cfg.InCluster, cfg.Kubeconfig)
	if err != nil {
		return fmt.Errorf("kubernetes config: %w", err)
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("kubernetes client: %w", err)
	}
	kube := k8s.New(cs, k8s.Options{
		Namespace:    cfg.Namespace,
		Image:        cfg.MCImage + ":" + cfg.MCImageTag,
		StorageClass: cfg.StorageClass,
		HeapRatio:    cfg.JVMHeapRatio,
		BaseDomain:   cfg.ServerBaseDomain,
		FilesImage:   cfg.FilesImage,
		AutoSleep:    cfg.AutoSleep,
	})
	mgr := servers.NewManager(store, kube)
	catalog := modrinth.New(modrinth.DefaultBaseURL, "kubecraft/"+version+" (git.fferrando.cc/rdfilippo/kubecraft)")
	mgr.SetContentValidator(catalog)
	go reconcile.New(store, mgr).Run(ctx, 5*time.Second)

	pool := live.NewPool(kube.RCONAddr, kube.RCONPassword)
	defer pool.Close()
	mc, err := metricsv.NewForConfig(restCfg)
	if err != nil {
		return fmt.Errorf("metrics client: %w", err)
	}
	poller := live.NewPoller(pool, mc, cfg.Namespace, k8s.LabelManagedBy+"="+k8s.ManagedByValue, func(ctx context.Context) ([]live.Target, error) {
		return runningTargets(ctx, store, mgr)
	})
	go poller.Run(ctx, 10*time.Second)

	if cfg.MetricsAddr != "" {
		reg := metrics.Registry(metricsSource(store, mgr, poller))
		go metrics.Serve(ctx, cfg.MetricsAddr, reg)
	}

	srv := &http.Server{
		Addr: cfg.ListenAddr,
		Handler: api.New(api.Deps{
			Config:   cfg,
			Store:    store,
			Auth:     authSvc,
			Mgr:      mgr,
			RCON:     pool,
			Live:     poller,
			Catalog:  catalog,
			Versions: versions.NewCache(versions.ManifestURL, time.Hour),
			Ready:    kube.Ping,
			UI:       web.Dist(),
		}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.ListenAddr, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// openStore retries for a while so the pod survives a slow database start.
func openStore(ctx context.Context, url string) (*db.Store, error) {
	var lastErr error
	for attempt := 0; attempt < 30; attempt++ {
		store, err := db.Open(ctx, url)
		if err == nil {
			return store, nil
		}
		lastErr = err
		slog.Warn("database not ready, retrying", "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, lastErr
}

// runningTargets lists servers that are up, for the live stats poller.
func runningTargets(ctx context.Context, store *db.Store, mgr *servers.Manager) ([]live.Target, error) {
	rows, err := store.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	var out []live.Target
	for _, s := range rows {
		st := mgr.Status(s)
		if s.DesiredState != servers.DesiredRunning || st.State != k8s.StateRunning {
			continue
		}
		t := live.Target{ID: s.ID, Slug: s.Slug}
		if st.StartedAt != nil {
			t.StartedAt = *st.StartedAt
		}
		if settings, err := servers.ParseSettings(s.Settings); err == nil {
			t.Sync = settings.LiveCommands()
		}
		out = append(out, t)
	}
	return out, nil
}

// metricsSource reads servers and users for the Prometheus collector.
func metricsSource(store *db.Store, mgr *servers.Manager, poller *live.Poller) metrics.Source {
	return func(ctx context.Context) ([]metrics.ServerInfo, []metrics.Users, error) {
		rows, err := store.ListServers(ctx)
		if err != nil {
			return nil, nil, err
		}
		plans, err := store.ListPlans(ctx)
		if err != nil {
			return nil, nil, err
		}
		users, err := store.ListUsers(ctx)
		if err != nil {
			return nil, nil, err
		}
		planByID := map[uuid.UUID]sqlc.Plan{}
		for _, p := range plans {
			planByID[p.ID] = p
		}
		owners := map[uuid.UUID]string{}
		userCounts := map[[2]string]int{}
		for _, u := range users {
			owners[u.ID] = u.Username
			userCounts[[2]string{u.Role, strconv.FormatBool(u.Disabled)}]++
		}
		out := make([]metrics.ServerInfo, 0, len(rows))
		for _, s := range rows {
			st := mgr.Status(s)
			p := planByID[s.PlanID]
			info := metrics.ServerInfo{
				Slug: s.Slug, Owner: owners[s.OwnerID], Type: s.Type, Version: s.Version, Plan: p.Name,
				DesiredRunning: s.DesiredState == servers.DesiredRunning, State: st.State, RestartRequired: st.RestartRequired,
				CPULimitM: p.CpuLimitM, MemoryLimitMB: p.MemoryLimitMb, StorageGB: p.StorageGb,
			}
			if live, ok := poller.Get(s.ID); ok && st.State == k8s.StateRunning {
				if live.Players != nil {
					info.PlayersOnline, info.PlayersMax = &live.Players.Online, &live.Players.Max
				}
				info.CPUUsageM, info.MemoryUsageMB = live.CPUM, live.MemoryMB
			}
			out = append(out, info)
		}
		var us []metrics.Users
		for k, n := range userCounts {
			us = append(us, metrics.Users{Role: k[0], Disabled: k[1] == "true", Count: n})
		}
		return out, us, nil
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
