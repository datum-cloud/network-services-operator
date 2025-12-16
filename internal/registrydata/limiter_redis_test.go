package registrydata

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRedisProviderLimiter_KeyPrefixing(t *testing.T) {
	t.Parallel()

	_, client := newTestRedis(t)
	l := newRedisProviderLimiter(client, "pfx:",
		RateLimits{DefaultRatePerSec: 1,
			DefaultBurst: 1,
			DefaultBlock: 1 * time.Second})
	require.Equal(t, "pfx:rl:rdap.example", l.key("rdap.example"))
	require.Equal(t, "pfx:rl:default", l.key(""))
}

func TestRedisProviderLimiter_Script_AcquireBlockAndRefill(t *testing.T) {
	t.Parallel()

	_, client := newTestRedis(t)
	l := newRedisProviderLimiter(client, "pfx:",
		RateLimits{DefaultRatePerSec: 1.0,
			DefaultBurst: 2,
			DefaultBlock: 2 * time.Second})
	l.stateTTL = 10 * time.Minute

	stateTTLms := l.stateTTL.Milliseconds()
	defaultBlockMs := l.limits.DefaultBlock.Milliseconds()

	provider := "rdap.example"
	key := l.key(provider)

	runAcquire := func(nowMs int64) (ok bool, retryMs int64) {
		t.Helper()
		res, err := l.acquireScript.Run(l.client, []string{key},
			nowMs,
			l.limits.DefaultRatePerSec,
			l.limits.DefaultBurst,
			defaultBlockMs,
			stateTTLms,
		).Result()
		require.NoError(t, err)
		arr, okArr := res.([]interface{})
		require.True(t, okArr)
		require.GreaterOrEqual(t, len(arr), 2)
		okNum, okConv := toInt64(arr[0])
		require.True(t, okConv)
		retryNum, okConv := toInt64(arr[1])
		require.True(t, okConv)
		return okNum == 1, retryNum
	}

	// Two tokens available initially.
	ok, retry := runAcquire(1_000)
	require.True(t, ok)
	require.Equal(t, int64(0), retry)

	ok, retry = runAcquire(1_000)
	require.True(t, ok)
	require.Equal(t, int64(0), retry)

	// Third attempt blocks for defaultBlock.
	ok, retry = runAcquire(1_000)
	require.False(t, ok)
	require.Equal(t, defaultBlockMs, retry)

	// Still blocked: remaining time is returned.
	ok, retry = runAcquire(1_000 + defaultBlockMs - 10)
	require.False(t, ok)
	require.Equal(t, int64(10), retry)

	// After block + refill time (2 seconds since last_refill at t=1000ms), should allow again.
	ok, retry = runAcquire(3_000)
	require.True(t, ok)
	require.Equal(t, int64(0), retry)
}

func TestRedisProviderLimiter_Script_BlockUntil(t *testing.T) {
	t.Parallel()

	_, client := newTestRedis(t)
	l := newRedisProviderLimiter(client, "pfx:",
		RateLimits{DefaultRatePerSec: 10.0,
			DefaultBurst: 1,
			DefaultBlock: 250 * time.Millisecond})
	l.stateTTL = 10 * time.Minute

	stateTTLms := l.stateTTL.Milliseconds()
	untilMs := int64(5_000)
	provider := "whois.example"

	_, err := l.blockScript.Run(l.client, []string{l.key(provider)}, untilMs, stateTTLms).Result()
	require.NoError(t, err)

	res, err := l.acquireScript.Run(l.client, []string{l.key(provider)},
		int64(1_000),
		l.limits.DefaultRatePerSec,
		l.limits.DefaultBurst,
		l.limits.DefaultBlock.Milliseconds(),
		stateTTLms,
	).Result()
	require.NoError(t, err)

	arr := res.([]interface{})
	okNum, _ := toInt64(arr[0])
	retryNum, _ := toInt64(arr[1])
	require.Equal(t, int64(0), okNum)
	require.Equal(t, untilMs-int64(1_000), retryNum)
}

func TestRedisProviderLimiter_AcquireWrapper_Smoke(t *testing.T) {
	t.Parallel()

	_, client := newTestRedis(t)
	l := newRedisProviderLimiter(client, "",
		RateLimits{DefaultRatePerSec: 100.0,
			DefaultBurst: 2,
			DefaultBlock: 50 * time.Millisecond})

	ok, retry, err := l.Acquire(context.Background(), "rdap.example")
	require.NoError(t, err)
	require.True(t, ok)
	require.Zero(t, retry)
}

func TestRedisProviderLimiter_ConcurrentAcquire_SingleWinner(t *testing.T) {
	t.Parallel()

	_, client := newTestRedis(t)
	l := newRedisProviderLimiter(client, "pfx:", RateLimits{
		DefaultRatePerSec: 0.0, // no refill
		DefaultBurst:      1,
		DefaultBlock:      5 * time.Second,
	})

	const n = 25
	start := make(chan struct{})
	type res struct {
		ok    bool
		retry time.Duration
		err   error
	}
	results := make(chan res, n)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			ok, retry, err := l.Acquire(context.Background(), "provider")
			results <- res{ok: ok, retry: retry, err: err}
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	okCount := 0
	for r := range results {
		require.NoError(t, r.err)
		if r.ok {
			okCount++
			require.Zero(t, r.retry)
		} else {
			// Redis implementation uses wall-clock time per Acquire(); allow a small
			// tolerance above DefaultBlock to avoid flakes under heavy scheduling.
			require.Greater(t, r.retry, 0*time.Second)
			require.LessOrEqual(t, r.retry, 5*time.Second+250*time.Millisecond)
		}
	}
	require.Equal(t, 1, okCount, "expected exactly one successful Acquire")
}
