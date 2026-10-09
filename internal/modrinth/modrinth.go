// Package modrinth searches the Modrinth catalog for mods and plugins. Nothing is
// stored locally: servers download the chosen projects themselves on start.
package modrinth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"
)

const DefaultBaseURL = "https://api.modrinth.com/v2"

// Kind of content a server type accepts.
const (
	KindPlugin = "plugin"
	KindMod    = "mod"
)

// loaders lists the Modrinth categories each server type can load.
var loaders = map[string][]string{
	"PAPER":    {"paper", "spigot", "bukkit"},
	"PURPUR":   {"purpur", "paper", "spigot", "bukkit"},
	"FABRIC":   {"fabric"},
	"FORGE":    {"forge"},
	"NEOFORGE": {"neoforge"},
}

// KindFor returns plugin, mod, or "" when the server type takes neither.
func KindFor(serverType string) string {
	switch serverType {
	case "PAPER", "PURPUR":
		return KindPlugin
	case "FABRIC", "FORGE", "NEOFORGE":
		return KindMod
	}
	return ""
}

var projectRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{2,64}$`)

// ValidProjectRef reports whether s looks like a Modrinth slug or id.
func ValidProjectRef(s string) bool { return projectRe.MatchString(s) }

// Project is the part of a Modrinth project the UI shows.
type Project struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	IconURL     string   `json:"icon_url"`
	Downloads   int64    `json:"downloads"`
	Categories  []string `json:"categories"`
	ServerSide  string   `json:"server_side"`
}

type SearchResult struct {
	Hits      []Project `json:"hits"`
	TotalHits int       `json:"total_hits"`
	Offset    int       `json:"offset"`
}

// Client talks to the Modrinth API with a small response cache.
type Client struct {
	base   string
	ua     string
	http   *http.Client
	ttl    time.Duration
	mu     sync.Mutex
	cache  map[string]cached
	maxLen int
}

type cached struct {
	at   time.Time
	body []byte
}

func New(base, userAgent string) *Client {
	return &Client{
		base:   base,
		ua:     userAgent,
		http:   &http.Client{Timeout: 10 * time.Second},
		ttl:    10 * time.Minute,
		cache:  map[string]cached{},
		maxLen: 500,
	}
}

// ErrUnavailable means the catalog could not be reached.
var ErrUnavailable = errors.New("the Modrinth catalog is not reachable right now")

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	u := c.base + path + "?" + q.Encode()
	c.mu.Lock()
	if e, ok := c.cache[u]; ok && time.Since(e.at) < c.ttl {
		c.mu.Unlock()
		return json.Unmarshal(e.body, out)
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	// Modrinth asks clients to identify themselves.
	req.Header.Set("User-Agent", c.ua)
	res, err := c.http.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%w (status %d)", ErrUnavailable, res.StatusCode)
	}
	var raw json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(nil, res.Body, 4<<20)).Decode(&raw); err != nil {
		return ErrUnavailable
	}
	c.mu.Lock()
	if len(c.cache) >= c.maxLen {
		c.cache = map[string]cached{}
	}
	c.cache[u] = cached{at: time.Now(), body: raw}
	c.mu.Unlock()
	return json.Unmarshal(raw, out)
}

// Search finds server-side projects for a server type and game version.
// gameVersion can be empty to skip the version filter.
func (c *Client) Search(ctx context.Context, serverType, gameVersion, query string, offset int) (SearchResult, error) {
	kind := KindFor(serverType)
	if kind == "" {
		return SearchResult{}, errors.New("this server type does not support mods or plugins")
	}
	loaderFacet := []string{}
	for _, l := range loaders[serverType] {
		loaderFacet = append(loaderFacet, "categories:"+l)
	}
	facets := [][]string{
		{"project_type:" + kind},
		loaderFacet,
		{"server_side:required", "server_side:optional"},
	}
	if gameVersion != "" {
		facets = append(facets, []string{"versions:" + gameVersion})
	}
	fj, err := json.Marshal(facets)
	if err != nil {
		return SearchResult{}, err
	}
	q := url.Values{}
	q.Set("facets", string(fj))
	q.Set("limit", "20")
	q.Set("offset", strconv.Itoa(max(0, offset)))
	if query != "" {
		q.Set("query", query)
		q.Set("index", "relevance")
	} else {
		q.Set("index", "downloads")
	}
	var r struct {
		Hits []struct {
			Project
			ProjectID string `json:"project_id"`
		} `json:"hits"`
		TotalHits int `json:"total_hits"`
		Offset    int `json:"offset"`
	}
	if err := c.get(ctx, "/search", q, &r); err != nil {
		return SearchResult{}, err
	}
	out := SearchResult{TotalHits: r.TotalHits, Offset: r.Offset, Hits: make([]Project, 0, len(r.Hits))}
	for _, h := range r.Hits {
		p := h.Project
		p.ID = h.ProjectID
		out.Hits = append(out.Hits, p)
	}
	return out, nil
}

// Projects looks up projects by slug or id. Unknown ones are left out.
func (c *Client) Projects(ctx context.Context, refs []string) ([]Project, error) {
	if len(refs) == 0 {
		return []Project{}, nil
	}
	ids, err := json.Marshal(refs)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Project
		Loaders []string `json:"loaders"`
	}
	if err := c.get(ctx, "/projects", url.Values{"ids": {string(ids)}}, &raw); err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(raw))
	for _, p := range raw {
		pr := p.Project
		pr.Categories = p.Loaders
		out = append(out, pr)
	}
	return out, nil
}

// Validate checks that every ref exists and can run on serverType.
// It returns the refs normalized to project slugs.
func (c *Client) Validate(ctx context.Context, serverType string, refs []string) ([]string, error) {
	if len(refs) == 0 {
		return []string{}, nil
	}
	if KindFor(serverType) == "" {
		return nil, errors.New("this server type does not support mods or plugins")
	}
	projects, err := c.Projects(ctx, refs)
	if err != nil {
		return nil, err
	}
	bySlugOrID := map[string]Project{}
	for _, p := range projects {
		bySlugOrID[p.Slug] = p
		bySlugOrID[p.ID] = p
	}
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		p, ok := bySlugOrID[r]
		if !ok {
			return nil, fmt.Errorf("%q was not found on Modrinth", r)
		}
		if p.ServerSide == "unsupported" {
			return nil, fmt.Errorf("%s does not run on servers", p.Title)
		}
		if !slices.ContainsFunc(loaders[serverType], func(l string) bool { return slices.Contains(p.Categories, l) }) {
			return nil, fmt.Errorf("%s is not available for %s", p.Title, serverType)
		}
		if !slices.Contains(out, p.Slug) {
			out = append(out, p.Slug)
		}
	}
	return out, nil
}
