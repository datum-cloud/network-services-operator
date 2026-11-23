// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	ipamv1alpha1 "go.miloapis.com/ipam/pkg/apis/ipam/v1alpha1"
	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

type ipamDNSScenario struct {
	t       *testing.T
	ctx     context.Context
	cl      client.Client
	ipam    client.Client
	r       *InternalDNSProjectReconciler
	network *networkingv1alpha.Network
	nc      *networkingv1alpha.NetworkContext
}

func newIPAMDNSScenario(t *testing.T, fresh bool) *ipamDNSScenario {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{networkingv1alpha.AddToScheme, locationsv1alpha1.AddToScheme, corev1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	scheme.AddKnownTypeWithName(internalDNSContextGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(internalDNSContextGVK.GroupVersion().WithKind("DNSResolverContextList"), &unstructured.UnstructuredList{})
	scheme.AddKnownTypeWithName(internalDNSAccessGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(internalDNSAccessGVK.GroupVersion().WithKind("DNSResolverAccessBindingList"), &unstructured.UnstructuredList{})
	network := &networkingv1alpha.Network{ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "blue", UID: "network-uid"}}
	location := &locationsv1alpha1.Location{ObjectMeta: metav1.ObjectMeta{Name: "ord-1"}, Spec: locationsv1alpha1.LocationSpec{Topology: map[string]string{internalDNSRegionTopologyKey: "us-central"}}}
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant"}}
	nc := testNetworkContext("first", "context-1")
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(network, location, namespace, nc).Build()
	ipamScheme, err := IPAMScheme()
	if err != nil {
		t.Fatal(err)
	}
	ipam := fake.NewClientBuilder().WithScheme(ipamScheme).WithStatusSubresource(&ipamv1alpha1.IPClaim{}).Build()
	r := &InternalDNSProjectReconciler{
		Config: config.InternalDNSConfig{
			Frontend: "fd53::53", ServicePrefix: "fd70:100::/64",
			IPAM: config.InternalDNSIPAM{ServiceClass: "dns-service", ResolverClasses: map[string]string{"us-central": "dns-resolver-us-central"}, FreshAllocation: fresh},
		},
		Projects: fixedProjectIdentity("project-uid"),
		IPAM:     fixedIPAMClient{ipam},
		Now:      func() time.Time { return time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC) },
	}
	return &ipamDNSScenario{t: t, ctx: mccontext.WithCluster(context.Background(), testProject), cl: cl, ipam: ipam, r: r, network: network, nc: nc}
}

func (s *ipamDNSScenario) reconcile() {
	s.t.Helper()
	if _, err := s.r.reconcile(s.ctx, s.cl, testProject, client.ObjectKeyFromObject(s.nc)); err != nil {
		s.t.Fatal(err)
	}
}

// readyContext marks the DNS context DNS created as ready.
func (s *ipamDNSScenario) readyContext() *unstructured.Unstructured {
	s.t.Helper()
	dnsContext := newInternalDNSObject(internalDNSContextGVK, "tenant", internalDNSResourceName("blue", s.network.UID))
	if err := s.cl.Get(s.ctx, client.ObjectKeyFromObject(dnsContext), dnsContext); err != nil {
		s.t.Fatal(err)
	}
	dnsContext.SetUID("dns-context-uid")
	dnsContext.SetGeneration(1)
	_ = unstructured.SetNestedSlice(dnsContext.Object, []any{map[string]any{"type": "Ready", "status": "True", "observedGeneration": int64(1)}}, "status", "conditions")
	_ = unstructured.SetNestedField(dnsContext.Object, internalDNSDefaultDomain, "status", "managedNamespace", "suffix")
	_ = unstructured.SetNestedMap(dnsContext.Object, map[string]any{"name": "blue-internal"}, "status", "managedNamespace", "dnsZoneRef")
	if err := s.cl.Update(s.ctx, dnsContext); err != nil {
		s.t.Fatal(err)
	}
	return dnsContext
}

func (s *ipamDNSScenario) claim(key internalDNSClaimKey) *ipamv1alpha1.IPClaim {
	s.t.Helper()
	claim, err := findInternalDNSClaim(s.ctx, s.ipam, "tenant", s.network, key)
	if err != nil {
		s.t.Fatal(err)
	}
	return claim
}

func (s *ipamDNSScenario) allocate(key internalDNSClaimKey, address string) {
	s.t.Helper()
	claim := s.claim(key)
	if claim == nil {
		s.t.Fatalf("claim %v was not made", key)
	}
	claim.Status.Address = address
	if err := s.ipam.Status().Update(s.ctx, claim); err != nil {
		s.t.Fatal(err)
	}
}

func (s *ipamDNSScenario) binding(dnsContext *unstructured.Unstructured) *unstructured.Unstructured {
	s.t.Helper()
	binding := newInternalDNSObject(internalDNSAccessGVK, "tenant", internalDNSRegionalName(dnsContext.GetName(), "us-central"))
	if err := s.cl.Get(s.ctx, client.ObjectKeyFromObject(binding), binding); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		s.t.Fatal(err)
	}
	return binding
}

