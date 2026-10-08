// SPDX-License-Identifier: AGPL-3.0-only

package spec

import (
	"fmt"
	"strings"

	"k8s.io/utils/ptr"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

type BackendInput struct {
	Endpoint       string
	TLSHostname    string
	NetworkService string
	Port           string
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
		}
	}
	backend := networkingv1alpha.HTTPProxyRuleBackend{Endpoint: in.Endpoint}
	if in.TLSHostname != "" {
		backend.TLS = &networkingv1alpha.HTTPProxyBackendTLS{Hostname: ptr.To(in.TLSHostname)}
	}
	return backend
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
