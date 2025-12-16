package registrydata

import (
	"context"
	"net"
	"net/http"
	"time"

	redis "github.com/go-redis/redis/v7"
	"github.com/openrdap/rdap"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// Client is the public interface consumed by controllers.
type Client interface {
	// Domain → registration & nameservers
	LookupDomain(ctx context.Context, domain string, opts LookupOptions) (*DomainResult, error)

	// NS hostname → IP addresses
	LookupNameserver(ctx context.Context, hostname string, opts LookupOptions) (*NameserverResult, error)

	// IP → registrant (e.g. RDAP IP network entity)
	LookupIPRegistrant(ctx context.Context, ip net.IP, opts LookupOptions) (*IPRegistrantResult, error)
}

type LookupOptions struct {
	// ForceRefresh bypasses cache and always fetches fresh data from upstream.
	ForceRefresh bool
}

type DomainResult struct {
	Registration   *networkingv1alpha.Registration
	Nameservers    []networkingv1alpha.Nameserver
	Source         string
	ProviderKey    string
	SuggestedDelay time.Duration
}

type NameserverResult struct {
	Hostname string
	IPs      []net.IP
	TTL      time.Duration
}

type IPRegistrantResult struct {
	IP             net.IP
	Registrant     string
	ProviderKey    string
	SuggestedDelay time.Duration
}

// Returned when the provider rate limit is hit or a limiter denies a token.
type RateLimitedError struct {
	Provider   string
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	if e == nil {
		return "rate limited"
	}
	if e.Provider == "" {
		return "rate limited"
	}
	if e.RetryAfter > 0 {
		return "rate limited by " + e.Provider + "; retry after " + e.RetryAfter.String()
	}
	return "rate limited by " + e.Provider
}

// CacheBackend enumerates supported cache types.
type CacheBackend string

const (
	CacheBackendMemory CacheBackend = "memory"
	CacheBackendRedis  CacheBackend = "redis"
)

type CacheConfig struct {
	Backend CacheBackend
	// RedisKeyPrefix is used for Redis keys when Backend == redis.
	RedisKeyPrefix string
}

type CacheTTLs struct {
	Domain       time.Duration
	Nameserver   time.Duration
	IPRegistrant time.Duration
}

type RateLimits struct {
	DefaultRatePerSec float64
	DefaultBurst      float64
	DefaultBlock      time.Duration
}

type Config struct {
	Cache      CacheConfig
	CacheTTLs  CacheTTLs
	RateLimits RateLimits

	// Optional: required if CacheBackendRedis or to enable Redis-backed limiter.
	RedisClient redis.UniversalClient

	// Upstream dependencies
	RDAPClient *rdap.Client
	HTTPClient *http.Client
	WhoisFetch func(ctx context.Context, query, host string) (string, error)

	// WhoisBootstrapHost defaults to "whois.iana.org" when empty.
	WhoisBootstrapHost string
}
