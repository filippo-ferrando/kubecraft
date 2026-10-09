// Package config loads manager settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr string
	// FilesImage runs the per-server file sidecar; empty disables backups and file editing.
	FilesImage string
	// AutoSleep is true when mc-router auto scales servers; AutoSleepIdle is its idle time.
	AutoSleep     bool
	AutoSleepIdle string
	// playit.gg: the agent key (read only) used to find the tunnel of each server.
	PlayitSecret    string
	PlayitAPI       string
	PlayitFirstPort int
	// MetricsAddr serves /metrics on its own port; empty disables it.
	MetricsAddr      string
	DatabaseURL      string
	UIHost           string
	ServerBaseDomain string
	Namespace        string
	StorageClass     string
	MCImage          string
	MCImageTag       string
	JVMHeapRatio     float64
	SessionSecret    []byte
	SessionTTL       time.Duration
	BootstrapUser    string
	BootstrapPass    string
	DefaultMaxServer int
	CookieSecure     bool
	LogLevel         slog.Level
	// AllowedOrigins are extra origins accepted for CSRF and WebSocket checks.
	AllowedOrigins []string
	// ClientIPHeader holds the real client IP set by a trusted proxy, for example Cf-Connecting-Ip.
	ClientIPHeader string
	// InCluster is false when running outside k8s (dev mode, uses KUBECONFIG).
	InCluster  bool
	Kubeconfig string
}

// Load reads the config from the given lookup func (os.LookupEnv in production).
func Load(lookup func(string) (string, bool)) (*Config, error) {
	get := func(key, def string) string {
		if v, ok := lookup(key); ok && v != "" {
			return v
		}
		return def
	}
	var errs []error

	c := &Config{
		ListenAddr:       get("LISTEN_ADDR", ":8080"),
		DatabaseURL:      get("DATABASE_URL", ""),
		UIHost:           get("UI_HOST", "localhost:8080"),
		ServerBaseDomain: strings.TrimPrefix(get("SERVER_BASE_DOMAIN", "localhost"), "."),
		Namespace:        get("K8S_NAMESPACE", "minecraft"),
		StorageClass:     get("STORAGE_CLASS", ""),
		MCImage:          get("MC_IMAGE", "itzg/minecraft-server"),
		MCImageTag:       get("MC_IMAGE_TAG", "latest"),
		BootstrapUser:    get("BOOTSTRAP_ADMIN_USER", ""),
		BootstrapPass:    get("BOOTSTRAP_ADMIN_PASSWORD", ""),
		Kubeconfig:       get("KUBECONFIG", ""),
		FilesImage:       get("FILES_IMAGE", ""),
		AutoSleepIdle:    get("AUTO_SLEEP_IDLE", "15m"),
		PlayitSecret:     strings.TrimSpace(get("PLAYIT_SECRET", "")),
		PlayitAPI:        get("PLAYIT_API", "https://api.playit.gg"),
		ClientIPHeader:   strings.TrimSpace(get("CLIENT_IP_HEADER", "")),
	}

	// An empty URL makes pgx read the standard PG* variables instead.
	// Unlike other settings, an explicitly empty METRICS_ADDR disables metrics.
	c.MetricsAddr = ":9090"
	if v, ok := lookup("METRICS_ADDR"); ok {
		c.MetricsAddr = v
	}

	if _, ok := lookup("PGHOST"); c.DatabaseURL == "" && !ok {
		errs = append(errs, errors.New("DATABASE_URL or PGHOST is required"))
	}

	ratio, err := strconv.ParseFloat(get("JVM_HEAP_RATIO", "0.75"), 64)
	if err != nil || ratio < 0.3 || ratio > 0.95 {
		errs = append(errs, errors.New("JVM_HEAP_RATIO must be a number between 0.3 and 0.95"))
	}
	c.JVMHeapRatio = ratio

	secret := get("SESSION_SECRET", "")
	if len(secret) < 32 {
		errs = append(errs, errors.New("SESSION_SECRET must be at least 32 characters"))
	}
	c.SessionSecret = []byte(secret)

	ttl, err := time.ParseDuration(get("SESSION_TTL", "168h"))
	if err != nil || ttl < time.Minute {
		errs = append(errs, errors.New("SESSION_TTL must be a duration of at least 1m"))
	}
	c.SessionTTL = ttl

	maxServers, err := strconv.Atoi(get("DEFAULT_MAX_SERVERS", "2"))
	if err != nil || maxServers < 0 {
		errs = append(errs, errors.New("DEFAULT_MAX_SERVERS must be a non-negative integer"))
	}
	c.DefaultMaxServer = maxServers

	c.CookieSecure, err = strconv.ParseBool(get("COOKIE_SECURE", "true"))
	if err != nil {
		errs = append(errs, errors.New("COOKIE_SECURE must be a boolean"))
	}

	c.AutoSleep, err = strconv.ParseBool(get("AUTO_SLEEP", "false"))
	if err != nil {
		errs = append(errs, errors.New("AUTO_SLEEP must be a boolean"))
	}

	c.InCluster, err = strconv.ParseBool(get("IN_CLUSTER", "true"))
	if err != nil {
		errs = append(errs, errors.New("IN_CLUSTER must be a boolean"))
	}

	if err := c.LogLevel.UnmarshalText([]byte(get("LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL: %w", err))
	}

	for _, o := range strings.Split(get("ALLOWED_ORIGINS", ""), ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.AllowedOrigins = append(c.AllowedOrigins, strings.TrimSuffix(o, "/"))
		}
	}

	c.PlayitFirstPort, err = strconv.Atoi(get("PLAYIT_FIRST_PORT", "30001"))
	if err != nil || c.PlayitFirstPort < 1024 || c.PlayitFirstPort > 65535 {
		errs = append(errs, errors.New("PLAYIT_FIRST_PORT must be between 1024 and 65535"))
	}

	if (c.BootstrapUser == "") != (c.BootstrapPass == "") {
		errs = append(errs, errors.New("BOOTSTRAP_ADMIN_USER and BOOTSTRAP_ADMIN_PASSWORD must be set together"))
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return c, nil
}

// FromEnv loads the config from the process environment.
func FromEnv() (*Config, error) {
	return Load(os.LookupEnv)
}

// PlayitEnabled reports whether servers are published through playit.gg tunnels.
func (c *Config) PlayitEnabled() bool { return c.PlayitSecret != "" }

// Origins returns every origin allowed to send state-changing requests.
func (c *Config) Origins() []string {
	scheme := "https"
	if !c.CookieSecure {
		scheme = "http"
	}
	return append([]string{scheme + "://" + c.UIHost}, c.AllowedOrigins...)
}
