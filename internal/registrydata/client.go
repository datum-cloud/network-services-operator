package registrydata

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openrdap/rdap"
	"github.com/openrdap/rdap/bootstrap"
	"golang.org/x/net/publicsuffix"
	"golang.org/x/sync/singleflight"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const rdapSource = "rdap"

type client struct {
	cfg Config

	cache   Cache
	limiter ProviderLimiter

	rdapClient      *rdap.Client
	bootstrapClient *bootstrap.Client
	whoisFetch      func(ctx context.Context, query, host string) (string, error)

	lookupNS func(ctx context.Context, name string) ([]*net.NS, error)
	lookupIP func(ctx context.Context, name string) ([]net.IPAddr, error)

	domainSF singleflight.Group
	nsSF     singleflight.Group
	ipSF     singleflight.Group
}

func NewClient(cfg Config) (Client, error) {
	if cfg.Cache.Backend == "" {
		cfg.Cache.Backend = CacheBackendMemory
	}
	if cfg.CacheTTLs.Domain <= 0 {
		cfg.CacheTTLs.Domain = 15 * time.Minute
	}
	if cfg.CacheTTLs.Nameserver <= 0 {
		cfg.CacheTTLs.Nameserver = 5 * time.Minute
	}
	if cfg.CacheTTLs.IPRegistrant <= 0 {
		cfg.CacheTTLs.IPRegistrant = 6 * time.Hour
	}
	if cfg.RateLimits.DefaultRatePerSec <= 0 {
		cfg.RateLimits.DefaultRatePerSec = 1.0
	}
	if cfg.RateLimits.DefaultBurst <= 0 {
		cfg.RateLimits.DefaultBurst = 5
	}
	if cfg.RateLimits.DefaultBlock <= 0 {
		cfg.RateLimits.DefaultBlock = 2 * time.Second
	}
	if cfg.WhoisBootstrapHost == "" {
		cfg.WhoisBootstrapHost = "whois.iana.org"
	}

	rdapClient := cfg.RDAPClient
	if rdapClient == nil {
		hc := cfg.HTTPClient
		if hc == nil {
			hc = &http.Client{}
		}
		rdapClient = &rdap.Client{HTTP: hc, Bootstrap: &bootstrap.Client{}}
	}

	whoisFetch := cfg.WhoisFetch
	if whoisFetch == nil {
		whoisFetch = whoisFetchAtHost
	}

	var cache Cache
	switch cfg.Cache.Backend {
	case CacheBackendMemory:
		cache = newMemoryCache()
	case CacheBackendRedis:
		if cfg.RedisClient == nil {
			return nil, fmt.Errorf("redis cache backend requires RedisClient")
		}
		cache = newRedisCache(cfg.RedisClient, cfg.Cache.RedisKeyPrefix)
	default:
		return nil, fmt.Errorf("unknown cache backend: %q", cfg.Cache.Backend)
	}

	var limiter ProviderLimiter
	if cfg.RedisClient != nil {
		limiter = newRedisProviderLimiter(cfg.RedisClient, cfg.Cache.RedisKeyPrefix, cfg.RateLimits)
	} else {
		limiter = newMemoryProviderLimiter(cfg.RateLimits)
	}

	bc := rdapClient.Bootstrap
	if bc == nil {
		bc = &bootstrap.Client{}
	}

	c := &client{
		cfg:             cfg,
		cache:           cache,
		limiter:         limiter,
		rdapClient:      rdapClient,
		bootstrapClient: bc,
		whoisFetch:      whoisFetch,
		lookupNS:        net.DefaultResolver.LookupNS,
		lookupIP:        net.DefaultResolver.LookupIPAddr,
	}

	return c, nil
}

