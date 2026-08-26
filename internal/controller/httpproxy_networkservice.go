// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// maxEndpointsPerSlice is the number of endpoints a single EndpointSlice
// carries before another is started.
const maxEndpointsPerSlice = 100

// NetworkServiceBackendLabel names the NetworkService whose membership an
// EndpointSlice was generated from.
const NetworkServiceBackendLabel = "networking.datumapis.com/network-service"

// errNetworkServiceBackendNotFound is returned when a networkService backend
// names a NetworkService that does not exist, or a port that service does not
// declare.
type errNetworkServiceBackendNotFound struct {
	service string
	port    string
}

func (e *errNetworkServiceBackendNotFound) Error() string {
	if e.port != "" {
		return fmt.Sprintf("NetworkService %q has no port named %q", e.service, e.port)
	}
	return fmt.Sprintf("referenced NetworkService %q not found", e.service)
}

// resolvedNetworkService is a NetworkService's membership, expressed as the
// endpoints a backend referencing it should forward to.
type resolvedNetworkService struct {
	port        int32
	addressType discoveryv1.AddressType
	endpoints   []discoveryv1.Endpoint
}

// resolveNetworkServiceBackend resolves a NetworkService and the named port on
// it, then evaluates the service's claim selector directly rather than reading
// a membership from the service's status.
func resolveNetworkServiceBackend(
	ctx context.Context,
	cl client.Client,
	namespace string,
	ref *networkingv1alpha.NetworkServiceBackendRef,
) (*resolvedNetworkService, error) {
	logger := log.FromContext(ctx)

	var service networkingv1alpha.NetworkService
	key := client.ObjectKey{Namespace: namespace, Name: ref.Name}
	if err := cl.Get(ctx, key, &service); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, &errNetworkServiceBackendNotFound{service: ref.Name}
		}
		return nil, fmt.Errorf("failed getting network service %q: %w", ref.Name, err)
	}

	portIndex := slices.IndexFunc(service.Spec.Ports, func(p networkingv1alpha.NetworkServicePort) bool {
		return p.Name == ref.Port
	})
	if portIndex < 0 {
		return nil, &errNetworkServiceBackendNotFound{service: ref.Name, port: ref.Port}
	}

	selector, err := metav1.LabelSelectorAsSelector(&service.Spec.NetworkInterfaceClaims.Selector)
	if err != nil {
		return nil, fmt.Errorf("invalid claim selector on network service %q: %w", ref.Name, err)
	}

	var claims networkingv1alpha.NetworkInterfaceClaimList
	if err := cl.List(ctx, &claims,
		client.InNamespace(namespace),
		client.MatchingLabelsSelector{Selector: selector},
	); err != nil {
		return nil, fmt.Errorf("failed listing network interface claims for network service %q: %w", ref.Name, err)
	}

	members := slices.Clone(claims.Items)
	slices.SortFunc(members, func(a, b networkingv1alpha.NetworkInterfaceClaim) int {
		return strings.Compare(a.Name, b.Name)
	})

	resolved := &resolvedNetworkService{
		port:        service.Spec.Ports[portIndex].Port,
		addressType: discoveryv1.AddressTypeIPv4,
	}

	addressTypeSet := false
	for i := range members {
		claim := &members[i]

		candidates := claimBackhaulAddresses(claim)
		if len(candidates) == 0 {
			logger.Info("network service member holds no address", "networkService", ref.Name, "claim", claim.Name)
			continue
		}

		if !addressTypeSet {
			addressType, ok := addressTypeForAddress(candidates[0])
			if !ok {
				logger.Info("network service member holds an unparseable address", "networkService", ref.Name, "claim", claim.Name)
				continue
			}
			resolved.addressType = addressType
			addressTypeSet = true
		}

		address, ok := addressOfType(candidates, resolved.addressType)
		if !ok {
			logger.Info("network service member holds no address of the service's family",
				"networkService", ref.Name, "claim", claim.Name, "addressType", resolved.addressType)
			continue
		}

		resolved.endpoints = append(resolved.endpoints, networkServiceEndpoint(claim, address))
	}

	return resolved, nil
}

