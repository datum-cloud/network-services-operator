package registrydata

import (
	"context"
	"sync"
	"time"
)

type memoryProviderLimiter struct {
	mu       sync.Mutex
	now      func() time.Time
	limits   RateLimits
	buckets  map[string]*memBucket
	stateTTL time.Duration
}

type memBucket struct {
	tokens       float64
	lastRefill   time.Time
	blockedUntil time.Time
	lastTouched  time.Time
}

func newMemoryProviderLimiter(limits RateLimits) *memoryProviderLimiter {
	return &memoryProviderLimiter{
		now:      time.Now,
		limits:   limits,
		buckets:  make(map[string]*memBucket),
		stateTTL: 30 * time.Minute,
	}
}

func (l *memoryProviderLimiter) Acquire(_ context.Context, provider string) (bool, time.Duration, error) {
	if provider == "" {
		provider = "default"
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.buckets[provider]
	if b == nil {
		b = &memBucket{tokens: l.limits.DefaultBurst, lastRefill: now}
		l.buckets[provider] = b
	}
	b.lastTouched = now

	// opportunistic cleanup
	for k, v := range l.buckets {
		if l.stateTTL > 0 && !v.lastTouched.IsZero() && now.Sub(v.lastTouched) > l.stateTTL {
			delete(l.buckets, k)
		}
	}

	if !b.blockedUntil.IsZero() && b.blockedUntil.After(now) {
		return false, b.blockedUntil.Sub(now), nil
	}

	// refill
	delta := now.Sub(b.lastRefill)
	if delta < 0 {
		delta = 0
	}
	b.tokens = minF(l.limits.DefaultBurst, b.tokens+(delta.Seconds()*l.limits.DefaultRatePerSec))
	b.lastRefill = now

	if b.tokens >= 1 {
		b.tokens -= 1
		return true, 0, nil
	}

	retry := l.limits.DefaultBlock
	if retry <= 0 {
		retry = 2 * time.Second
	}
	b.blockedUntil = now.Add(retry)
	return false, retry, nil
}

func (l *memoryProviderLimiter) BlockUntil(_ context.Context, provider string, until time.Time) error {
	if provider == "" {
		provider = "default"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[provider]
	if b == nil {
		b = &memBucket{tokens: l.limits.DefaultBurst, lastRefill: now}
		l.buckets[provider] = b
	}
	b.lastTouched = now
	if b.blockedUntil.Before(until) {
		b.blockedUntil = until
	}
	return nil
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
