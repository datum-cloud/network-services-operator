// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"k8s.io/utils/ptr"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

// MaxBackendWeight is the API's upper bound on a backend's weight.
const MaxBackendWeight = 1000000

type BackendInput struct {
	Endpoint       string
	TLSHostname    string
	NetworkService string
	Port           string
	// Weight is left nil to keep the API default of 1, or for AddRouteBackend
	// to pick one that gives the new origin an even share.
	Weight *int32
}

type BackendFlags struct {
	Endpoints       []string
	NetworkServices []string
	Ports           []string
	TLSHostname     string
}

func ParseBackendFlags(flags BackendFlags) ([]BackendInput, error) {
	switch {
	case len(flags.NetworkServices) > len(flags.Ports):
		return nil, util.UsageErrorf("each --network-service needs a matching --port").
			WithFix("for example:\n       --network-service storefront --port http")
	case len(flags.Ports) > len(flags.NetworkServices):
		return nil, util.UsageErrorf("--port only applies to --network-service backends").
			WithFix("for example:\n       --network-service storefront --port http")
	}

	backends := make([]BackendInput, 0, len(flags.Endpoints)+len(flags.NetworkServices))
	for _, endpoint := range flags.Endpoints {
		endpoint = strings.TrimSpace(endpoint)
		if endpoint == "" {
			return nil, util.UsageErrorf("--endpoint must not be empty")
		}
		if !strings.Contains(endpoint, "://") {
			return nil, util.UsageErrorf("endpoint %q has no scheme", endpoint).
				WithFix("use a full URL, for example:\n       --endpoint https://" + endpoint)
		}
		backends = append(backends, BackendInput{Endpoint: endpoint, TLSHostname: strings.TrimSpace(flags.TLSHostname)})
	}
	for i, name := range flags.NetworkServices {
		name = strings.TrimSpace(name)
		port := strings.TrimSpace(flags.Ports[i])
		if name == "" {
			return nil, util.UsageErrorf("--network-service must not be empty")
		}
		if port == "" {
			return nil, util.UsageErrorf("--port for network service %q must not be empty", name)
		}
		backends = append(backends, BackendInput{NetworkService: name, Port: port})
	}

	if len(backends) == 0 {
		return nil, util.UsageErrorf("at least one backend is required").
			WithFix("pass --endpoint URL, or --network-service NAME --port PORTNAME")
	}
	if flags.TLSHostname != "" && len(flags.Endpoints) == 0 {
		return nil, util.UsageErrorf("--tls-hostname only applies to --endpoint backends")
	}
	for i := range backends {
		for j := 0; j < i; j++ {
			if sameBackendTarget(ToBackend(backends[i]), ToBackend(backends[j])) {
				return nil, util.UsageErrorf("backend %s is given more than once", FormatBackend(ToBackend(backends[i])))
			}
		}
	}
	return backends, nil
}

func toBackends(inputs []BackendInput) []networkingv1alpha.HTTPProxyRuleBackend {
	out := make([]networkingv1alpha.HTTPProxyRuleBackend, 0, len(inputs))
	for _, in := range inputs {
		out = append(out, ToBackend(in))
	}
	return out
}

func ToBackend(in BackendInput) networkingv1alpha.HTTPProxyRuleBackend {
	if in.NetworkService != "" {
		return networkingv1alpha.HTTPProxyRuleBackend{
			NetworkService: &networkingv1alpha.NetworkServiceBackendRef{Name: in.NetworkService, Port: in.Port},
			Weight:         in.Weight,
		}
	}
	backend := networkingv1alpha.HTTPProxyRuleBackend{Endpoint: in.Endpoint, Weight: in.Weight}
	if in.TLSHostname != "" {
		backend.TLS = &networkingv1alpha.HTTPProxyBackendTLS{Hostname: ptr.To(in.TLSHostname)}
	}
	return backend
}

func ValidateWeight(weight int32) error {
	if weight < 0 || weight > MaxBackendWeight {
		return util.UsageErrorf("--weight must be between 0 and %d", MaxBackendWeight).
			WithFix("weights are relative: two origins at 1 and 3 get 25% and 75% of requests")
	}
	return nil
}