func TestInternalDNSAdoptsExistingAddressesIntoIPAM(t *testing.T) {
	s := newIPAMDNSScenario(t, false)
	s.reconcile()
	dnsContext := s.readyContext()
	derived, err := internalDNSDestination("fd70:100::/64", dnsContext.GetUID())
	if err != nil {
		t.Fatal(err)
	}
	s.reconcile()
	service := s.claim(serviceClaimKey())
	if service == nil || service.Spec.Address != derived || service.Spec.ReclaimPolicy != ipamv1alpha1.ReclaimRetain || service.Spec.ClassName != "dns-service" {
		t.Fatalf("service address was not adopted: %#v", service)
	}
	if s.binding(dnsContext) != nil {
		t.Fatal("access was written before IPAM held its destination")
	}
	s.allocate(serviceClaimKey(), derived)
	s.reconcile()
	binding := s.binding(dnsContext)
	if binding == nil {
		t.Fatal("access was not written")
	}
	if got, _, _ := unstructured.NestedString(binding.Object, "spec", "destinationAddress"); got != derived {
		t.Fatalf("destination = %s, want %s", got, derived)
	}
	if accessRegionalAddress(binding, "spec") != "" || s.claim(resolverClaimKey("us-central")) != nil {
		t.Fatal("a regional address was claimed before DNS reported the one it allocated")
	}

	_ = unstructured.SetNestedField(binding.Object, "fd54::5", "status", "regionalAddress")
	if err := s.cl.Update(s.ctx, binding); err != nil {
		t.Fatal(err)
	}
	s.reconcile()
	resolverName := resolverClaimKey("us-central")
	resolver := s.claim(resolverName)
	if resolver == nil || resolver.Spec.Address != "fd54::5" || resolver.Spec.ClassName != "dns-resolver-us-central" {
		t.Fatalf("regional address was not adopted: %#v", resolver)
	}
	s.allocate(resolverName, "fd54::5")
	s.reconcile()
	if got := accessRegionalAddress(s.binding(dnsContext), "spec"); got != "fd54::5" {
		t.Fatalf("access does not carry the adopted regional address: %q", got)
	}
}

func TestInternalDNSFreshAllocationUsesIPAMAddresses(t *testing.T) {
	s := newIPAMDNSScenario(t, true)
	s.reconcile()
	dnsContext := s.readyContext()
	s.reconcile()
	service := s.claim(serviceClaimKey())
	if service == nil || service.Spec.Address != "" {
		t.Fatalf("a new network did not ask IPAM for a destination: %#v", service)
	}
	s.allocate(serviceClaimKey(), "fd70:100::99")
	s.reconcile()
	resolverName := resolverClaimKey("us-central")
	if resolver := s.claim(resolverName); resolver == nil || resolver.Spec.Address != "" {
		t.Fatalf("a new network did not ask IPAM for a regional address: %#v", resolver)
	}
	if s.binding(dnsContext) != nil {
		t.Fatal("access was written before IPAM held its regional address")
	}
	s.allocate(resolverName, "fd54::7")
	s.reconcile()
	binding := s.binding(dnsContext)
	destination, _, _ := unstructured.NestedString(binding.Object, "spec", "destinationAddress")
	if destination != "fd70:100::99" || accessRegionalAddress(binding, "spec") != "fd54::7" {
		t.Fatalf("access does not carry the IPAM addresses: %#v", binding.Object["spec"])
	}
}

