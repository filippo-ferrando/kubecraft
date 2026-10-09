package auth

import (
	"sync"
	"time"
)

// RateLimiter counts failed logins per key in a fixed window.
type RateLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	hits   map[string]*window
}

type window struct {
	start time.Time
	count int
}

func NewRateLimiter(limit int, w time.Duration) *RateLimiter {
	return &RateLimiter{max: limit, window: w, now: time.Now, hits: map[string]*window{}}
}

// Allowed reports whether key may try again, and how long to wait if not.
func (r *RateLimiter) Allowed(key string) (bool, time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.hits[key]
	if !ok {
		return true, 0
	}
	elapsed := r.now().Sub(w.start)
	if elapsed >= r.window {
		delete(r.hits, key)
		return true, 0
	}
	if w.count >= r.max {
		return false, r.window - elapsed
	}
	return true, 0
}

// Fail records a failed attempt for key.
func (r *RateLimiter) Fail(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	w, ok := r.hits[key]
	if !ok || now.Sub(w.start) >= r.window {
		r.hits[key] = &window{start: now, count: 1}
		return
	}
	w.count++
}

// Reset clears key after a successful login.
func (r *RateLimiter) Reset(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.hits, key)
}

// Prune drops expired windows to bound memory.
func (r *RateLimiter) Prune() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	for k, w := range r.hits {
		if now.Sub(w.start) >= r.window {
			delete(r.hits, k)
		}
	}
}
