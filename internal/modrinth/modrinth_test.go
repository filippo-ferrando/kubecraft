package modrinth

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/testutil"
)

func TestSearchFacets(t *testing.T) {
	queries := make(chan string, 4)
	c := New(testutil.FakeModrinth(t, queries), "kubecraft-test")
	ctx := context.Background()
	res, err := c.Search(ctx, "PAPER", "1.21.4", "perms", 0)
	if err != nil || len(res.Hits) != 1 || res.Hits[0].ID != "Vebnzrzj" {
		t.Fatalf("search: %+v %v", res, err)
	}
	q, _ := url.ParseQuery(<-queries)
	f := q.Get("facets")
	for _, want := range []string{"project_type:plugin", "categories:paper", "categories:bukkit", "server_side:required", "versions:1.21.4"} {
		if !strings.Contains(f, want) {
			t.Errorf("facets %s missing %s", f, want)
		}
	}
	// Cached: a second identical search does not hit the API.
	if _, err := c.Search(ctx, "PAPER", "1.21.4", "perms", 0); err != nil {
		t.Fatal(err)
	}
	select {
	case <-queries:
		t.Fatal("search was not cached")
	default:
	}
	if _, err := c.Search(ctx, "FABRIC", "", "", 0); err != nil {
		t.Fatal(err)
	}
	q, _ = url.ParseQuery(<-queries)
	if !strings.Contains(q.Get("facets"), "project_type:mod") || strings.Contains(q.Get("facets"), "versions:") {
		t.Fatalf("fabric facets: %s", q.Get("facets"))
	}
	if _, err := c.Search(ctx, "VANILLA", "", "", 0); err == nil {
		t.Fatal("vanilla search must fail")
	}
}

func TestValidate(t *testing.T) {
	c := New(testutil.FakeModrinth(t, nil), "kubecraft-test")
	ctx := context.Background()
	got, err := c.Validate(ctx, "PAPER", []string{"Vebnzrzj", "luckperms"})
	if err != nil || len(got) != 1 || got[0] != "luckperms" {
		t.Fatalf("got %v %v", got, err)
	}
	for _, tc := range []struct {
		typ  string
		refs []string
	}{
		{"PAPER", []string{"lithium"}},     // fabric only
		{"FABRIC", []string{"clientonly"}}, // client side
		{"FABRIC", []string{"missing"}},    // unknown
		{"VANILLA", []string{"luckperms"}}, // no loader
	} {
		if _, err := c.Validate(ctx, tc.typ, tc.refs); err == nil {
			t.Errorf("%s %v accepted", tc.typ, tc.refs)
		}
	}
	if _, err := New("http://127.0.0.1:1", "x").Validate(ctx, "PAPER", []string{"a1"}); err == nil {
		t.Fatal("unreachable catalog must fail")
	}
}
