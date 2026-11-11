// Package ratelimit implements simple keyed token buckets.
package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter allows `burst` events immediately and refills `rate` tokens per
// second. Idle buckets are dropped lazily so memory stays bounded.
type Limiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	buckets map[string]*bucket
	sweep   time.Time
	now     func() time.Time
}

func New(rate float64, burst int) *Limiter {
	return &Limiter{rate: rate, burst: float64(burst), buckets: map[string]*bucket{}, now: time.Now}
}

// PerMinute is a convenience constructor: n events per minute, burst n.
func PerMinute(n int) *Limiter { return New(float64(n)/60, n) }

// PerHour: n events per hour, burst n.
func PerHour(n int) *Limiter { return New(float64(n)/3600, n) }

// Allow consumes one token for key; false if the bucket is empty.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.sweep) > time.Minute {
		full := time.Duration(l.burst / l.rate * float64(time.Second))
		for k, b := range l.buckets {
			if now.Sub(b.last) > full {
				delete(l.buckets, k)
			}
		}
		l.sweep = now
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
