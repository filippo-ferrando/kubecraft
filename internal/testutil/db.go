// Package testutil has helpers shared by integration tests.
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/db"
)

// DB returns a migrated store on a fresh database.
// Tests are skipped when TEST_DATABASE_URL is not set.
func DB(t *testing.T) *db.Store {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close(ctx)

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "kubecraft_test_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database: %v", err)
	}

	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	store, err := db.Open(ctx, u.String())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		store.Close()
		c, err := pgx.Connect(context.Background(), base)
		if err == nil {
			_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			_ = c.Close(context.Background())
		}
	})
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := store.Seed(ctx, 2); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return store
}