func TestInternalDNSDisableReleasesClaims(t *testing.T) {
	s := newIPAMDNSScenario(t, true)
	s.reconcile()
	s.readyContext()
	s.reconcile()
	s.allocate(serviceClaimKey(), "fd70:100::99")
	s.reconcile()
	s.allocate(resolverClaimKey("us-central"), "fd54::7")
	s.reconcile()
	network := &networkingv1alpha.Network{}
	if err := s.cl.Get(s.ctx, client.ObjectKeyFromObject(s.network), network); err != nil {
		t.Fatal(err)
	}
	network.Spec.DNS.Mode = networkingv1alpha.NetworkDNSModeDisabled
	if err := s.cl.Update(s.ctx, network); err != nil {
		t.Fatal(err)
	}
	s.reconcile()
	var claims ipamv1alpha1.IPClaimList
	if err := s.ipam.List(s.ctx, &claims); err != nil {
		t.Fatal(err)
	}
	if len(claims.Items) != 0 {
		t.Fatalf("disabling DNS kept %d address claims", len(claims.Items))
	}
}

func TestInternalDNSRefusesAClaimBoundToAnotherAddress(t *testing.T) {
	s := newIPAMDNSScenario(t, false)
	s.reconcile()
	dnsContext := s.readyContext()
	s.reconcile()
	s.allocate(serviceClaimKey(), "fd70:100::1")
	if _, err := s.r.reconcile(s.ctx, s.cl, testProject, client.ObjectKeyFromObject(s.nc)); err == nil {
		t.Fatal("a claim bound to an address other than the adopted one was accepted")
	}
	if s.binding(dnsContext) != nil {
		t.Fatal("access was written with an address the network does not use")
	}
	verify := claimedInternalDNSDestination(s.cl, s.r.IPAM, "fd70:100::/64")
	if _, err := verify(s.ctx, s.network, dnsContext); err == nil || errors.Is(err, errInternalDNSIntentUnverified) {
		t.Fatalf("a mismatched claim must hold intent, not withdraw it: %v", err)
	}
}

func TestInternalDNSRegionChangeKeepsTheServiceAddress(t *testing.T) {
	s := newIPAMDNSScenario(t, true)
	s.reconcile()
	s.readyContext()
	s.reconcile()
	s.allocate(serviceClaimKey(), "fd70:100::99")
	s.reconcile()
	s.allocate(resolverClaimKey("us-central"), "fd54::7")
	s.reconcile()
	location := &locationsv1alpha1.Location{}
	if err := s.cl.Get(s.ctx, client.ObjectKey{Name: "ord-1"}, location); err != nil {
		t.Fatal(err)
	}
	location.Spec.Topology[internalDNSRegionTopologyKey] = "us-east"
	if err := s.cl.Update(s.ctx, location); err != nil {
		t.Fatal(err)
	}
	s.reconcile()
	if service := s.claim(serviceClaimKey()); service == nil {
		t.Fatal("moving region released the network's service address")
	}
	if s.claim(resolverClaimKey("us-central")) != nil {
		t.Fatal("the old region's resolver address was kept")
	}
}

func TestInternalDNSDeletionBeforeIntentReleasesClaims(t *testing.T) {
	s := newIPAMDNSScenario(t, true)
	s.reconcile()
	s.readyContext()
	s.reconcile()
	if s.claim(serviceClaimKey()) == nil {
		t.Fatal("no claim was made")
	}
	current := &networkingv1alpha.NetworkContext{}
	if err := s.cl.Get(s.ctx, client.ObjectKeyFromObject(s.nc), current); err != nil {
		t.Fatal(err)
	}
	if current.Annotations[InternalDNSContextNameAnnotation] != "" {
		t.Fatal("intent was published before the claim was bound")
	}
	if err := s.cl.Delete(s.ctx, current); err != nil {
		t.Fatal(err)
	}
	s.reconcile()
	if s.claim(serviceClaimKey()) != nil {
		t.Fatal("a context deleted before its intent was published left its claim")
	}
}

