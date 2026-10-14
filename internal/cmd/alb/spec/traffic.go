// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"fmt"
	"regexp"
	"strings"

	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

// AlgorithmDefault unsets spec.loadBalancer. The operator then attaches no
// policy and Envoy Gateway falls back to least request.
const AlgorithmDefault = "default"

var algorithms = map[string]networkingv1alpha.HTTPProxyLoadBalancerType{
	"roundrobin":     networkingv1alpha.HTTPProxyLoadBalancerTypeRoundRobin,
	"random":         networkingv1alpha.HTTPProxyLoadBalancerTypeRandom,
	"leastrequest":   networkingv1alpha.HTTPProxyLoadBalancerTypeLeastRequest,
	"consistenthash": networkingv1alpha.HTTPProxyLoadBalancerTypeConsistentHash,
}

// gatewayDurationPattern is the Gateway API Duration format: up to four
// <int><unit> groups, for example 30s or 1m30s.
var gatewayDurationPattern = regexp.MustCompile(`^([0-9]{1,5}(h|m|s|ms)){1,4}$`)

// LoadBalancerInput is what "alb update" asks of spec.loadBalancer. An empty
// Algorithm leaves it alone unless HashHeader is set.
type LoadBalancerInput struct {
	Algorithm  string
	HashHeader string
}

// HealthCheckInput is what "alb update" asks of spec.healthCheck. Enabled
// nil with no tuning leaves it alone; any tuning value turns passive checks
// on, keeping the values not given.
type HealthCheckInput struct {
	Enabled              *bool
	Consecutive5xxErrors *int32
	BaseEjectionTime     *string
	MaxEjectionPercent   *int32
}

func (in LoadBalancerInput) IsSet() bool {
	return in.Algorithm != "" || in.HashHeader != ""
}

func (in HealthCheckInput) IsSet() bool {
	return in.Enabled != nil || in.tuned()
}

func (in HealthCheckInput) tuned() bool {
	return in.Consecutive5xxErrors != nil || in.BaseEjectionTime != nil || in.MaxEjectionPercent != nil
}

// BuildLoadBalancer turns the flags into spec.loadBalancer; nil means unset.
func BuildLoadBalancer(in LoadBalancerInput) (*networkingv1alpha.HTTPProxyLoadBalancer, error) {
	header := strings.TrimSpace(in.HashHeader)
	key := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(in.Algorithm)))

	if key == AlgorithmDefault {
		if header != "" {
			return nil, util.UsageErrorf("--hash-header only applies to --algorithm consistent-hash")
		}
		return nil, nil
	}
	if key == "" && header != "" {
		key = "consistenthash"
	}
	algorithm, ok := algorithms[key]
	if !ok {
		return nil, util.UsageErrorf("unknown algorithm %q", in.Algorithm).
			WithFix("use round-robin, random, least-request, consistent-hash, or default")
	}

	lb := &networkingv1alpha.HTTPProxyLoadBalancer{Type: algorithm}
	if algorithm != networkingv1alpha.HTTPProxyLoadBalancerTypeConsistentHash {
		if header != "" {
			return nil, util.UsageErrorf("--hash-header only applies to --algorithm consistent-hash")
		}
		return lb, nil
	}
	lb.ConsistentHash = &networkingv1alpha.HTTPProxyConsistentHash{Type: networkingv1alpha.HTTPProxyConsistentHashTypeSourceIP}
	if header != "" {
		if len(header) > 256 {
			return nil, util.UsageErrorf("--hash-header must be 256 characters or fewer")
		}
		lb.ConsistentHash = &networkingv1alpha.HTTPProxyConsistentHash{
			Type:   networkingv1alpha.HTTPProxyConsistentHashTypeHeader,
			Header: ptr.To(header),
		}
	}
	return lb, nil
}

// ApplyHealthCheck returns spec.healthCheck after the flags; nil means off.
func ApplyHealthCheck(current *networkingv1alpha.HTTPProxyHealthCheck, in HealthCheckInput) (*networkingv1alpha.HTTPProxyHealthCheck, error) {
	if in.Enabled != nil && !*in.Enabled {
		if in.tuned() {
			return nil, util.UsageErrorf("cannot tune health checks while turning them off").
				WithFix("drop --no-health-checks, or drop the tuning flags")
		}
		return nil, nil
	}

	passive := &networkingv1alpha.HTTPProxyPassiveHealthCheck{}
	if current != nil && current.Passive != nil {
		passive = current.Passive.DeepCopy()
	}
	if v := in.Consecutive5xxErrors; v != nil {
		if *v < 1 {
			return nil, util.UsageErrorf("--consecutive-5xx must be at least 1")
		}
		passive.Consecutive5xxErrors = ptr.To(*v)
	}
	if v := in.BaseEjectionTime; v != nil {
		d := strings.TrimSpace(*v)
		if !gatewayDurationPattern.MatchString(d) {
			return nil, util.UsageErrorf("--base-ejection-time %q is not a duration", *v).
				WithFix("use a value such as 30s, 2m or 1m30s")
		}
		passive.BaseEjectionTime = ptr.To(gatewayv1.Duration(d))
	}
	if v := in.MaxEjectionPercent; v != nil {
		if *v < 1 || *v > 100 {
			return nil, util.UsageErrorf("--max-ejection-percent must be between 1 and 100")
		}
		passive.MaxEjectionPercent = ptr.To(*v)
	}
	return &networkingv1alpha.HTTPProxyHealthCheck{Passive: passive}, nil
}

// LoadBalancingSummary describes spec.loadBalancer for describe.
func LoadBalancingSummary(proxy *networkingv1alpha.HTTPProxy) string {
	lb := proxy.Spec.LoadBalancer
	if lb == nil {
		return "least request (default)"
	}
	switch lb.Type {
	case networkingv1alpha.HTTPProxyLoadBalancerTypeRoundRobin:
		return "round robin"
	case networkingv1alpha.HTTPProxyLoadBalancerTypeRandom:
		return "random"
	case networkingv1alpha.HTTPProxyLoadBalancerTypeLeastRequest:
		return "least request"
	case networkingv1alpha.HTTPProxyLoadBalancerTypeConsistentHash:
		if lb.ConsistentHash != nil && lb.ConsistentHash.Type == networkingv1alpha.HTTPProxyConsistentHashTypeHeader && lb.ConsistentHash.Header != nil {
			return "consistent hash on header " + *lb.ConsistentHash.Header
		}
		return "consistent hash on source IP"
	}
	return string(lb.Type)
}

// HealthCheckSummary describes spec.healthCheck for describe, with the API
// defaults filled in for values left unset.
func HealthCheckSummary(proxy *networkingv1alpha.HTTPProxy) string {
	hc := proxy.Spec.HealthCheck
	if hc == nil || hc.Passive == nil {
		return "off"
	}
	consecutive := ptr.Deref(hc.Passive.Consecutive5xxErrors, networkingv1alpha.DefaultPassiveConsecutive5xxErrors)
	ejection := ptr.Deref(hc.Passive.BaseEjectionTime, networkingv1alpha.DefaultPassiveBaseEjectionTime)
	maxPercent := ptr.Deref(hc.Passive.MaxEjectionPercent, networkingv1alpha.DefaultPassiveMaxEjectionPercent)
	return fmt.Sprintf("passive: eject after %d consecutive 5xx for %s, max %d%% ejected", consecutive, ejection, maxPercent)
}
