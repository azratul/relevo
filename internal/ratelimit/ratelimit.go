// Package ratelimit implements keyed token buckets with bounded memory.
package ratelimit

import (
	"math"
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter is a keyed token bucket: rate tokens/second, up to burst.
type Limiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	maxKeys int
	m       map[string]*bucket
	now     func() time.Time
}

func New(rate float64, burst int, maxKeys int) *Limiter {
	return &Limiter{rate: rate, burst: float64(burst), maxKeys: maxKeys, m: map[string]*bucket{}, now: time.Now}
}

// Allow consumes one token for key. It returns the wait before the next token
// when denied.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.m[key]
	if !ok {
		if len(l.m) >= l.maxKeys {
			l.gcLocked(now)
			if len(l.m) >= l.maxKeys {
				return false, time.Second // table full: fail closed
			}
		}
		b = &bucket{tokens: l.burst, last: now}
		l.m[key] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
}

// gcLocked drops buckets that are full again (idle keys).
func (l *Limiter) gcLocked(now time.Time) {
	for k, b := range l.m {
		if b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst {
			delete(l.m, k)
		}
	}
}

// GC removes idle keys; call periodically.
func (l *Limiter) GC() {
	l.mu.Lock()
	l.gcLocked(l.now())
	l.mu.Unlock()
}

// Backoff tracks consecutive failures per key with exponential lockout.
type Backoff struct {
	mu        sync.Mutex
	free      int           // failures allowed before lockout
	base, max time.Duration // lockout grows base*2^(n-free) up to max
	m         map[string]*failState
	maxKeys   int
	now       func() time.Time
}

type failState struct {
	n           int
	lockedUntil time.Time
	last        time.Time
}

func NewBackoff(free int, base, max time.Duration, maxKeys int) *Backoff {
	return &Backoff{free: free, base: base, max: max, m: map[string]*failState{}, maxKeys: maxKeys, now: time.Now}
}

// Check returns remaining lockout for key (0 if allowed).
func (b *Backoff) Check(key string) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.m[key]; ok {
		if d := s.lockedUntil.Sub(b.now()); d > 0 {
			return d
		}
	}
	return 0
}

// Fail records a failure.
func (b *Backoff) Fail(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	s, ok := b.m[key]
	if !ok {
		if len(b.m) >= b.maxKeys {
			for k, v := range b.m {
				if now.Sub(v.last) > b.max*2 {
					delete(b.m, k)
				}
			}
		}
		s = &failState{}
		b.m[key] = s
	}
	if now.Sub(s.last) > b.max*2 {
		s.n = 0 // forget old failures
	}
	s.n++
	s.last = now
	if s.n > b.free {
		d := b.base << min(s.n-b.free-1, 20)
		if d > b.max || d <= 0 {
			d = b.max
		}
		s.lockedUntil = now.Add(d)
	}
}

// Success clears the key.
func (b *Backoff) Success(key string) {
	b.mu.Lock()
	delete(b.m, key)
	b.mu.Unlock()
}