func TestInternalDNSFreshAllocationCanBeTurnedOff(t *testing.T) {
	s := newIPAMDNSScenario(t, true)
	s.reconcile()
	dnsContext := s.readyContext()
	s.reconcile()
	s.allocate(serviceClaimKey(), "fd70:100::99")
	s.reconcile()
	s.allocate(resolverClaimKey("us-central"), "fd54::7")
	s.reconcile()
	s.r.Config.IPAM.FreshAllocation = false
	s.reconcile()
	destination, _, _ := unstructured.NestedString(s.binding(dnsContext).Object, "spec", "destinationAddress")
	if destination != "fd70:100::99" {
		t.Fatalf("turning fresh allocation off moved a network: %s", destination)
	}
}

func TestInternalDNSFreshResolverAddressesMoveAnExistingNetwork(t *testing.T) {
	s := newIPAMDNSScenario(t, false)
	s.reconcile()
	dnsContext := s.readyContext()
	derived, err := internalDNSDestination("fd70:100::/64", dnsContext.GetUID())
	if err != nil {
		t.Fatal(err)
	}
	s.reconcile()
	s.allocate(serviceClaimKey(), derived)
	s.reconcile()
	binding := s.binding(dnsContext)
	_ = unstructured.SetNestedField(binding.Object, "fd20:0:e::2:0:5", "status", "regionalAddress")
	if err := s.cl.Update(s.ctx, binding); err != nil {
		t.Fatal(err)
	}
	s.r.Config.IPAM.FreshResolverAddresses = true
	s.reconcile()
	resolver := s.claim(resolverClaimKey("us-central"))
	if resolver == nil || resolver.Spec.Address != "" {
		t.Fatalf("an existing network adopted its old regional address: %#v", resolver)
	}
	s.allocate(resolverClaimKey("us-central"), "fd70:202::1:0:9")
	s.reconcile()
	binding = s.binding(dnsContext)
	destination, _, _ := unstructured.NestedString(binding.Object, "spec", "destinationAddress")
	if got := accessRegionalAddress(binding, "spec"); got != "fd70:202::1:0:9" || destination != derived {
		t.Fatalf("the network was not moved to its new regional address with the same destination: %q %q", got, destination)
	}
}

func TestInternalDNSFreshResolverAddressesReplaceAStuckAdoption(t *testing.T) {
	s := newIPAMDNSScenario(t, false)
	s.reconcile()
	dnsContext := s.readyContext()
	derived, err := internalDNSDestination("fd70:100::/64", dnsContext.GetUID())
	if err != nil {
		t.Fatal(err)
	}
	s.reconcile()
	s.allocate(serviceClaimKey(), derived)
	s.reconcile()
	binding := s.binding(dnsContext)
	_ = unstructured.SetNestedField(binding.Object, "fd20:0:e::2:0:5", "status", "regionalAddress")
	if err := s.cl.Update(s.ctx, binding); err != nil {
		t.Fatal(err)
	}
	// The adoption claim asks for the old address, which the new pool can't
	// give, so it never binds.
	s.reconcile()
	stuck := s.claim(resolverClaimKey("us-central"))
	if stuck == nil || stuck.Spec.Address != "fd20:0:e::2:0:5" {
		t.Fatalf("no adoption claim was made: %#v", stuck)
	}
	s.r.Config.IPAM.FreshResolverAddresses = true
	s.reconcile()
	fresh := s.claim(resolverClaimKey("us-central"))
	if fresh == nil || fresh.Name == stuck.Name || fresh.Spec.Address != "" {
		t.Fatalf("the stuck adoption claim was not replaced by one IPAM picks: %#v", fresh)
	}
	s.allocate(resolverClaimKey("us-central"), "fd70:202::1:0:9")
	s.reconcile()
	if got := accessRegionalAddress(s.binding(dnsContext), "spec"); got != "fd70:202::1:0:9" {
		t.Fatalf("the network did not move to its new regional address: %q", got)
	}
}
