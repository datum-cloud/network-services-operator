// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"
)

// ---------------------------------------------------------------------------
// This file pins the test-coverage gap called out in
// https://github.com/datum-cloud/network-services-operator/issues/283:
// garbageCollectDNSRecordSets and cleanupDNSRecordSets were only ever
// exercised in isolation, never through the reconcile-level round trip that
// actually reaps a stale hostname's DNS record in production. Each test below
// documents the #283 behavior it pins and its expected pass/fail status on
// origin/main.
// ---------------------------------------------------------------------------

// TestEnsureDNSRecordSets_HostnameRemovalReapsRecord pins #283 scenario 1:
// the reconcile-level round trip. A hostname that disappears from a
// Gateway's claimed hostnames between two ensureDNSRecordSets calls
// (mirroring two passes of the gateway controller's Reconcile loop) must
// have its DNSRecordSet garbage collected, while a hostname that remains
// claimed keeps its record. Covers both CNAME (non-apex) and ALIAS (apex)
// record types.
//
// Expected GREEN on origin/main: garbageCollectDNSRecordSets already runs at
// the tail of every ensureDNSRecordSets call (gateway_dns_controller.go:382),
// scoped to the hostnames passed into that same call, so the reap already
// works at this level. If this test goes red, the create/GC pairing inside
// ensureDNSRecordSets is broken. If it stays green, the real #283 gap is
// upstream of this function -- in how claimedHostnames survives (or fails to
// drop a removed hostname) across reconciles, which this test does not
// exercise (see ensureHostnamesClaimed in gateway_controller.go).
func TestEnsureDNSRecordSets_HostnameRemovalReapsRecord(t *testing.T) {
	const ns = "test-ns"
	ctx := log.IntoContext(context.Background(), zap.New())

	testConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			TargetDomain:         "gateways.test.local",
			EnableDNSIntegration: true,
		},
	}

	tests := []struct {
		name              string
		removedHostname   string
		keptHostname      string
		upstreamObjects   []client.Object
		wantRemovedRRType dnsv1alpha1.RRType
	}{
		{
			name:            "non-apex CNAME hostname removed",
			removedHostname: "remove.example.com",
			keptHostname:    "keep.example.com",
			upstreamObjects: []client.Object{
				newVerifiedDNSZoneDomain(ns, "example.com", false),
				newDNSZone(ns, "example-com", "example.com"),
			},
			wantRemovedRRType: dnsv1alpha1.RRTypeCNAME,
		},
		{
			name:            "apex ALIAS hostname removed, sibling CNAME survives",
			removedHostname: "example.com",
			keptHostname:    "keep.example.net",
			upstreamObjects: []client.Object{
				newVerifiedDNSZoneDomain(ns, "example.com", true),
				newDNSZone(ns, "example-com", "example.com"),
				newVerifiedDNSZoneDomain(ns, "example.net", false),
				newDNSZone(ns, "example-net", "example.net"),
			},
			wantRemovedRRType: dnsv1alpha1.RRTypeALIAS,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newDNSTestScheme(t)
			gw := newTestGatewayForDNS(ns, "roundtrip-gw")

			allObjects := append([]client.Object{gw}, tt.upstreamObjects...)
			for _, obj := range allObjects {
				if obj.GetUID() == "" {
					obj.SetUID(uuid.NewUUID())
				}
				if obj.GetCreationTimestamp().Time.IsZero() {
					obj.SetCreationTimestamp(metav1.Now())
				}
			}

			cl := buildFakeUpstreamClientForDNS(s, allObjects...)
			reconciler := newDNSReconciler(testConfig)

			removedRSName := dnsRecordSetName(gw.Name, tt.removedHostname)
			keptRSName := dnsRecordSetName(gw.Name, tt.keptHostname)

			// First reconcile pass: both hostnames are claimed.
			statuses, result := reconciler.ensureDNSRecordSets(ctx, cl, gw, []string{tt.removedHostname, tt.keptHostname})
			require.NoError(t, result.Err)
			require.Len(t, statuses, 2)

			var removedRS dnsv1alpha1.DNSRecordSet
			require.NoError(t, cl.Get(ctx, client.ObjectKey{Namespace: ns, Name: removedRSName}, &removedRS),
				"record for %q should exist after the first pass", tt.removedHostname)
			assert.Equal(t, tt.wantRemovedRRType, removedRS.Spec.RecordType)

			var keptRS dnsv1alpha1.DNSRecordSet
			require.NoError(t, cl.Get(ctx, client.ObjectKey{Namespace: ns, Name: keptRSName}, &keptRS),
				"record for %q should exist after the first pass", tt.keptHostname)

			// Second reconcile pass: the hostname was removed from the Gateway.
			_, result = reconciler.ensureDNSRecordSets(ctx, cl, gw, []string{tt.keptHostname})
			require.NoError(t, result.Err)

			err := cl.Get(ctx, client.ObjectKey{Namespace: ns, Name: removedRSName}, &dnsv1alpha1.DNSRecordSet{})
			assert.True(t, apierrors.IsNotFound(err),
				"DNSRecordSet for removed hostname %q should have been reaped (#283); got err=%v", tt.removedHostname, err)

			require.NoError(t, cl.Get(ctx, client.ObjectKey{Namespace: ns, Name: keptRSName}, &keptRS),
				"record for still-claimed hostname %q must survive the reap", tt.keptHostname)
		})
	}
}

