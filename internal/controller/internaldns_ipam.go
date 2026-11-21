// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// internalDNSClaimNetworkLabel names the network a DNS address claim is for,
// so every claim of a network can be found and released together.
const internalDNSClaimNetworkLabel = "networking.datumapis.com/internal-dns-network-uid"

// internalDNSClaimRoleLabel says which DNS address a claim holds, and
// internalDNSClaimRegionLabel the region of a resolver address. Claims are
// found by these labels rather than by name: IPAM refuses to recreate a claim
// under the name of one whose address it still retains.
const (
	internalDNSClaimRoleLabel   = "networking.datumapis.com/internal-dns-address"
	internalDNSClaimRegionLabel = "networking.datumapis.com/internal-dns-region"
	internalDNSServiceRole      = "service"
	internalDNSResolverRole     = "resolver"
)

// errInternalDNSAddressPending means IPAM has not assigned a DNS address yet.
var errInternalDNSAddressPending = errors.New("waiting for IPAM to assign a DNS address")

type internalDNSClaimKey struct {
	role, region string
}

func (k internalDNSClaimKey) labels(network *networkingv1alpha.Network) map[string]string {
	labels := map[string]string{internalDNSClaimNetworkLabel: string(network.UID), internalDNSClaimRoleLabel: k.role}
	if k.region != "" {
		labels[internalDNSClaimRegionLabel] = k.region
	}
	return labels
}

func serviceClaimKey() internalDNSClaimKey { return internalDNSClaimKey{role: internalDNSServiceRole} }

func resolverClaimKey(region string) internalDNSClaimKey {
	return internalDNSClaimKey{role: internalDNSResolverRole, region: region}
}

func (r *InternalDNSProjectReconciler) ipamEnabled() bool {
	return r.IPAM != nil && r.Config.IPAM.ServiceClass != ""
}

func (r *InternalDNSProjectReconciler) ipamClient(ctx context.Context, cl client.Client, namespace string) (client.Client, string, error) {
	routing, err := resolveProjectOrCluster(ctx, cl, namespace)
	if err != nil {
		return nil, "", err
	}
	ipamClient, err := r.IPAM.ClientForProject(routing.project)
	if err != nil {
		return nil, "", fmt.Errorf("build project IPAM client: %w", err)
	}
	return ipamClient, routing.projectNamespace, nil
}

// findInternalDNSClaim returns the network's live claim for key, or nil.
func findInternalDNSClaim(ctx context.Context, ipamClient client.Client, namespace string, network *networkingv1alpha.Network, key internalDNSClaimKey) (*ipamv1alpha1.IPClaim, error) {
	var claims ipamv1alpha1.IPClaimList
	if err := ipamClient.List(ctx, &claims, client.InNamespace(namespace), client.MatchingLabels(key.labels(network))); err != nil {
		return nil, fmt.Errorf("list DNS address claims: %w", err)
	}
	var found *ipamv1alpha1.IPClaim
	for i := range claims.Items {
		claim := &claims.Items[i]
		if !claim.DeletionTimestamp.IsZero() {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("network %s holds more than one DNS %s address claim", network.Name, key.role)
		}
		found = claim
	}
	return found, nil
}

// claimedAddress returns the address IPAM bound to a claim, or "" while it is
// unbound. A claim that asked for an address and was bound to another is an
// error: its address is not the network's.
func claimedAddress(claim *ipamv1alpha1.IPClaim) (string, error) {
	allocated := claim.Status.Address
	if allocated == "" && claim.Status.AllocatedCIDR != "" {
		if prefix, err := netip.ParsePrefix(claim.Status.AllocatedCIDR); err == nil && prefix.Bits() == 128 {
			allocated = prefix.Addr().String()
		}
	}
	if allocated == "" {
		return "", nil
	}
	parsed, err := netip.ParseAddr(allocated)
	if err != nil || !parsed.Is6() || parsed.Is4In6() {
		return "", fmt.Errorf("IPClaim %q returned an invalid IPv6 address %q", claim.Name, allocated)
	}
	if claim.Spec.Address != "" && !sameIPText(claim.Spec.Address, allocated) {
		return "", fmt.Errorf("IPClaim %q asked for %s but holds %s", claim.Name, claim.Spec.Address, parsed)
	}
	return parsed.String(), nil
}

