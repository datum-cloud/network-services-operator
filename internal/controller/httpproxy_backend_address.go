// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/util/addressrange"
)

const (
	backendAddressObservationTTL = time.Minute

	backendAddressRecheckInterval = 5 * time.Minute

	backendAddressLookupTimeout = 2 * time.Second

	backendAddressLookupBudget = 3 * time.Second

	backendAddressLookupWorkers = 8

	backendAddressCacheLimit = 10000
)

type backendAddressObserver struct {
	lookupIP func(ctx context.Context, host string) ([]net.IPAddr, error)
	now      func() time.Time

	mu    sync.Mutex
	cache map[string]backendAddressObservation
}

type nonPublicAnswer struct {
	addr      netip.Addr
	addrRange addressrange.Range
}

type backendAddressObservation struct {
	answer    nonPublicAnswer
	nonPublic bool
	expires   time.Time
}

func newBackendAddressObserver() *backendAddressObserver {
	return &backendAddressObserver{
		lookupIP: net.DefaultResolver.LookupIPAddr,
		now:      time.Now,
		cache:    map[string]backendAddressObservation{},
	}
}

func backendAddressKey(host string) string {
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

func (o *backendAddressObserver) nonPublicAnswer(ctx context.Context, host string) (nonPublicAnswer, bool) {
	key := backendAddressKey(host)
	now := o.now()

	o.mu.Lock()
	if seen, ok := o.cache[key]; ok && now.Before(seen.expires) {
		o.mu.Unlock()
		return seen.answer, seen.nonPublic
	}
	o.mu.Unlock()

	if ctx.Err() != nil {
		return nonPublicAnswer{}, false
	}

	answer, nonPublic, err := o.lookup(ctx, key)
	if err != nil && ctx.Err() != nil {
		return nonPublicAnswer{}, false
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.cache) >= backendAddressCacheLimit {
		for k, v := range o.cache {
			if !now.Before(v.expires) {
				delete(o.cache, k)
			}
		}
	}
	o.cache[key] = backendAddressObservation{answer: answer, nonPublic: nonPublic, expires: now.Add(backendAddressObservationTTL)}
	return answer, nonPublic
}

func (o *backendAddressObserver) lookup(ctx context.Context, host string) (nonPublicAnswer, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, backendAddressLookupTimeout)
	defer cancel()

	addrs, err := o.lookupIP(ctx, host+".")
	if err != nil {
		return nonPublicAnswer{}, false, err
	}
	for _, a := range addrs {
		addr, ok := netip.AddrFromSlice(a.IP)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		if r, refused := addressrange.RefusedAtEdge(addr); refused {
			return nonPublicAnswer{addr: addr, addrRange: r}, true, nil
		}
	}
	return nonPublicAnswer{}, false, nil
}

func (o *backendAddressObserver) observeAll(ctx context.Context, hosts []string) map[string]nonPublicAnswer {
	ctx, cancel := context.WithTimeout(ctx, backendAddressLookupBudget)
	defer cancel()

	queue := make(chan string, len(hosts))
	for _, h := range hosts {
		queue <- h
	}
	close(queue)

	var mu sync.Mutex
	found := map[string]nonPublicAnswer{}
	var wg sync.WaitGroup
	for range min(backendAddressLookupWorkers, len(hosts)) {
		wg.Go(func() {
			for host := range queue {
				if ctx.Err() != nil {
					return
				}
				if answer, nonPublic := o.nonPublicAnswer(ctx, host); nonPublic {
					mu.Lock()
					found[host] = answer
					mu.Unlock()
				}
			}
		})
	}
	wg.Wait()
	return found
}

func resolvedBackendHost(backend networkingv1alpha.HTTPProxyRuleBackend) (string, bool) {
	if backend.Endpoint == "" || backend.Connector != nil || backend.Instance != nil || backend.NetworkService != nil {
		return "", false
	}
	u, err := url.Parse(backend.Endpoint)
	if err != nil {
		return "", false
	}
	host := u.Hostname()
	if host == "" {
		return "", false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return "", false
	}
	return backendAddressKey(host), true
}

func (r *HTTPProxyReconciler) applyBackendAddressNotPublic(
	ctx context.Context,
	httpProxy *networkingv1alpha.HTTPProxy,
	programmedCondition *metav1.Condition,
) bool {
	if r.backendAddresses == nil {
		return false
	}

	var hosts []string
	for _, rule := range httpProxy.Spec.Rules {
		for _, backend := range rule.Backends {
			if host, ok := resolvedBackendHost(backend); ok && !slices.Contains(hosts, host) {
				hosts = append(hosts, host)
			}
		}
	}
	if len(hosts) == 0 {
		return false
	}
	if programmedCondition.Status != metav1.ConditionTrue {
		return true
	}

	found := r.backendAddresses.observeAll(ctx, hosts)
	for ruleIndex, rule := range httpProxy.Spec.Rules {
		for backendIndex, backend := range rule.Backends {
			host, ok := resolvedBackendHost(backend)
			if !ok {
				continue
			}
			answer, nonPublic := found[host]
			if !nonPublic {
				continue
			}
			programmedCondition.Status = metav1.ConditionFalse
			programmedCondition.Reason = networkingv1alpha.HTTPProxyReasonBackendAddressNotPublic
			programmedCondition.Message = fmt.Sprintf(
				"Backend %d in rule %d points at %s, which resolves to %s in the %s range %s, as resolved from the control plane. "+
					"The edge refuses to connect to non-public addresses, so requests to this backend fail. "+
					"Point %s at a public address, or reach a private origin through a connector.",
				backendIndex, ruleIndex, host, answer.addr, answer.addrRange.Name, answer.addrRange.Prefix, host)
			return true
		}
	}
	return true
}

func applyRouteRefNotPermitted(route *gatewayv1.HTTPRoute, programmedCondition *metav1.Condition) {
	if programmedCondition.Status != metav1.ConditionTrue {
		return
	}
	for _, parent := range route.Status.Parents {
		c := apimeta.FindStatusCondition(parent.Conditions, string(gatewayv1.RouteConditionResolvedRefs))
		if c == nil || c.Status != metav1.ConditionFalse || c.Reason != string(gatewayv1.RouteReasonRefNotPermitted) {
			continue
		}
		if c.ObservedGeneration != route.Generation {
			continue
		}
		detail, ok := nonPublicBackendAddressDetail(c.Message)
		if !ok {
			continue
		}
		programmedCondition.Status = metav1.ConditionFalse
		programmedCondition.Reason = networkingv1alpha.HTTPProxyReasonBackendAddressNotPublic
		programmedCondition.Message = sanitizeConditionMessage(
			"An instance backend's address is not public, so the edge refuses to connect to it: " + detail + ".")
		return
	}
}
