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
// it, then evaluates the service's interface selector directly rather than
// reading a membership from the service's status.
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

	selector, err := metav1.LabelSelectorAsSelector(&service.Spec.NetworkInterfaces.Selector)
	if err != nil {
		return nil, fmt.Errorf("invalid interface selector on network service %q: %w", ref.Name, err)
	}

	members, err := matchingInterfaces(ctx, cl, namespace, selector)
	if err != nil {
		return nil, fmt.Errorf("failed resolving the members of network service %q: %w", ref.Name, err)
	}

	slices.SortFunc(members, func(a, b networkingv1alpha.NetworkInterface) int {
		return strings.Compare(a.Name, b.Name)
	})

	resolved := &resolvedNetworkService{
		port:        service.Spec.Ports[portIndex].Port,
		addressType: discoveryv1.AddressTypeIPv4,
	}

	addressTypeSet := false
	for i := range members {
		member := &members[i]

		candidates := interfaceBackhaulAddresses(member)
		if len(candidates) == 0 {
			logger.Info("network service member holds no address", "networkService", ref.Name, "interface", member.Name)
			continue
		}

		if !addressTypeSet {
			addressType, ok := addressTypeForAddress(candidates[0])
			if !ok {
				logger.Info("network service member holds an unparseable address", "networkService", ref.Name, "interface", member.Name)
				continue
			}
			resolved.addressType = addressType
			addressTypeSet = true
		}

		address, ok := addressOfType(candidates, resolved.addressType)
		if !ok {
			logger.Info("network service member holds no address of the service's family",
				"networkService", ref.Name, "interface", member.Name, "addressType", resolved.addressType)
			continue
		}

		resolved.endpoints = append(resolved.endpoints, networkServiceEndpoint(member, address))
	}

	return resolved, nil
}

// interfaceBackhaulAddresses returns the addresses a member can be reached at.
//
// Only the addresses a member holds inside its network. An edge reaches a
// member over the fabric, so the address it dials is the tenant one and the
// external addresses a member may also hold are deliberately not offered: an
// edge that dialled one would leave the fabric and cross the public internet
// to reach an origin that is meant to stay private.
func interfaceBackhaulAddresses(member *networkingv1alpha.NetworkInterface) []string {
	var addresses []string
	for _, internal := range member.Spec.Addresses {
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
// member's location, which is what Envoy Gateway turns into a locality.
func networkServiceEndpoint(member *networkingv1alpha.NetworkInterface, address string) discoveryv1.Endpoint {
	ready := isServiceMemberHealthy(member)

	endpoint := discoveryv1.Endpoint{
		Addresses: []string{address},
		Conditions: discoveryv1.EndpointConditions{
			Ready:       ptr.To(ready),
			Serving:     ptr.To(ready),
			Terminating: ptr.To(false),
		},
	}

	if zone := member.Labels[networkingv1alpha.NetworkInterfaceLocationLabel]; zone != "" {
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

	// A service with no members still gets a slice, empty. The rule's
	// backendRef names shards[0], and the Gateway controller resolves that
	// reference by getting the upstream EndpointSlice before it can build the
	// downstream Service. A NotFound there aborts the whole route loop for the
	// Gateway, not just this route, so withholding the slice would stop every
	// other HTTPProxy on that Gateway from being reprogrammed for as long as
	// the service had no members. Carrying it empty keeps the reference
	// resolvable and costs only this rule, which Envoy answers 503 on a
	// cluster with no endpoints.
	//
	// This holds because the slice stays in one place. If these are ever
	// authored centrally and fanned out to every edge, an empty slice becomes
	// a fleet-wide outage and the withholding decision has to be revisited
	// together with the route loop's per-route error isolation.
	if len(resolved.endpoints) == 0 {
		return []*discoveryv1.EndpointSlice{newSlice(0, nil)}
	}

	var endpointSlices []*discoveryv1.EndpointSlice
	for endpoints := range slices.Chunk(resolved.endpoints, maxEndpointsPerSlice) {
		endpointSlices = append(endpointSlices, newSlice(len(endpointSlices), endpoints))
	}

	return endpointSlices
}
