package main

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"
)

// ipLimiter is a per-IP token bucket used to back off brute-force API key guesses.
// Buckets are populated only on auth failure, so legitimate traffic never touches
// the map. A background GC drops buckets that have been idle long enough that the
// attacker has effectively given up.
type ipLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	capacity float64
	interval time.Duration // time to refill one token
	idle     time.Duration // delete buckets idle longer than this
	now      func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// newIPLimiter creates a limiter that allows `capacity` failures per `window`,
// modeled as a token bucket refilled at `window/capacity` per token.
func newIPLimiter(capacity int, window time.Duration) *ipLimiter {
	if capacity < 1 {
		capacity = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	return &ipLimiter{
		buckets:  make(map[string]*bucket),
		capacity: float64(capacity),
		interval: window / time.Duration(capacity),
		idle:     3 * window,
		now:      time.Now,
	}
}

// check consumes a token for ip. Returns (true, 0) if the failure is within
// budget, or (false, retryAfter) if the IP has exhausted its budget.
func (l *ipLimiter) check(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[ip]
	if !ok {
		b = &bucket{tokens: l.capacity, last: now}
		l.buckets[ip] = b
	} else {
		elapsed := now.Sub(b.last)
		if elapsed > 0 {
			b.tokens += float64(elapsed) / float64(l.interval)
			if b.tokens > l.capacity {
				b.tokens = l.capacity
			}
		}
		b.last = now
	}
	if b.tokens < 1 {
		deficit := 1 - b.tokens
		retry := time.Duration(deficit * float64(l.interval))
		return false, retry
	}
	b.tokens -= 1
	return true, 0
}

func (l *ipLimiter) gc(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for ip, b := range l.buckets {
		if now.Sub(b.last) > l.idle {
			delete(l.buckets, ip)
		}
	}
}

// runGC ticks every interval and prunes idle buckets until ctx is done.
func (l *ipLimiter) runGC(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			l.gc(now)
		}
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
