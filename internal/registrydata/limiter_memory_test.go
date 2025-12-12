package registrydata

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMemoryProviderLimiter_Acquire_BurstBlockAndRefill(t *testing.T) {
	t.Parallel()

	limits := RateLimits{
		DefaultRatePerSec: 1.0,
		DefaultBurst:      2,
		DefaultBlock:      2 * time.Second,
	}
	l := newMemoryProviderLimiter(limits)

	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }

	ctx := context.Background()

	ok, retry, err := l.Acquire(ctx, "rdap.example")
	require.NoError(t, err)
	require.True(t, ok)
	require.Zero(t, retry)

	ok, retry, err = l.Acquire(ctx, "rdap.example")
	require.NoError(t, err)
	require.True(t, ok)
	require.Zero(t, retry)

	// out of tokens → blocked for DefaultBlock
	ok, retry, err = l.Acquire(ctx, "rdap.example")
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, 2*time.Second, retry)

	// still blocked; retry should decrease
	now = now.Add(500 * time.Millisecond)
	ok, retry, err = l.Acquire(ctx, "rdap.example")
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, 1500*time.Millisecond, retry)

	// after the block window, refill should allow again
	now = now.Add(3 * time.Second)
	ok, retry, err = l.Acquire(ctx, "rdap.example")
	require.NoError(t, err)
	require.True(t, ok)
	require.Zero(t, retry)
}

func TestMemoryProviderLimiter_BlockUntil_ExtendsBlockOnlyForward(t *testing.T) {
	t.Parallel()

	limits := RateLimits{
		DefaultRatePerSec: 100.0,
		DefaultBurst:      1,
		DefaultBlock:      1 * time.Second,
	}
	l := newMemoryProviderLimiter(limits)

	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }

	ctx := context.Background()
	until := now.Add(5 * time.Second)
	require.NoError(t, l.BlockUntil(ctx, "whois.example", until))

	// attempt to shorten the block should be ignored
	require.NoError(t, l.BlockUntil(ctx, "whois.example", now.Add(3*time.Second)))

	ok, retry, err := l.Acquire(ctx, "whois.example")
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, 5*time.Second, retry)

	now = until.Add(1 * time.Millisecond)
	ok, retry, err = l.Acquire(ctx, "whois.example")
	require.NoError(t, err)
	require.True(t, ok)
	require.Zero(t, retry)
}

func TestMemoryProviderLimiter_DefaultProviderKey(t *testing.T) {
	t.Parallel()

	l := newMemoryProviderLimiter(RateLimits{
		DefaultRatePerSec: 1.0,
		DefaultBurst:      1,
		DefaultBlock:      100 * time.Millisecond,
	})

	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }

	ok, _, err := l.Acquire(context.Background(), "")
	require.NoError(t, err)
	require.True(t, ok)

	l.mu.Lock()
	_, exists := l.buckets["default"]
	l.mu.Unlock()
	require.True(t, exists)
}

func TestMemoryProviderLimiter_ConcurrentAcquire_SingleWinner(t *testing.T) {
	t.Parallel()

	l := newMemoryProviderLimiter(RateLimits{
		DefaultRatePerSec: 0.0, // no refill
		DefaultBurst:      1,
		DefaultBlock:      5 * time.Second,
	})

	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }

	const n = 25
	start := make(chan struct{})
	type res struct {
		ok    bool
		retry time.Duration
		err   error
	}
	results := make(chan res, n)

	for i := 0; i < n; i++ {
		go func() {
			<-start
			ok, retry, err := l.Acquire(context.Background(), "provider")
			results <- res{ok: ok, retry: retry, err: err}
		}()
	}

	close(start)

	okCount := 0
	for i := 0; i < n; i++ {
		r := <-results
		require.NoError(t, r.err)
		if r.ok {
			okCount++
			require.Zero(t, r.retry)
		} else {
			require.Equal(t, 5*time.Second, r.retry)
		}
	}
	require.Equal(t, 1, okCount, "expected exactly one successful Acquire")
}