// claimBackhaulAddresses returns the addresses a claim can be reached at, in
// the order the edge should prefer them. External addresses come first because
// backhaul from the edge crosses the public internet.
func claimBackhaulAddresses(claim *networkingv1alpha.NetworkInterfaceClaim) []string {
	var addresses []string
	for _, external := range claim.Status.ExternalAddresses {
		if address, _, _ := strings.Cut(external.Address, "/"); address != "" {
			addresses = append(addresses, address)
		}
	}
	for _, internal := range claim.Status.Addresses {
		if address, _, _ := strings.Cut(internal.Address, "/"); address != "" {
			addresses = append(addresses, address)
		}
	}
	return addresses
}

func addressTypeForAddress(address string) (discoveryv1.AddressType, bool) {
	ip := net.ParseIP(address)
	if ip == nil {
		return "", false
	}
	if ip.To4() != nil {
		return discoveryv1.AddressTypeIPv4, true
	}
	return discoveryv1.AddressTypeIPv6, true
}

func addressOfType(candidates []string, addressType discoveryv1.AddressType) (string, bool) {
	for _, candidate := range candidates {
		if t, ok := addressTypeForAddress(candidate); ok && t == addressType {
			return candidate, true
		}
	}
	return "", false
}

// networkServiceEndpoint builds the endpoint for one member. Zone carries the
// claim's location, which is what Envoy Gateway turns into a locality.
func networkServiceEndpoint(claim *networkingv1alpha.NetworkInterfaceClaim, address string) discoveryv1.Endpoint {
	ready := apimeta.IsStatusConditionTrue(claim.Status.Conditions, networkingv1alpha.NetworkInterfaceClaimProgrammed)

	endpoint := discoveryv1.Endpoint{
		Addresses: []string{address},
		Conditions: discoveryv1.EndpointConditions{
			Ready:       ptr.To(ready),
			Serving:     ptr.To(ready),
			Terminating: ptr.To(false),
		},
	}

	if zone := claim.Labels[networkingv1alpha.NetworkInterfaceLocationLabel]; zone != "" {
		endpoint.Zone = ptr.To(zone)
	}

	return endpoint
}

// networkServiceEndpointSlices shards a service's endpoints across as many
// EndpointSlices as the per-slice limit requires. The first shard keeps the
// name the rule's backendRef points at; every shard carries the same
// service-name label so the set is discoverable as one service's endpoints.
func networkServiceEndpointSlices(
	namespace string,
	baseName string,
	portName string,
	serviceName string,
	resolved *resolvedNetworkService,
) []*discoveryv1.EndpointSlice {
	ports := []discoveryv1.EndpointPort{
		{
			Name:        ptr.To(portName),
			Protocol:    ptr.To(corev1.ProtocolTCP),
			AppProtocol: ptr.To(SchemeHTTP),
			Port:        ptr.To(resolved.port),
		},
	}

	newSlice := func(shard int, endpoints []discoveryv1.Endpoint) *discoveryv1.EndpointSlice {
		name := baseName
		if shard > 0 {
			name = fmt.Sprintf("%s-%d", baseName, shard)
		}

		return &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
				Labels: map[string]string{
					discoveryv1.LabelServiceName: baseName,
					NetworkServiceBackendLabel:   serviceName,
				},
			},
			AddressType: resolved.addressType,
			Endpoints:   endpoints,
			Ports:       ports,
		}
	}

	if len(resolved.endpoints) == 0 {
		return []*discoveryv1.EndpointSlice{newSlice(0, nil)}
	}

	var endpointSlices []*discoveryv1.EndpointSlice
	for endpoints := range slices.Chunk(resolved.endpoints, maxEndpointsPerSlice) {
		endpointSlices = append(endpointSlices, newSlice(len(endpointSlices), endpoints))
	}

	return endpointSlices
}