// TestCleanupDNSRecordSets_OnGatewayDeletion pins #283 scenario 2: deleting a
// Gateway must reap every DNSRecordSet it owns via cleanupDNSRecordSets, the
// function finalizeGateway invokes during finalization
// (gateway_controller.go:1598). Also asserts scope: a DNSRecordSet belonging
// to a different Gateway must survive.
//
// Expected GREEN on origin/main: cleanupDNSRecordSets unconditionally lists
// and deletes by the (managed, managed-by, source-kind=Gateway, source-name,
// source-namespace) label set -- no bug is visible at this layer, matching
// the issue's note that the HTTPProxy/Gateway-delete cleanup path has been
// fixed since v0.17.0.
func TestCleanupDNSRecordSets_OnGatewayDeletion(t *testing.T) {
	const ns = "test-ns"
	ctx := log.IntoContext(context.Background(), zap.New())
	s := newDNSTestScheme(t)

	testConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			TargetDomain:         "gateways.test.local",
			EnableDNSIntegration: true,
		},
	}

	gw := newTestGatewayForDNS(ns, "deleted-gw")
	now := metav1.Now()
	gw.DeletionTimestamp = &now
	gw.Finalizers = []string{"networking.datumapis.com/gateway-cleanup"}

	otherGW := newTestGatewayForDNS(ns, "other-gw")

	makeRS := func(name, sourceGWName, hostname string) *dnsv1alpha1.DNSRecordSet {
		return &dnsv1alpha1.DNSRecordSet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: ns,
				Name:      name,
				UID:       uuid.NewUUID(),
				Labels: map[string]string{
					labelManagedBy:     labelManagedByValue,
					labelDNSManaged:    labelValueTrue,
					labelDNSSourceKind: KindGateway,
					labelDNSSourceName: sourceGWName,
					labelDNSSourceNS:   ns,
				},
				Annotations: map[string]string{
					annotationDNSHostname: hostname,
				},
			},
			Spec: dnsv1alpha1.DNSRecordSetSpec{
				DNSZoneRef: corev1.LocalObjectReference{Name: "example-com"},
				RecordType: dnsv1alpha1.RRTypeCNAME,
				Records:    []dnsv1alpha1.RecordEntry{{Name: hostname + "."}},
			},
		}
	}

	deletedGWRecordA := makeRS(dnsRecordSetName(gw.Name, "a.example.com"), gw.Name, "a.example.com")
	deletedGWRecordB := makeRS(dnsRecordSetName(gw.Name, "b.example.com"), gw.Name, "b.example.com")
	survivingRecord := makeRS(dnsRecordSetName(otherGW.Name, "c.example.com"), otherGW.Name, "c.example.com")

	allObjects := []client.Object{gw, otherGW, deletedGWRecordA, deletedGWRecordB, survivingRecord}
	for _, obj := range allObjects {
		if obj.GetUID() == "" {
			obj.SetUID(uuid.NewUUID())
		}
		if obj.GetCreationTimestamp().Time.IsZero() {
			obj.SetCreationTimestamp(metav1.Now())
		}
	}

	cl := buildFakeUpstreamClientForDNS(s, allObjects...)
	reconciler := newDNSReconciler(testConfig)

	result := reconciler.cleanupDNSRecordSets(ctx, cl, gw)
	require.NoError(t, result.Err)

	var remaining dnsv1alpha1.DNSRecordSetList
	require.NoError(t, cl.List(ctx, &remaining, client.InNamespace(ns)))

	remainingNames := make([]string, 0, len(remaining.Items))
	for _, rs := range remaining.Items {
		remainingNames = append(remainingNames, rs.Name)
	}
	assert.NotContains(t, remainingNames, deletedGWRecordA.Name, "record for deleted gateway should be reaped (#283)")
	assert.NotContains(t, remainingNames, deletedGWRecordB.Name, "record for deleted gateway should be reaped (#283)")
	assert.Contains(t, remainingNames, survivingRecord.Name, "record belonging to a different gateway must not be touched")
}

