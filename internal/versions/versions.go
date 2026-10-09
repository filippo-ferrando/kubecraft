// Package versions fetches and caches the Minecraft version list from Mojang.
package versions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const ManifestURL = "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json"

type Version struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"`
	ReleaseTime time.Time `json:"release_time"`
}

type List struct {
	Latest   string    `json:"latest"`
	Versions []Version `json:"versions"`
}

// Cache holds the version list for ttl and keeps serving stale data if Mojang is down.
type Cache struct {
	url    string
	client *http.Client
	ttl    time.Duration

	mu      sync.Mutex
	list    *List
	fetched time.Time
}

func NewCache(url string, ttl time.Duration) *Cache {
	return &Cache{url: url, ttl: ttl, client: &http.Client{Timeout: 10 * time.Second}}
}

// Get returns releases only, newest first.
func (c *Cache) Get(ctx context.Context) (*List, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.list != nil && time.Since(c.fetched) < c.ttl {
		return c.list, nil
	}
	l, err := c.fetch(ctx)
	if err != nil {
		if c.list != nil {
			return c.list, nil
		}
		return nil, err
	}
	c.list, c.fetched = l, time.Now()
	return l, nil
}

func (c *Cache) fetch(ctx context.Context) (*List, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch version manifest: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch version manifest: status %d", res.StatusCode)
	}
	var m struct {
		Latest struct {
			Release string `json:"release"`
		} `json:"latest"`
		Versions []struct {
			ID          string    `json:"id"`
			Type        string    `json:"type"`
			ReleaseTime time.Time `json:"releaseTime"`
		} `json:"versions"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, res.Body, 8<<20)).Decode(&m); err != nil {
		return nil, fmt.Errorf("decode version manifest: %w", err)
	}
	out := &List{Latest: m.Latest.Release, Versions: []Version{}}
	for _, v := range m.Versions {
		if v.Type == "release" {
			out.Versions = append(out.Versions, Version{ID: v.ID, Type: v.Type, ReleaseTime: v.ReleaseTime})
		}
	}
	return out, nil
}