// holdInternalDNSAddress ensures a /128 claim of class for the network and
// returns the address IPAM assigned, or errInternalDNSAddressPending. When a
// claim is made, a non-empty address asks for that address, which is how an
// address the network already uses is adopted; a claim that exists keeps
// whatever it holds. Claims use Delete: IPAM may hand a released address
// out again at once, and DNS refuses it for another network until every
// serving member has withdrawn it from the old one.
func holdInternalDNSAddress(
	ctx context.Context, ipamClient client.Client, namespace string, network *networkingv1alpha.Network,
	key internalDNSClaimKey, class, address string,
) (string, error) {
	scope := map[string]ipamv1alpha1.ScopeRef{
		ipamScopeRoleNetwork: {APIGroup: datumNetworkingAPIGroup, Kind: "Network", Name: network.Name},
	}
	existing, err := findInternalDNSClaim(ctx, ipamClient, namespace, network, key)
	if err != nil {
		return "", err
	}
	if existing == nil {
		claim := &ipamv1alpha1.IPClaim{}
		claim.Namespace = namespace
		claim.GenerateName = "dns-" + key.role + "-"
		claim.Labels = key.labels(network)
		claim.Spec = ipamv1alpha1.IPClaimSpec{
			ClassName:     class,
			IPFamily:      ipamv1alpha1.IPFamily(networkingv1alpha.IPv6Protocol),
			PrefixLength:  ptr.To(int32(128)),
			Address:       address,
			ReclaimPolicy: ipamv1alpha1.ReclaimDelete,
			Scope:         scope,
		}
		if err := ipamClient.Create(ctx, claim); err != nil {
			return "", fmt.Errorf("claim DNS %s address: %w", key.role, err)
		}
		existing = claim
	}
	if existing.Spec.ClassName != class ||
		existing.Spec.PrefixLength == nil || *existing.Spec.PrefixLength != 128 ||
		existing.Spec.ReclaimPolicy != ipamv1alpha1.ReclaimDelete ||
		!equality.Semantic.DeepEqual(existing.Spec.Scope, scope) {
		return "", fmt.Errorf("IPClaim %q does not match this network's DNS %s address request; the IPAM class of an existing claim cannot be changed", existing.Name, key.role)
	}
	allocated, err := claimedAddress(existing)
	if err != nil {
		return "", err
	}
	if allocated == "" {
		return "", errInternalDNSAddressPending
	}
	return allocated, nil
}

// internalDNSClaimedAddress reads the address a claim holds, without creating
// it. An empty address means none is held.
func internalDNSClaimedAddress(ctx context.Context, ipamClient client.Client, namespace string, network *networkingv1alpha.Network, key internalDNSClaimKey) (string, error) {
	claim, err := findInternalDNSClaim(ctx, ipamClient, namespace, network, key)
	if err != nil || claim == nil {
		return "", err
	}
	return claimedAddress(claim)
}

// releaseInternalDNSClaims deletes the network's DNS address claims that
// release selects, or all of them when release is nil.
func releaseInternalDNSClaims(ctx context.Context, ipamClient client.Client, namespace string, networkUID string, release func(labels map[string]string) bool) error {
	if networkUID == "" {
		return nil
	}
	var claims ipamv1alpha1.IPClaimList
	if err := ipamClient.List(ctx, &claims, client.InNamespace(namespace), client.MatchingLabels{internalDNSClaimNetworkLabel: networkUID}); err != nil {
		return fmt.Errorf("list DNS address claims: %w", err)
	}
	var errs []error
	for i := range claims.Items {
		claim := &claims.Items[i]
		if release != nil && !release(claim.Labels) {
			continue
		}
		uid := claim.UID
		if err := ipamClient.Delete(ctx, claim, client.Preconditions{UID: &uid}); err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, fmt.Errorf("release DNS address claim %q: %w", claim.Name, err))
		}
	}
	return errors.Join(errs...)
}

func accessRegionalAddress(binding *unstructured.Unstructured, field string) string {
	address, _, _ := unstructured.NestedString(binding.Object, field, "regionalAddress")
	return address
}

