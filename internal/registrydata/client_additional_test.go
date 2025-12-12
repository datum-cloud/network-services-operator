package registrydata

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openrdap/rdap"
	"github.com/openrdap/rdap/bootstrap"
	"github.com/openrdap/rdap/bootstrap/cache"
	"github.com/stretchr/testify/require"
)

type spyLimiter struct {
	acquireFn    func(ctx context.Context, provider string) (bool, time.Duration, error)
	blockUntilFn func(ctx context.Context, provider string, until time.Time) error

	blockMu sync.Mutex
	blocks  []struct {
		provider string
		until    time.Time
	}
}

func (s *spyLimiter) Acquire(ctx context.Context, provider string) (bool, time.Duration, error) {
	if s.acquireFn != nil {
		return s.acquireFn(ctx, provider)
	}
	return true, 0, nil
}

func (s *spyLimiter) BlockUntil(ctx context.Context, provider string, until time.Time) error {
	s.blockMu.Lock()
	s.blocks = append(s.blocks, struct {
		provider string
		until    time.Time
	}{provider: provider, until: until})
	s.blockMu.Unlock()
	if s.blockUntilFn != nil {
		return s.blockUntilFn(ctx, provider, until)
	}
	return nil
}

func (s *spyLimiter) lastBlock() (provider string, until time.Time, ok bool) {
	s.blockMu.Lock()
	defer s.blockMu.Unlock()
	if len(s.blocks) == 0 {
		return "", time.Time{}, false
	}
	b := s.blocks[len(s.blocks)-1]
	return b.provider, b.until, true
}

func newBootstrapJSON(entry string, rdapBaseURL string) string {
	// bootstrap.File expects Services [][][]string: [ [ [entries...], [urls...] ] ]
	doc := map[string]any{
		"description": "test",
		"publication": time.Now().UTC().Format(time.RFC3339),
		"version":     "1",
		"services": []any{
			[]any{
				[]string{entry},
				[]string{rdapBaseURL},
			},
		},
	}
	b, _ := json.Marshal(doc)
	return string(b)
}

func newTLSRegistryAndRDAPServer(t *testing.T, dnsEntry string, rdapHandler http.HandlerFunc) (*httptest.Server, *url.URL) {
	t.Helper()

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/dns.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(newBootstrapJSON(dnsEntry, srv.URL+"/")))
	})
	mux.HandleFunc("/ipv4.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(newBootstrapJSON("0.0.0.0/0", srv.URL+"/")))
	})
	mux.HandleFunc("/ipv6.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(newBootstrapJSON("::/0", srv.URL+"/")))
	})

	// RDAP endpoints (domain/ip) + fallthrough for others.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if rdapHandler != nil && (strings.HasPrefix(r.URL.Path, "/domain/") || strings.HasPrefix(r.URL.Path, "/ip/")) {
			rdapHandler(w, r)
			return
		}
		http.NotFound(w, r)
	})

	srv = httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL + "/")
	require.NoError(t, err)
	return srv, u
}

func newTestRegistryClient(t *testing.T, srv *httptest.Server, bootstrapBase *url.URL) *client {
	t.Helper()

	// Make bootstrap + rdap client use the same TLS transport trust.
	hc := srv.Client()
	if tr, ok := hc.Transport.(*http.Transport); ok {
		// Ensure TLS config is present and skips verification of the test server cert chain
		// is already handled by srv.Client(), but keep it explicit for safety if transports change.
		if tr.TLSClientConfig == nil {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
		}
	}

	bc := &bootstrap.Client{
		HTTP:    hc,
		BaseURL: bootstrapBase,
		Cache:   cache.NewMemoryCache(),
	}

	rc, err := NewClient(Config{
		Cache: CacheConfig{Backend: CacheBackendMemory},
		CacheTTLs: CacheTTLs{
			Domain:       5 * time.Minute,
			Nameserver:   5 * time.Minute,
			IPRegistrant: 5 * time.Minute,
		},
		RateLimits: RateLimits{
			DefaultRatePerSec: 1000,
			DefaultBurst:      10,
			DefaultBlock:      2 * time.Second,
		},
		RDAPClient: &rdap.Client{
			HTTP:      hc,
			Bootstrap: bc,
		},
		WhoisBootstrapHost: "whois.iana.test",
		WhoisFetch: func(ctx context.Context, query, host string) (string, error) {
			return "", fmt.Errorf("unexpected whois fetch: %s %s", host, query)
		},
	})
	require.NoError(t, err)

	c := rc.(*client)
	// Avoid any real DNS.
	c.lookupNS = func(ctx context.Context, name string) ([]*net.NS, error) {
		return nil, &net.DNSError{IsNotFound: true}
	}
	c.lookupIP = func(ctx context.Context, name string) ([]net.IPAddr, error) {
		return nil, nil
	}
	return c
}

