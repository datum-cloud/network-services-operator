package registrydata

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

var (
	parentNS = []string{"ns1.parent-dns.test", "ns2.parent-dns.test"}
	childNS  = []string{"ns1.datumdomains.net", "ns2.datumdomains.net"}
)

func rdapDomainHandler(nameservers []string, queries *atomic.Int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/domain/") {
			http.NotFound(w, r)
			return
		}
		queries.Add(1)
		ns := make([]map[string]string, 0, len(nameservers))
		for _, h := range nameservers {
			ns = append(ns, map[string]string{"objectClassName": "nameserver", "ldhName": h})
		}
		w.Header().Set("Content-Type", "application/rdap+json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"objectClassName": "domain",
			"ldhName":         strings.TrimPrefix(r.URL.Path, "/domain/"),
			"nameservers":     ns,
		})
	}
}

func stubDNS(c *client, answers map[string][]string, failures map[string]error) {
	c.lookupNS = func(ctx context.Context, name string) ([]*net.NS, error) {
		if err, ok := failures[name]; ok {
			return nil, err
		}
		if hosts, ok := answers[name]; ok {
			recs := make([]*net.NS, 0, len(hosts))
			for _, h := range hosts {
				recs = append(recs, &net.NS{Host: h + "."})
			}
			return recs, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
}

func newRDAPTestClient(t *testing.T, rdapQueries *atomic.Int64) *client {
	t.Helper()
	srv, base := newTLSRegistryAndRDAPServer(t, "com", rdapDomainHandler(parentNS, rdapQueries))
	c := newTestRegistryClient(t, srv, base)
	stubDNS(c, map[string][]string{"shop.example.com.": childNS}, nil)
	return c
}

func nameserverEntry(host string) networkingv1alpha.Nameserver {
	return networkingv1alpha.Nameserver{Hostname: host}
}

func nameserverHosts(t *testing.T, res *DomainResult) []string {
	t.Helper()
	require.NotNil(t, res)
	out := make([]string, 0, len(res.Nameservers))
	for _, ns := range res.Nameservers {
		out = append(out, ns.Hostname)
	}
	return out
}

func lookupHosts(t *testing.T, c *client, name string) []string {
	t.Helper()
	res, err := c.LookupDomain(context.Background(), name, LookupOptions{})
	require.NoError(t, err)
	return nameserverHosts(t, res)
}

func TestLookupDomain_EachNameGetsItsOwnNameservers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		order []string
		want  map[string][]string
	}{
		{
			name:  "parent first, then its delegated child",
			order: []string{"example.com", "shop.example.com"},
			want:  map[string][]string{"example.com": parentNS, "shop.example.com": childNS},
		},
		{
			name:  "delegated child first, then its parent",
			order: []string{"shop.example.com", "example.com"},
			want:  map[string][]string{"shop.example.com": childNS, "example.com": parentNS},
		},
		{
			name:  "delegated sibling first, then a name with no delegation of its own",
			order: []string{"shop.example.com", "www.example.com"},
			want:  map[string][]string{"shop.example.com": childNS, "www.example.com": parentNS},
		},
		{
			name:  "a name below a delegated child takes the child's nameservers",
			order: []string{"example.com", "cart.shop.example.com"},
			want:  map[string][]string{"example.com": parentNS, "cart.shop.example.com": childNS},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var rdapQueries atomic.Int64
			c := newRDAPTestClient(t, &rdapQueries)
			for _, name := range tt.order {
				require.ElementsMatch(t, tt.want[name], lookupHosts(t, c, name), "nameservers of %s", name)
			}
		})
	}
}

func TestLookupDomain_RegistrationIsSharedAcrossNamesOfOneDomain(t *testing.T) {
	t.Parallel()

	var rdapQueries atomic.Int64
	c := newRDAPTestClient(t, &rdapQueries)
	for _, name := range []string{"shop.example.com", "example.com", "www.example.com"} {
		res, err := c.LookupDomain(context.Background(), name, LookupOptions{})
		require.NoError(t, err)
		require.NotNil(t, res.Registration)
		require.Equal(t, "example.com", res.Registration.Domain)
	}
	require.Equal(t, int64(1), rdapQueries.Load(), "one RDAP query serves every name of the registered domain")
}

