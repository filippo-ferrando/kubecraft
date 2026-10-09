package auth

import (
	"strings"
	"testing"
	"time"
)

func TestHashAndVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected hash format: %s", h)
	}
	if ok, err := VerifyPassword("correct horse battery", h); err != nil || !ok {
		t.Fatalf("expected match, got %v %v", ok, err)
	}
	if ok, _ := VerifyPassword("wrong", h); ok {
		t.Fatal("expected mismatch")
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Fatal("hashes must use a random salt")
	}
}

func TestVerifyBadHash(t *testing.T) {
	for _, h := range []string{"", "plain", "$argon2i$v=19$m=1,t=1,p=1$aa$bb", "$argon2id$v=19$m=x$aa$bb"} {
		if _, err := VerifyPassword("x", h); err == nil {
			t.Errorf("expected error for %q", h)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if ValidatePassword("short") == nil {
		t.Error("short password accepted")
	}
	if ValidatePassword(strings.Repeat("a", MaxPasswordLen+1)) == nil {
		t.Error("long password accepted")
	}
	if err := ValidatePassword("long enough pw"); err != nil {
		t.Error(err)
	}
}

func TestValidateUsername(t *testing.T) {
	for _, u := range []string{"ab", "alice", "bob.smith", "a_1-2"} {
		if err := ValidateUsername(u); err != nil {
			t.Errorf("%q rejected: %v", u, err)
		}
	}
	for _, u := range []string{"", "a", "Alice", "-bob", "bob smith", strings.Repeat("a", 33)} {
		if ValidateUsername(u) == nil {
			t.Errorf("%q accepted", u)
		}
	}
}

func TestRateLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	r := NewRateLimiter(2, time.Minute)
	r.now = func() time.Time { return now }

	r.Fail("k")
	if ok, _ := r.Allowed("k"); !ok {
		t.Fatal("blocked after one failure")
	}
	r.Fail("k")
	ok, wait := r.Allowed("k")
	if ok || wait != time.Minute {
		t.Fatalf("expected block for 1m, got %v %v", ok, wait)
	}
	if ok, _ := r.Allowed("other"); !ok {
		t.Fatal("other key blocked")
	}
	now = now.Add(time.Minute)
	if ok, _ := r.Allowed("k"); !ok {
		t.Fatal("still blocked after window")
	}
	r.Fail("k")
	r.Reset("k")
	if ok, _ := r.Allowed("k"); !ok {
		t.Fatal("blocked after reset")
	}
	r.Fail("x")
	now = now.Add(2 * time.Minute)
	r.Prune()
	if len(r.hits) != 0 {
		t.Fatal("prune left entries")
	}
}
