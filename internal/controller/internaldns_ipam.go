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

// errInternalDNSAddressPending means IPAM has not assigned a DNS address yet.
var errInternalDNSAddressPending = errors.New("waiting for IPAM to assign a DNS address")

func internalDNSServiceClaimName(network *networkingv1alpha.Network) string {
	return boundedDNSName("dns-service-"+network.Name, string(network.UID)+"/service")
}

func internalDNSResolverClaimName(network *networkingv1alpha.Network, region string) string {
	return boundedDNSName("dns-resolver-"+network.Name+"-"+region, string(network.UID)+"/resolver/"+region)
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

// holdInternalDNSAddress ensures a /128 claim of class for the network and
// returns the address IPAM assigned, or errInternalDNSAddressPending. A
// non-empty address asks for that address, which is how an address the
// network already uses is adopted. The claim is kept under Retain so the
// class's retention lease holds the address after the claim is deleted,
// until every serving member has withdrawn it.
func holdInternalDNSAddress(
	ctx context.Context, ipamClient client.Client, namespace string, network *networkingv1alpha.Network,
	name, class, address string,
) (string, error) {
	claim := &ipamv1alpha1.IPClaim{}
	claim.Namespace, claim.Name = namespace, name
	claim.Labels = map[string]string{internalDNSClaimNetworkLabel: string(network.UID)}
	claim.Spec = ipamv1alpha1.IPClaimSpec{
		ClassName:     class,
		IPFamily:      ipamv1alpha1.IPFamily(networkingv1alpha.IPv6Protocol),
		PrefixLength:  ptr.To(int32(128)),
		Address:       address,
		ReclaimPolicy: ipamv1alpha1.ReclaimRetain,
		Scope: map[string]ipamv1alpha1.ScopeRef{
			ipamScopeRoleNetwork: {APIGroup: datumNetworkingAPIGroup, Kind: "Network", Name: network.Name},
		},
	}
	existing := &ipamv1alpha1.IPClaim{}
	switch err := ipamClient.Get(ctx, client.ObjectKeyFromObject(claim), existing); {
	case apierrors.IsNotFound(err):
		if err := ipamClient.Create(ctx, claim); err != nil && !apierrors.IsAlreadyExists(err) {
			return "", fmt.Errorf("claim DNS address %q: %w", name, err)
		}
		return "", errInternalDNSAddressPending
	case err != nil:
		return "", err
	}
	if !existing.DeletionTimestamp.IsZero() {
		return "", fmt.Errorf("%w: IPClaim %q is being deleted", errInternalDNSAddressPending, name)
	}
	if existing.Labels[internalDNSClaimNetworkLabel] != string(network.UID) ||
		existing.Spec.ClassName != class ||
		existing.Spec.PrefixLength == nil || *existing.Spec.PrefixLength != 128 ||
		existing.Spec.ReclaimPolicy != ipamv1alpha1.ReclaimRetain ||
		address != "" && existing.Spec.Address != "" && !sameIPText(existing.Spec.Address, address) ||
		!equality.Semantic.DeepEqual(existing.Spec.Scope, claim.Spec.Scope) {
		return "", fmt.Errorf("IPClaim %q does not match this network's DNS address request", name)
	}
	allocated := existing.Status.Address
	if allocated == "" && existing.Status.AllocatedCIDR != "" {
		if prefix, err := netip.ParsePrefix(existing.Status.AllocatedCIDR); err == nil && prefix.Bits() == 128 {
			allocated = prefix.Addr().String()
		}
	}
	if allocated == "" {
		return "", errInternalDNSAddressPending
	}
	parsed, err := netip.ParseAddr(allocated)
	if err != nil || !parsed.Is6() || parsed.Is4In6() {
		return "", fmt.Errorf("IPClaim %q returned an invalid IPv6 address %q", name, allocated)
	}
	if address != "" && parsed != netip.MustParseAddr(address).Unmap() {
		return "", fmt.Errorf("IPClaim %q holds %s, not the address %s it adopts", name, parsed, address)
	}
	return parsed.String(), nil
}

// internalDNSClaimedAddress reads the address a claim holds, without creating
// it. An empty address means none is held.
func internalDNSClaimedAddress(ctx context.Context, ipamClient client.Client, namespace, name string, network *networkingv1alpha.Network) (string, error) {
	claim := &ipamv1alpha1.IPClaim{}
	if err := ipamClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, claim); err != nil {
		return "", client.IgnoreNotFound(err)
	}
	if claim.Labels[internalDNSClaimNetworkLabel] != string(network.UID) || !claim.DeletionTimestamp.IsZero() {
		return "", nil
	}
	if claim.Status.Address != "" {
		return claim.Status.Address, nil
	}
	if prefix, err := netip.ParsePrefix(claim.Status.AllocatedCIDR); err == nil && prefix.Bits() == 128 {
		return prefix.Addr().String(), nil
	}
	return "", nil
}

// releaseInternalDNSClaims deletes the network's DNS address claims that keep
// returns false for.
func releaseInternalDNSClaims(ctx context.Context, ipamClient client.Client, namespace string, networkUID string, keep func(name string) bool) error {
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
		if keep != nil && keep(claim.Name) {
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
		// A network that already has a binding keeps its destination.
		want = ""
		if held, err := internalDNSClaimedAddress(ctx, ipamClient, namespace, internalDNSServiceClaimName(network), network); err != nil {
			return "", "", err
		} else if held == "" {
			if existing, err := r.existingAccess(ctx, cl, network, dnsContext, region); err != nil {
				return "", "", err
			} else if existing != nil {
				want, _, _ = unstructured.NestedString(existing.Object, "spec", "destinationAddress")
			}
		}
	}
	destination, err := holdInternalDNSAddress(ctx, ipamClient, namespace, network, internalDNSServiceClaimName(network), r.Config.IPAM.ServiceClass, want)
	if err != nil {
		return "", "", err
	}
	class := r.Config.IPAM.ResolverClasses[region]
	if class == "" {
		return destination, "", nil
	}
	claimName := internalDNSResolverClaimName(network, region)
	regional, err := internalDNSClaimedAddress(ctx, ipamClient, namespace, claimName, network)
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
	regional, err = holdInternalDNSAddress(ctx, ipamClient, namespace, network, claimName, class, "")
	return destination, regional, err
}

func (r *InternalDNSProjectReconciler) adoptRegional(ctx context.Context, cl client.Client, network *networkingv1alpha.Network, region, address string) (string, error) {
	ipamClient, namespace, err := r.ipamClient(ctx, cl, network.Namespace)
	if err != nil {
		return "", err
	}
	return holdInternalDNSAddress(ctx, ipamClient, namespace, network, internalDNSResolverClaimName(network, region), r.Config.IPAM.ResolverClasses[region], address)
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