func TestLookupDomain_IgnoresAWholeResultCachedUnderTheRegisteredDomain(t *testing.T) {
	t.Parallel()

	var rdapQueries atomic.Int64
	c := newRDAPTestClient(t, &rdapQueries)
	stale := DomainResult{Source: rdapSource}
	for _, h := range childNS {
		stale.Nameservers = append(stale.Nameservers, nameserverEntry(h))
	}
	require.NoError(t, c.cache.Set("domain:example.com", &stale, c.cfg.CacheTTLs.Domain))

	require.ElementsMatch(t, parentNS, lookupHosts(t, c, "example.com"))
}

var (
	servfail = &net.DNSError{Err: "server misbehaving", IsTemporary: true}
	timedOut = &net.DNSError{Err: "i/o timeout", IsTimeout: true, IsTemporary: true}
)

func TestLookupDomain_AFailedNSQueryIsAnErrorNotTheParentsNameservers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lookup   string
		failures map[string]error
	}{
		{name: "server failure at the name", lookup: "shop.example.com", failures: map[string]error{"shop.example.com.": servfail}},
		{name: "timeout at the name", lookup: "shop.example.com", failures: map[string]error{"shop.example.com.": timedOut}},
		{name: "server failure at a parent label", lookup: "cart.shop.example.com", failures: map[string]error{"shop.example.com.": servfail}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var rdapQueries atomic.Int64
			c := newRDAPTestClient(t, &rdapQueries)
			stubDNS(c, nil, tt.failures)

			res, err := c.LookupDomain(context.Background(), tt.lookup, LookupOptions{})
			var nsErr *NameserverLookupError
			require.ErrorAs(t, err, &nsErr)
			require.NotNil(t, res)
			require.NotNil(t, res.Registration)
			require.Equal(t, "example.com", res.Registration.Domain)
			require.Empty(t, res.Nameservers)
		})
	}
}

func TestLookupDomain_NameserversRecoverOnceDNSAnswers(t *testing.T) {
	t.Parallel()

	var rdapQueries atomic.Int64
	c := newRDAPTestClient(t, &rdapQueries)
	stubDNS(c, nil, map[string]error{"shop.example.com.": servfail})
	_, err := c.LookupDomain(context.Background(), "shop.example.com", LookupOptions{})
	require.Error(t, err)

	stubDNS(c, map[string][]string{"shop.example.com.": childNS}, nil)
	require.ElementsMatch(t, childNS, lookupHosts(t, c, "shop.example.com"))
}

func TestLookupDomain_WHOISDomainTakesItsNameserversFromDNS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		answers  map[string][]string
		failures map[string]error
		wantErr  bool
	}{
		{name: "DNS answers", answers: map[string][]string{"example.com.": parentNS}},
		{name: "DNS fails", failures: map[string]error{"example.com.": servfail}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv, base := newTLSRegistryAndRDAPServer(t, "net", nil)
			c := newTestRegistryClient(t, srv, base)
			c.whoisFetch = func(ctx context.Context, query, host string) (string, error) {
				if host == c.cfg.WhoisBootstrapHost {
					return IAMABootstrapResponse, nil
				}
				return testRegistrarResponse, nil
			}
			stubDNS(c, tt.answers, tt.failures)

			res, err := c.LookupDomain(context.Background(), "example.com", LookupOptions{})
			if tt.wantErr {
				var nsErr *NameserverLookupError
				require.ErrorAs(t, err, &nsErr)
				require.NotNil(t, res.Registration)
				require.Empty(t, res.Nameservers)
				return
			}
			require.NoError(t, err)
			require.ElementsMatch(t, parentNS, nameserverHosts(t, res))
		})
	}
}