// BackendWeight is the weight the API applies: 1 when the backend omits it.
func BackendWeight(backend networkingv1alpha.HTTPProxyRuleBackend) int32 {
	if backend.Weight == nil {
		return 1
	}
	return *backend.Weight
}

// suggestedWeight gives an origin joining a pool the mean of the pool's
// non-zero weights, so it starts with an even share rather than a sliver (1
// beside two 50s is under 1%). It stays nil while no origin sets a weight,
// leaving every origin on the API default.
func suggestedWeight(backends []networkingv1alpha.HTTPProxyRuleBackend) *int32 {
	explicit := false
	var sum, count int64
	for _, b := range backends {
		if b.Weight != nil {
			explicit = true
		}
		if w := BackendWeight(b); w > 0 {
			sum += int64(w)
			count++
		}
	}
	if !explicit || count == 0 {
		return nil
	}
	mean := int32(math.Round(float64(sum) / float64(count)))
	return ptr.To(max(mean, 1))
}

// ShareLabels gives each backend's share of its route's requests as whole
// percentages that add up to 100%. Largest-remainder rounding hands the
// leftover points to the shares closest to rounding up, so 37.5/62.5 reads
// 38%/62% rather than 38%/63%. A non-zero share that still rounds to 0 reads
// "<1%" so it doesn't look drained. Matches the cloud portal's labels.
func ShareLabels(backends []networkingv1alpha.HTTPProxyRuleBackend) []string {
	var total int64
	for _, b := range backends {
		total += int64(BackendWeight(b))
	}
	labels := make([]string, len(backends))
	if total == 0 {
		for i := range labels {
			labels[i] = "0%"
		}
		return labels
	}

	shares := make([]float64, len(backends))
	rounded := make([]int, len(backends))
	leftover := 100
	for i, b := range backends {
		shares[i] = float64(BackendWeight(b)) * 100 / float64(total)
		rounded[i] = int(math.Floor(shares[i]))
		leftover -= rounded[i]
	}
	order := make([]int, len(backends))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return shares[order[a]]-float64(rounded[order[a]]) > shares[order[b]]-float64(rounded[order[b]])
	})
	for _, i := range order {
		if leftover <= 0 {
			break
		}
		rounded[i]++
		leftover--
	}
	for i := range labels {
		if rounded[i] == 0 && shares[i] > 0 {
			labels[i] = "<1%"
			continue
		}
		labels[i] = fmt.Sprintf("%d%%", rounded[i])
	}
	return labels
}

func FormatBackend(backend networkingv1alpha.HTTPProxyRuleBackend) string {
	switch {
	case backend.NetworkService != nil:
		return backend.NetworkService.Name + ":" + backend.NetworkService.Port
	case backend.Instance != nil:
		return fmt.Sprintf("instance %s:%d", backend.Instance.Name, backend.Instance.Port)
	case backend.Connector != nil && backend.Endpoint != "":
		return backend.Endpoint + " via " + backend.Connector.Name
	default:
		return backend.Endpoint
	}
}

func BackendKind(backend networkingv1alpha.HTTPProxyRuleBackend) string {
	switch {
	case backend.NetworkService != nil:
		return "network-service"
	case backend.Instance != nil:
		return "instance"
	case backend.Connector != nil:
		return "connector"
	default:
		return "url"
	}
}

func sameBackendTarget(a, b networkingv1alpha.HTTPProxyRuleBackend) bool {
	switch {
	case a.NetworkService != nil || b.NetworkService != nil:
		return a.NetworkService != nil && b.NetworkService != nil &&
			a.NetworkService.Name == b.NetworkService.Name &&
			a.NetworkService.Port == b.NetworkService.Port
	case a.Instance != nil || b.Instance != nil:
		return a.Instance != nil && b.Instance != nil && *a.Instance == *b.Instance
	default:
		return strings.EqualFold(strings.TrimSuffix(a.Endpoint, "/"), strings.TrimSuffix(b.Endpoint, "/"))
	}
}