func TestClient_RateLimitedWhenLimiterDeniesAfterBootstrap(t *testing.T) {
	t.Parallel()

	// Bootstrap matches "com" so providerKey will be server host.
	srv, base := newTLSRegistryAndRDAPServer(t, "com", func(w http.ResponseWriter, r *http.Request) {
		// Should never be reached in this test.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})

	c := newTestRegistryClient(t, srv, base)

	deny := &spyLimiter{
		acquireFn: func(ctx context.Context, provider string) (bool, time.Duration, error) {
			return false, 123 * time.Millisecond, nil
		},
	}
	c.limiter = deny

	_, err := c.LookupDomain(context.Background(), "example.com", LookupOptions{ForceRefresh: true})
	require.Error(t, err)
	rl, ok := err.(*RateLimitedError)
	require.True(t, ok)
	require.Equal(t, base.Host, rl.Provider)
	require.Equal(t, 123*time.Millisecond, rl.RetryAfter)
}

func TestClient_RDAp429_RetryAfterBlocksProvider(t *testing.T) {
	t.Parallel()

	srv, base := newTLSRegistryAndRDAPServer(t, "com", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"errorCode":429}`))
	})

	c := newTestRegistryClient(t, srv, base)
	spy := &spyLimiter{
		acquireFn: func(ctx context.Context, provider string) (bool, time.Duration, error) {
			return true, 0, nil
		},
	}
	c.limiter = spy

	_, err := c.lookupDomainRDAP(context.Background(), "example.com")
	require.Error(t, err)
	rl, ok := err.(*RateLimitedError)
	require.True(t, ok)
	require.Equal(t, base.Host, rl.Provider)
	require.Equal(t, 10*time.Second, rl.RetryAfter)

	prov, until, ok := spy.lastBlock()
	require.True(t, ok)
	require.Equal(t, base.Host, prov)
	require.GreaterOrEqual(t, time.Until(until), 9*time.Second)
	require.LessOrEqual(t, time.Until(until), 11*time.Second)
}

func TestClient_Singleflight_PreventsWHOISStampede(t *testing.T) {
	t.Parallel()

	// DNS bootstrap has NO matching entry (use WHOIS path).
	srv, base := newTLSRegistryAndRDAPServer(t, "net", nil)
	c := newTestRegistryClient(t, srv, base)

	// Allow all limiter acquires.
	c.limiter = &spyLimiter{
		acquireFn: func(ctx context.Context, provider string) (bool, time.Duration, error) {
			return true, 0, nil
		},
	}

	var calls int64
	c.whoisFetch = func(ctx context.Context, query, host string) (string, error) {
		atomic.AddInt64(&calls, 1)
		// Slow down to increase concurrency overlap; no sleeps in main test goroutine.
		time.Sleep(25 * time.Millisecond)

		if host == c.cfg.WhoisBootstrapHost {
			// IANA bootstrap response: refer to a registry host.
			return "refer: whois.registry.test\n", nil
		}
		// Registry response body must be non-empty.
		return "Registrar: Test Registrar\n", nil
	}

	const n = 20
	start := make(chan struct{})
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		go func() {
			<-start
			_, err := c.LookupDomain(context.Background(), "example.com", LookupOptions{})
			errs <- err
		}()
	}
	close(start)

	for i := 0; i < n; i++ {
		require.NoError(t, <-errs)
	}

	// One lookup should perform exactly two whois fetches: IANA + registry.
	require.Equal(t, int64(2), atomic.LoadInt64(&calls))
}

func TestClient_WHOISRateLimitPropagates(t *testing.T) {
	t.Parallel()

	// DNS bootstrap has NO matching entry (use WHOIS path).
	srv, base := newTLSRegistryAndRDAPServer(t, "net", nil)
	c := newTestRegistryClient(t, srv, base)

	c.whoisFetch = func(ctx context.Context, query, host string) (string, error) {
		if host == c.cfg.WhoisBootstrapHost {
			return "refer: whois.registry.test\n", nil
		}
		return "Registrar: Test Registrar\n", nil
	}

	// Deny the bootstrap host.
	c.limiter = &spyLimiter{
		acquireFn: func(ctx context.Context, provider string) (bool, time.Duration, error) {
			if provider == c.cfg.WhoisBootstrapHost {
				return false, 7 * time.Second, nil
			}
			return true, 0, nil
		},
	}

	_, err := c.LookupDomain(context.Background(), "example.com", LookupOptions{ForceRefresh: true})
	require.Error(t, err)
	rl, ok := err.(*RateLimitedError)
	require.True(t, ok)
	require.Equal(t, c.cfg.WhoisBootstrapHost, rl.Provider)
	require.Equal(t, 7*time.Second, rl.RetryAfter)
}

func TestClient_ErrorDoesNotPopulateCache(t *testing.T) {
	t.Parallel()

	// DNS bootstrap has NO matching entry (use WHOIS path).
	srv, base := newTLSRegistryAndRDAPServer(t, "net", nil)
	c := newTestRegistryClient(t, srv, base)

	c.limiter = &spyLimiter{
		acquireFn: func(ctx context.Context, provider string) (bool, time.Duration, error) {
			return true, 0, nil
		},
	}

	var mode atomic.Int32 // 0=fail, 1=success
	var calls int64
	c.whoisFetch = func(ctx context.Context, query, host string) (string, error) {
		atomic.AddInt64(&calls, 1)
		if host == c.cfg.WhoisBootstrapHost {
			return "refer: whois.registry.test\n", nil
		}
		if mode.Load() == 0 {
			// empty body -> causes "no WHOIS registry body" error
			return "", nil
		}
		return "Registrar: Test Registrar\n", nil
	}

	_, err := c.LookupDomain(context.Background(), "example.com", LookupOptions{ForceRefresh: true})
	require.Error(t, err)

	// Flip to success and ensure we perform WHOIS again (no cached error/empty result).
	mode.Store(1)
	_, err = c.LookupDomain(context.Background(), "example.com", LookupOptions{})
	require.NoError(t, err)

	require.GreaterOrEqual(t,
		atomic.LoadInt64(&calls),
		int64(4),
		"expected two whois fetches per attempt (IANA + registry)")
}

func TestRedisProviderLimiter_StateTTLEvictionResetsBucket(t *testing.T) {
	t.Parallel()

	mr, rc := newTestRedis(t)
	l := newRedisProviderLimiter(rc, "pfx:", RateLimits{
		DefaultRatePerSec: 0.0,
		DefaultBurst:      1,
		DefaultBlock:      10 * time.Second,
	})
	l.stateTTL = 500 * time.Millisecond

	ctx := context.Background()
	ok, _, err := l.Acquire(ctx, "provider")
	require.NoError(t, err)
	require.True(t, ok)

	ok, retry, err := l.Acquire(ctx, "provider")
	require.NoError(t, err)
	require.False(t, ok)
	require.Greater(t, retry, 0*time.Second)

	// After stateTTL expires, limiter state should be recreated with a fresh token.
	mr.FastForward(2 * time.Second)
	ok, _, err = l.Acquire(ctx, "provider")
	require.NoError(t, err)
	require.True(t, ok)
}

func TestMemoryProviderLimiter_StateTTLCleanupEvictsOldProviders(t *testing.T) {
	t.Parallel()

	l := newMemoryProviderLimiter(RateLimits{
		DefaultRatePerSec: 0.0,
		DefaultBurst:      1,
		DefaultBlock:      1 * time.Second,
	})
	l.stateTTL = 1 * time.Second

	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }

	// Touch providerA at t0
	ok, _, err := l.Acquire(context.Background(), "providerA")
	require.NoError(t, err)
	require.True(t, ok)

	// Advance beyond TTL and touch providerB, which triggers opportunistic cleanup.
	now = now.Add(2 * time.Second)
	ok, _, err = l.Acquire(context.Background(), "providerB")
	require.NoError(t, err)
	require.True(t, ok)

	l.mu.Lock()
	_, existsA := l.buckets["providerA"]
	_, existsB := l.buckets["providerB"]
	l.mu.Unlock()
	require.False(t, existsA, "expected providerA bucket to be evicted by TTL cleanup")
	require.True(t, existsB)
}

func TestNewClient_RedisPrefixAppliedToCacheAndLimiter(t *testing.T) {
	t.Parallel()

	mr, rc := newTestRedis(t)
	_ = mr

	cIntf, err := NewClient(Config{
		Cache: CacheConfig{
			Backend:        CacheBackendRedis,
			RedisKeyPrefix: "pfx:",
		},
		CacheTTLs: CacheTTLs{
			Domain:       1 * time.Minute,
			Nameserver:   1 * time.Minute,
			IPRegistrant: 1 * time.Minute,
		},
		RateLimits: RateLimits{
			DefaultRatePerSec: 1,
			DefaultBurst:      1,
			DefaultBlock:      1 * time.Second,
		},
		RedisClient:        rc,
		WhoisBootstrapHost: "whois.iana.test",
	})
	require.NoError(t, err)

	c := cIntf.(*client)
	rcache, ok := c.cache.(*redisCache)
	require.True(t, ok)
	require.Equal(t, "pfx:domain:example.com", rcache.key("domain:example.com"))

	rlim, ok := c.limiter.(*redisProviderLimiter)
	require.True(t, ok)
	require.Equal(t, "pfx:rl:provider", rlim.key("provider"))
}

func TestRedisCache_TTLZeroDoesNotExpire(t *testing.T) {
	t.Parallel()

	mr, rc := newTestRedis(t)
	_ = mr

	c := newRedisCache(rc, "pfx:")

	type payload struct {
		A string `json:"a"`
	}
	require.NoError(t, c.Set("k", payload{A: "x"}, 0))

	mr.FastForward(24 * time.Hour)

	var got payload
	found, err := c.Get("k", &got)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "x", got.A)
}

func TestRedisLimiterAndCache_NoKeyCollisions(t *testing.T) {
	t.Parallel()

	mr, rc := newTestRedis(t)
	_ = mr

	// Create both with the same prefix and ensure keys are distinct.
	cache := newRedisCache(rc, "pfx:")
	lim := newRedisProviderLimiter(rc, "pfx:", RateLimits{DefaultRatePerSec: 0, DefaultBurst: 1, DefaultBlock: 1 * time.Second})

	require.NoError(t, cache.Set("domain:example.com", map[string]any{"ok": true}, 1*time.Minute))
	ok, _, err := lim.Acquire(context.Background(), "domain:example.com")
	require.NoError(t, err)
	require.True(t, ok)

	// Should have both "pfx:domain:example.com" (string key) and "pfx:rl:domain:example.com" (hash key).
	keys := rc.Keys("pfx:*").Val()
	require.GreaterOrEqual(t, len(keys), 2)
	require.Contains(t, keys, "pfx:domain:example.com")
	require.Contains(t, keys, "pfx:rl:domain:example.com")
}