// TestHTTPProxyDeletionCascadesDNSRecordCleanup pins #283 scenario 3: an
// HTTPProxy delete must eventually reap the DNS records created for its
// child Gateway. In production this happens because the HTTPProxy
// controller sets itself as the Gateway's Controller owner reference
// (httpproxy_controller.go, collectDesiredResources/SetControllerReference),
// native Kubernetes garbage collection then deletes the Gateway once the
// HTTPProxy is gone, and Gateway deletion runs finalizeGateway ->
// cleanupDNSRecordSets.
//
// The fake client used here has no garbage-collector loop, so this test:
//  1. Drives the real HTTPProxy reconcile to prove the owner-reference
//     wiring that native GC depends on is actually correct.
//  2. Simulates the GC cascade by deleting the owned Gateway directly --
//     what real Kubernetes garbage collection guarantees given the owner
//     reference asserted in step 1.
//  3. Calls cleanupDNSRecordSets, the same function the real Gateway
//     finalizer invokes, to assert the DNS records are reaped.
//
// Expected GREEN on origin/main: both the owner-reference wiring and
// cleanupDNSRecordSets are individually correct, so this simulated cascade
// succeeds. This does NOT prove the real end-to-end cascade is bug-free --
// only a chainsaw/e2e test exercising real garbage collection can prove
// that. See #283's "no chainsaw e2e references DNSRecordSet at all" gap.
func TestHTTPProxyDeletionCascadesDNSRecordCleanup(t *testing.T) {
	const ns = "test"
	ctx := log.IntoContext(context.Background(), zap.New())

	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, gatewayv1.Install(testScheme))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(testScheme))
	require.NoError(t, discoveryv1.AddToScheme(testScheme))
	require.NoError(t, networkingv1alpha.AddToScheme(testScheme))
	require.NoError(t, networkingv1alpha1.AddToScheme(testScheme))
	require.NoError(t, dnsv1alpha1.AddToScheme(testScheme))

	testConfig := config.NetworkServicesOperator{
		HTTPProxy: config.HTTPProxyConfig{
			GatewayClassName: "test-gateway-class",
		},
		Gateway: config.GatewayConfig{
			ControllerName:             gatewayv1.GatewayController("test-gateway-class"),
			DownstreamGatewayClassName: "test-downstream-gateway-class",
			TargetDomain:               "gateways.test.local",
			EnableDNSIntegration:       true,
		},
	}

	httpProxy := newHTTPProxy()
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns, UID: uuid.NewUUID()}}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(httpProxy, namespace).
		WithStatusSubresource(httpProxy, &gatewayv1.Gateway{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				obj.SetUID(uuid.NewUUID())
				obj.SetCreationTimestamp(metav1.Now())
				return cl.Create(ctx, obj, opts...)
			},
		}).
		Build()

	httpProxyReconciler := &HTTPProxyReconciler{
		mgr:    &fakeMockManager{cl: fakeClient},
		Config: testConfig,
	}

	req := mcreconcile.Request{
		Request: reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(httpProxy),
		},
		ClusterName: "test-cluster",
	}

	// Pass 1 adds the HTTPProxy finalizer; pass 2 creates the owned Gateway.
	_, err := httpProxyReconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	_, err = httpProxyReconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	var gateway gatewayv1.Gateway
	require.NoError(t, fakeClient.Get(ctx, client.ObjectKeyFromObject(httpProxy), &gateway))

	ownerRef := metav1.GetControllerOf(&gateway)
	require.NotNil(t, ownerRef, "Gateway must have a controller owner reference for native GC to cascade-delete it")
	assert.Equal(t, httpProxy.Name, ownerRef.Name)
	assert.Equal(t, "HTTPProxy", ownerRef.Kind)

	// Seed a DNS record as if a prior GatewayReconciler pass had already run
	// ensureDNSRecordSets for this Gateway.
	rs := &dnsv1alpha1.DNSRecordSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      dnsRecordSetName(gateway.Name, "api.example.com"),
			UID:       uuid.NewUUID(),
			Labels: map[string]string{
				labelManagedBy:     labelManagedByValue,
				labelDNSManaged:    labelValueTrue,
				labelDNSSourceKind: KindGateway,
				labelDNSSourceName: gateway.Name,
				labelDNSSourceNS:   ns,
			},
			Annotations: map[string]string{
				annotationDNSHostname: "api.example.com",
			},
		},
		Spec: dnsv1alpha1.DNSRecordSetSpec{
			DNSZoneRef: corev1.LocalObjectReference{Name: "example-com"},
			RecordType: dnsv1alpha1.RRTypeCNAME,
			Records:    []dnsv1alpha1.RecordEntry{{Name: "api.example.com."}},
		},
	}
	require.NoError(t, fakeClient.Create(ctx, rs))

	// Delete the HTTPProxy. The fake client runs no garbage-collector, so
	// explicitly delete the owned Gateway to simulate the cascade the owner
	// reference asserted above guarantees in a real cluster.
	require.NoError(t, fakeClient.Delete(ctx, httpProxy))
	require.NoError(t, fakeClient.Delete(ctx, &gateway))

	gatewayReconciler := &GatewayReconciler{Config: testConfig}
	cleanupResult := gatewayReconciler.cleanupDNSRecordSets(ctx, fakeClient, &gateway)
	require.NoError(t, cleanupResult.Err)

	var remaining dnsv1alpha1.DNSRecordSetList
	require.NoError(t, fakeClient.List(ctx, &remaining, client.InNamespace(ns)))
	assert.Empty(t, remaining.Items,
		"DNS records owned by the HTTPProxy's Gateway should be reaped once the cascade reaches cleanupDNSRecordSets (#283)")
}