func (c *client) LookupDomain(ctx context.Context, domain string, opts LookupOptions) (*DomainResult, error) {
	domainNorm := normalizeDomain(domain)
	apex, err := publicsuffix.EffectiveTLDPlusOne(domainNorm)
	if err != nil {
		return nil, err
	}
	cacheKey := "domain:" + apex

	if !opts.ForceRefresh {
		var cached DomainResult
		if found, err := c.cache.Get(cacheKey, &cached); err != nil {
			return nil, err
		} else if found {
			return &cached, nil
		}
	}

	v, err, _ := c.domainSF.Do(cacheKey, func() (any, error) {
		if !opts.ForceRefresh {
			var cached DomainResult
			if found, err := c.cache.Get(cacheKey, &cached); err != nil {
				return nil, err
			} else if found {
				return &cached, nil
			}
		}
		res, lookupErr := c.lookupDomainFresh(ctx, domainNorm, apex)
		if lookupErr == nil && res != nil {
			_ = c.cache.Set(cacheKey, res, c.cfg.CacheTTLs.Domain)
		}
		return res, lookupErr
	})
	if err != nil {
		if res, ok := v.(*DomainResult); ok && res != nil {
			return res, err
		}
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	return v.(*DomainResult), nil
}

func (c *client) LookupNameserver(ctx context.Context, hostname string, opts LookupOptions) (*NameserverResult, error) {
	host := normalizeHostname(hostname)
	cacheKey := "ns:" + host

	if !opts.ForceRefresh {
		var cached nameserverCacheValue
		if found, err := c.cache.Get(cacheKey, &cached); err != nil {
			return nil, err
		} else if found {
			return cached.toResult(c.cfg.CacheTTLs.Nameserver), nil
		}
	}

	v, err, _ := c.nsSF.Do(cacheKey, func() (any, error) {
		if !opts.ForceRefresh {
			var cached nameserverCacheValue
			if found, err := c.cache.Get(cacheKey, &cached); err != nil {
				return nil, err
			} else if found {
				return cached.toResult(c.cfg.CacheTTLs.Nameserver), nil
			}
		}
		ips, err := c.lookupIP(ctx, host)
		if err != nil {
			return nil, err
		}
		val := nameserverCacheValue{Hostname: host}
		for _, ip := range ips {
			if ip.IP != nil {
				val.IPs = append(val.IPs, ip.IP.String())
			}
		}
		_ = c.cache.Set(cacheKey, &val, c.cfg.CacheTTLs.Nameserver)
		return val.toResult(c.cfg.CacheTTLs.Nameserver), nil
	})
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	return v.(*NameserverResult), nil
}

func (c *client) LookupIPRegistrant(ctx context.Context, ip net.IP, opts LookupOptions) (*IPRegistrantResult, error) {
	ipNorm := normalizeIP(ip)
	cacheKey := "ipreg:" + ipNorm

	if !opts.ForceRefresh {
		var cached IPRegistrantResult
		if found, err := c.cache.Get(cacheKey, &cached); err != nil {
			return nil, err
		} else if found {
			return &cached, nil
		}
	}

	v, err, _ := c.ipSF.Do(cacheKey, func() (any, error) {
		if !opts.ForceRefresh {
			var cached IPRegistrantResult
			if found, err := c.cache.Get(cacheKey, &cached); err != nil {
				return nil, err
			} else if found {
				return &cached, nil
			}
		}

		res, lookupErr := c.lookupIPRegistrantFresh(ctx, ipNorm)
		if lookupErr == nil && res != nil {
			_ = c.cache.Set(cacheKey, res, c.cfg.CacheTTLs.IPRegistrant)
		}
		return res, lookupErr
	})
	if err != nil {
		if res, ok := v.(*IPRegistrantResult); ok && res != nil {
			return res, err
		}
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	return v.(*IPRegistrantResult), nil
}

// --- domain lookup implementation ---

type nameserverCacheValue struct {
	Hostname string   `json:"hostname"`
	IPs      []string `json:"ips"`
}

func (v *nameserverCacheValue) toResult(ttl time.Duration) *NameserverResult {
	out := &NameserverResult{Hostname: v.Hostname, TTL: ttl}
	for _, s := range v.IPs {
		if p := net.ParseIP(s); p != nil {
			out.IPs = append(out.IPs, p)
		}
	}
	return out
}

func (c *client) lookupDomainFresh(ctx context.Context, domainNorm, apex string) (*DomainResult, error) {
	isApex := strings.EqualFold(domainNorm, apex)

	// Attempt RDAP first.
	rdapRes, rdapErr := c.lookupDomainRDAP(ctx, apex)
	useWHOIS := false
	if rdapErr != nil {
		var ce *rdap.ClientError
		if errors.As(rdapErr, &ce) && ce.Type == rdap.BootstrapNoMatch {
			useWHOIS = true
		} else {
			// RDAP lookup failed (non-bootstrap); retry later.
			return nil, rdapErr
		}
	}
	if rdapRes == nil || rdapRes.domain == nil {
		useWHOIS = true
	}

	var reg *networkingv1alpha.Registration
	providerKey := ""
	source := ""
	suggestedDelay := time.Duration(0)
	var apexNS []string

	if !useWHOIS {
		r := mapRDAPDomainToRegistration(*rdapRes.domain)
		r.Source = rdapSource
		reg = &r
		providerKey = rdapRes.providerKey
		source = rdapSource
		// Registry from bootstrap URL host.
		if providerKey != "" {
			reg.Registry = &networkingv1alpha.RegistryInfo{Name: providerKey, URL: "https://" + providerKey}
		}
		for _, ns := range rdapRes.domain.Nameservers {
			if ns.LDHName != "" {
				apexNS = append(apexNS, normalizeHostname(ns.LDHName))
			}
		}
	} else {
		wreg, whoisProvider, whoisErr := c.fetchRegistrationWhois(ctx, apex)
		if whoisErr != nil {
			return nil, whoisErr
		}
		wreg.Source = "whois"
		reg = wreg
		providerKey = whoisProvider
		source = "whois"
	}

	// Nameserver selection (apex vs delegated subdomain).
	var nsHosts []string
	if isApex {
		if source == rdapSource && len(apexNS) > 0 {
			nsHosts = apexNS
		} else {
			_, nsHosts = c.delegatedZoneNS(ctx, apex, apex)
		}
	} else {
		if delegated, delegatedNS := c.delegatedZoneNS(ctx, domainNorm, apex); delegated && len(delegatedNS) > 0 {
			nsHosts = delegatedNS
		} else {
			if source == rdapSource && len(apexNS) > 0 {
				nsHosts = apexNS
			} else {
				_, nsHosts = c.delegatedZoneNS(ctx, apex, apex)
			}
		}
	}

	nameservers := make([]networkingv1alpha.Nameserver, 0, len(nsHosts))
	for _, h := range nsHosts {
		ns := networkingv1alpha.Nameserver{Hostname: normalizeHostname(h)}
		nsRes, err := c.LookupNameserver(ctx, ns.Hostname, LookupOptions{})
		if err != nil {
			// best-effort; controller will retry on next cycle anyway.
			nameservers = append(nameservers, ns)
			continue
		}
		for _, ip := range nsRes.IPs {
			ipStr := normalizeIP(ip)
			entry := networkingv1alpha.NameserverIP{Address: ipStr}
			ipRes, ipErr := c.LookupIPRegistrant(ctx, ip, LookupOptions{})
			if ipErr != nil {
				// Preserve partial data but bubble the error so controllers can schedule a retry.
				if rl, ok := ipErr.(*RateLimitedError); ok {
					suggestedDelay = maxD(suggestedDelay, rl.RetryAfter)
				}
				res := &DomainResult{Registration: reg, Nameservers: append(nameservers, ns), Source: source, ProviderKey: providerKey, SuggestedDelay: suggestedDelay}
				return res, ipErr
			}
			if ipRes != nil && ipRes.Registrant != "" {
				entry.RegistrantName = ipRes.Registrant
			}
			ns.IPs = append(ns.IPs, entry)
		}
		nameservers = append(nameservers, ns)
	}

	res := &DomainResult{Registration: reg, Nameservers: nameservers, Source: source, ProviderKey: providerKey, SuggestedDelay: suggestedDelay}
	return res, nil
}

type rdapDomainResponse struct {
	domain      *rdap.Domain
	providerKey string
	baseURL     *url.URL
}

func (c *client) lookupDomainRDAP(ctx context.Context, apex string) (*rdapDomainResponse, error) {
	// Bootstrap to determine provider.
	answer, err := c.bootstrapClient.Lookup((&bootstrap.Question{RegistryType: bootstrap.DNS, Query: apex}).WithContext(ctx))
	if err != nil {
		// Let controller retry; don't silently fall back.
		return nil, err
	}
	if answer == nil || len(answer.URLs) == 0 {
		return nil, &rdap.ClientError{Type: rdap.BootstrapNoMatch, Text: fmt.Sprintf("No RDAP servers found for %q", apex)}
	}

	base := pickBootstrapURL(answer.URLs)
	providerKey := ""
	if base != nil {
		providerKey = base.Host
	}
	if ok, retryAfter, err := c.limiter.Acquire(ctx, providerKey); err != nil {
		return nil, err
	} else if !ok {
		return nil, &RateLimitedError{Provider: providerKey, RetryAfter: retryAfter}
	}

	req := (&rdap.Request{Type: rdap.DomainRequest, Query: apex}).WithContext(ctx)
	if base != nil {
		req = req.WithServer(base)
	}
	resp, err := c.rdapClient.Do(req)
	if err != nil {
		delay, limited := rdapSuggestedDelay(resp)
		if delay > 0 || limited {
			if delay <= 0 {
				delay = c.cfg.RateLimits.DefaultBlock
			}
			_ = c.limiter.BlockUntil(ctx, providerKey, time.Now().Add(delay))
			return nil, &RateLimitedError{Provider: providerKey, RetryAfter: delay}
		}
		return nil, err
	}
	if resp == nil || resp.Object == nil {
		return &rdapDomainResponse{providerKey: providerKey, baseURL: base}, nil
	}
	dom, _ := resp.Object.(*rdap.Domain)
	return &rdapDomainResponse{domain: dom, providerKey: providerKey, baseURL: base}, nil
}

func (c *client) lookupIPRegistrantFresh(ctx context.Context, ip string) (*IPRegistrantResult, error) {
	providerKey, base, err := c.bootstrapIP(ctx, ip)
	if err != nil {
		return nil, err
	}
	if ok, retryAfter, err := c.limiter.Acquire(ctx, providerKey); err != nil {
		return nil, err
	} else if !ok {
		return nil, &RateLimitedError{Provider: providerKey, RetryAfter: retryAfter}
	}

	req := (&rdap.Request{Type: rdap.IPRequest, Query: ip}).WithContext(ctx)
	if base != nil {
		req = req.WithServer(base)
	}
	resp, err := c.rdapClient.Do(req)
	if err != nil {
		delay, limited := rdapSuggestedDelay(resp)
		if delay > 0 || limited {
			if delay <= 0 {
				delay = c.cfg.RateLimits.DefaultBlock
			}
			_ = c.limiter.BlockUntil(ctx, providerKey, time.Now().Add(delay))
			return nil, &RateLimitedError{Provider: providerKey, RetryAfter: delay}
		}
		return nil, err
	}
	if resp == nil || resp.Object == nil {
		return &IPRegistrantResult{IP: net.ParseIP(ip), ProviderKey: providerKey}, nil
	}
	netObj, _ := resp.Object.(*rdap.IPNetwork)
	if netObj == nil {
		return &IPRegistrantResult{IP: net.ParseIP(ip), ProviderKey: providerKey}, nil
	}

	name := ""
	for _, e := range netObj.Entities {
		if hasRole(e.Roles, "registrant") {
			n, _, _ := extractVCard(e.VCard)
			if n != "" {
				name = n
				break
			}
		}
	}
	return &IPRegistrantResult{IP: net.ParseIP(ip), Registrant: name, ProviderKey: providerKey}, nil
}

func (c *client) delegatedZoneNS(ctx context.Context, fqdn, apex string) (delegated bool, hosts []string) {
	trimDot := func(s string) string { return strings.TrimSuffix(s, ".") }
	addDot := func(s string) string {
		if s == "" || strings.HasSuffix(s, ".") {
			return s
		}
		return s + "."
	}

	cur := strings.ToLower(trimDot(fqdn))
	apex = strings.ToLower(trimDot(apex))

	for {
		recs, err := c.lookupNS(ctx, addDot(cur))
		if err == nil && len(recs) > 0 {
			out := make([]string, 0, len(recs))
			for _, rr := range recs {
				if rr != nil && rr.Host != "" {
					out = append(out, trimDot(rr.Host))
				}
			}
			return cur != apex, out
		}
		if cur == apex {
			break
		}
		if i := strings.IndexByte(cur, '.'); i > 0 {
			cur = cur[i+1:]
		} else {
			break
		}
	}
	return false, nil
}

func (c *client) fetchRegistrationWhois(ctx context.Context, apex string) (*networkingv1alpha.Registration, string, error) {
	// Rate limit IANA WHOIS bootstrap.
	if ok, retryAfter, err := c.limiter.Acquire(ctx, c.cfg.WhoisBootstrapHost); err != nil {
		return nil, "", err
	} else if !ok {
		return nil, "", &RateLimitedError{Provider: c.cfg.WhoisBootstrapHost, RetryAfter: retryAfter}
	}

	tld, _ := publicsuffix.PublicSuffix(apex)
	bodyIANA, _ := c.whoisFetch(ctx, tld, c.cfg.WhoisBootstrapHost)
	referHost := strings.TrimSpace(findWhoisValue(bodyIANA, []string{"refer", "whois"}))

	tryHosts := []string{}
	if referHost != "" {
		tryHosts = append(tryHosts, referHost)
	}
	tryHosts = append(tryHosts, "whois.registry."+strings.ToLower(tld))
	tryHosts = append(tryHosts, "whois.nic."+strings.ToLower(tld))

	var registryBody string
	var lastErr error
	selectedHost := ""
	for _, h := range tryHosts {
		if ok, retryAfter, err := c.limiter.Acquire(ctx, h); err != nil {
			lastErr = err
			continue
		} else if !ok {
			return nil, "", &RateLimitedError{Provider: h, RetryAfter: retryAfter}
		}
		if b, e := c.whoisFetch(ctx, apex, h); e == nil && strings.TrimSpace(b) != "" {
			registryBody = b
			selectedHost = h
			break
		} else {
			lastErr = e
		}
	}
	if registryBody == "" {
		if lastErr == nil {
			lastErr = fmt.Errorf("no WHOIS registry body for %s", apex)
		}
		return nil, "", lastErr
	}

	bodyToParse := registryBody
	provider := selectedHost
	if registrarHost := strings.TrimSpace(findWhoisValue(registryBody, []string{"Registrar WHOIS Server"})); registrarHost != "" {
		if ok, retryAfter, err := c.limiter.Acquire(ctx, registrarHost); err != nil {
			// ignore; parse registry body
		} else if !ok {
			return nil, "", &RateLimitedError{Provider: registrarHost, RetryAfter: retryAfter}
		} else if b, e := c.whoisFetch(ctx, apex, registrarHost); e == nil && strings.TrimSpace(b) != "" {
			bodyToParse = b
			provider = registrarHost
		}
	}

	reg := parseWhoisRegistration(apex, bodyToParse)
	return reg, provider, nil
}

func bootstrapRegistryTypeForIP(ip string) bootstrap.RegistryType {
	p := net.ParseIP(ip)
	if p == nil {
		return bootstrap.IPv4
	}
	if p.To4() != nil {
		return bootstrap.IPv4
	}
	return bootstrap.IPv6
}

func (c *client) bootstrapIP(ctx context.Context, ip string) (providerKey string, base *url.URL, err error) {
	q := (&bootstrap.Question{RegistryType: bootstrapRegistryTypeForIP(ip), Query: ip}).WithContext(ctx)
	answer, err := c.bootstrapClient.Lookup(q)
	if err != nil {
		return "", nil, err
	}
	if answer == nil || len(answer.URLs) == 0 {
		return "", nil, &rdap.ClientError{Type: rdap.BootstrapNoMatch, Text: fmt.Sprintf("No RDAP servers found for %q", ip)}
	}
	base = pickBootstrapURL(answer.URLs)
	if base != nil {
		providerKey = base.Host
	}
	return providerKey, base, nil
}

func pickBootstrapURL(urls []*url.URL) *url.URL {
	for _, u := range urls {
		if u != nil && strings.EqualFold(u.Scheme, "https") {
			return u
		}
	}
	if len(urls) > 0 {
		return urls[0]
	}
	return nil
}

func normalizeDomain(d string) string {
	d = strings.TrimSpace(d)
	d = strings.TrimSuffix(d, ".")
	return strings.ToLower(d)
}

func normalizeHostname(h string) string {
	h = strings.TrimSpace(h)
	h = strings.TrimSuffix(h, ".")
	return strings.ToLower(h)
}

func normalizeIP(ip net.IP) string {
	if ip == nil {
		return ""
	}
	return ip.String()
}

func maxD(a, b time.Duration) time.Duration {
	if a < b {
		return b
	}
	return a
}