func sameIPText(a, b string) bool {
	left, err := netip.ParseAddr(a)
	if err != nil {
		return false
	}
	right, err := netip.ParseAddr(b)
	return err == nil && left.Unmap() == right.Unmap()
}

// addresses returns the network's service destination and, when IPAM holds
// it, its regional resolver address. Until fresh allocation is allowed,
// claims adopt the derived destination, and a regional address is claimed
// only once DNS reports the one it allocated.
func (r *InternalDNSProjectReconciler) addresses(
	ctx context.Context, cl client.Client, network *networkingv1alpha.Network, dnsContext *unstructured.Unstructured, region string,
) (string, string, error) {
	derived, err := internalDNSDestination(r.Config.ServicePrefix, dnsContext.GetUID())
	if err != nil {
		return "", "", err
	}
	if !r.ipamEnabled() {
		return derived, "", nil
	}
	ipamClient, namespace, err := r.ipamClient(ctx, cl, network.Namespace)
	if err != nil {
		return "", "", err
	}
	want := derived
	if r.Config.IPAM.FreshAllocation {
		// A network with a binding in any region keeps its destination.
		want, err = r.networkDestination(ctx, cl, network)
		if err != nil {
			return "", "", err
		}
	}
	destination, err := holdInternalDNSAddress(ctx, ipamClient, namespace, network, serviceClaimKey(), r.Config.IPAM.ServiceClass, want)
	if err != nil {
		return "", "", err
	}
	class := r.Config.IPAM.ResolverClasses[region]
	if class == "" {
		return destination, "", nil
	}
	regional, err := internalDNSClaimedAddress(ctx, ipamClient, namespace, network, resolverClaimKey(region))
	if err != nil || regional != "" {
		return destination, regional, err
	}
	existing, err := r.existingAccess(ctx, cl, network, dnsContext, region)
	if err != nil {
		return "", "", err
	}
	if existing != nil || !r.Config.IPAM.FreshAllocation {
		// Adopted after the binding reports what DNS allocated.
		return destination, "", nil
	}
	regional, err = holdInternalDNSAddress(ctx, ipamClient, namespace, network, resolverClaimKey(region), class, "")
	return destination, regional, err
}

func (r *InternalDNSProjectReconciler) adoptRegional(ctx context.Context, cl client.Client, network *networkingv1alpha.Network, region, address string) (string, error) {
	ipamClient, namespace, err := r.ipamClient(ctx, cl, network.Namespace)
	if err != nil {
		return "", err
	}
	return holdInternalDNSAddress(ctx, ipamClient, namespace, network, resolverClaimKey(region), r.Config.IPAM.ResolverClasses[region], address)
}

func (r *InternalDNSProjectReconciler) existingAccess(
	ctx context.Context, cl client.Client, network *networkingv1alpha.Network, dnsContext *unstructured.Unstructured, region string,
) (*unstructured.Unstructured, error) {
	current := newInternalDNSObject(internalDNSAccessGVK, network.Namespace, internalDNSRegionalName(dnsContext.GetName(), region))
	if err := cl.Get(ctx, client.ObjectKeyFromObject(current), current); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	if current.GetAnnotations()[internalDNSOwnerNetworkUIDAnnotation] != string(network.UID) || !current.GetDeletionTimestamp().IsZero() {
		return nil, nil
	}
	return current, nil
}

// networkDestination returns the destination any of the network's access
// bindings uses, in any region, or "".
func (r *InternalDNSProjectReconciler) networkDestination(ctx context.Context, cl client.Client, network *networkingv1alpha.Network) (string, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(internalDNSAccessGVK.GroupVersion().WithKind(internalDNSAccessGVK.Kind + "List"))
	if err := cl.List(ctx, list, client.InNamespace(network.Namespace)); err != nil {
		return "", err
	}
	for i := range list.Items {
		binding := &list.Items[i]
		if binding.GetAnnotations()[internalDNSOwnerNetworkUIDAnnotation] != string(network.UID) {
			continue
		}
		if destination, _, _ := unstructured.NestedString(binding.Object, "spec", "destinationAddress"); destination != "" {
			return destination, nil
		}
	}
	return "", nil
}
