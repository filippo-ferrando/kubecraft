package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func valid() map[string]string {
	return map[string]string{
		"DATABASE_URL":   "postgres://x",
		"SESSION_SECRET": strings.Repeat("a", 32),
	}
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if c.MetricsAddr != ":9090" {
		t.Fatalf("metrics addr: %q", c.MetricsAddr)
	}
	if c.JVMHeapRatio != 0.75 || c.Namespace != "minecraft" || c.DefaultMaxServer != 2 || !c.CookieSecure {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if got := c.Origins(); len(got) != 1 || got[0] != "https://localhost:8080" {
		t.Fatalf("origins: %v", got)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]map[string]string{
		"missing db":       {"SESSION_SECRET": strings.Repeat("a", 32)},
		"short secret":     {"DATABASE_URL": "x", "SESSION_SECRET": "short"},
		"bad ratio":        merge(valid(), "JVM_HEAP_RATIO", "1.5"),
		"bad max servers":  merge(valid(), "DEFAULT_MAX_SERVERS", "-1"),
		"half bootstrap":   merge(valid(), "BOOTSTRAP_ADMIN_USER", "admin"),
		"bad log level":    merge(valid(), "LOG_LEVEL", "loud"),
		"bad cookie value": merge(valid(), "COOKIE_SECURE", "maybe"),
	}
	for name, m := range cases {
		if _, err := Load(env(m)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestMetricsDisabled(t *testing.T) {
	c, err := Load(env(merge(valid(), "METRICS_ADDR", "")))
	if err != nil || c.MetricsAddr != "" {
		t.Fatalf("empty METRICS_ADDR must disable metrics: %q %v", c.MetricsAddr, err)
	}
}

func TestLoadWithPGEnv(t *testing.T) {
	m := map[string]string{"PGHOST": "db", "SESSION_SECRET": strings.Repeat("a", 32)}
	if _, err := Load(env(m)); err != nil {
		t.Fatal(err)
	}
}

func TestOriginsInsecure(t *testing.T) {
	m := merge(valid(), "COOKIE_SECURE", "false")
	m["UI_HOST"] = "mine.example.com"
	m["ALLOWED_ORIGINS"] = "http://localhost:5173/, "
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	got := c.Origins()
	if len(got) != 2 || got[0] != "http://mine.example.com" || got[1] != "http://localhost:5173" {
		t.Fatalf("origins: %v", got)
	}
}

func merge(m map[string]string, k, v string) map[string]string {
	m[k] = v
	return m
}
