package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestRateLimitFailureBudget(t *testing.T) {
	lim := newIPLimiter(5, 15*time.Minute)
	h := apiKeyMiddleware("good", lim, okHandler())

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-API-Key", "wrong")
		req.RemoteAddr = "10.0.0.1:1111"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: want 401, got %d", i+1, w.Code)
		}
	}
	// 6th
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "wrong")
	req.RemoteAddr = "10.0.0.1:1111"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("6th: want 429, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatalf("expected Retry-After header on 429")
	}
}

func TestRateLimitPerIPIsolation(t *testing.T) {
	lim := newIPLimiter(5, 15*time.Minute)
	h := apiKeyMiddleware("good", lim, okHandler())

	// Exhaust IP A
	for i := 0; i < 6; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-API-Key", "wrong")
		req.RemoteAddr = "10.0.0.1:9000"
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	// IP B unaffected
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-API-Key", "wrong")
	req.RemoteAddr = "10.0.0.2:9000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("IP B 1st failure: want 401, got %d", w.Code)
	}
}

func TestRateLimitSuccessDoesNotConsume(t *testing.T) {
	lim := newIPLimiter(2, 15*time.Minute) // tight budget on purpose
	h := apiKeyMiddleware("good", lim, okHandler())

	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-API-Key", "good")
		req.RemoteAddr = "10.0.0.3:1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("success %d: want 200, got %d", i+1, w.Code)
		}
	}
	// Now confirm bucket is fresh: 2 failures still return 401 (not immediately 429)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-API-Key", "wrong")
		req.RemoteAddr = "10.0.0.3:1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d after successes: want 401, got %d", i+1, w.Code)
		}
	}
}

func TestRateLimitGCRemovesIdleBuckets(t *testing.T) {
	lim := newIPLimiter(5, 15*time.Minute)
	now := time.Now()
	lim.now = func() time.Time { return now }

	// Create buckets for two IPs
	lim.check("10.0.0.10")
	lim.check("10.0.0.11")
	if got := bucketCount(lim); got != 2 {
		t.Fatalf("setup: expected 2 buckets, got %d", got)
	}

	// Advance past idle threshold
	advanced := now.Add(2 * lim.idle)
	lim.gc(advanced)
	if got := bucketCount(lim); got != 0 {
		t.Fatalf("after gc: expected 0 buckets, got %d", got)
	}
}

func bucketCount(l *ipLimiter) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
