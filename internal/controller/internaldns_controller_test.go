// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

type fixedProjectIdentity types.UID

func (f fixedProjectIdentity) ProjectUID(context.Context, string) (types.UID, error) {
	return types.UID(f), nil
}

func TestInternalDNSProjectLifecycleFencesAccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	scheme := runtime.NewScheme()
	if err := networkingv1alpha.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := locationsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	scheme.AddKnownTypeWithName(internalDNSContextGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(internalDNSContextGVK.GroupVersion().WithKind("DNSResolverContextList"), &unstructured.UnstructuredList{})
	scheme.AddKnownTypeWithName(internalDNSAccessGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(internalDNSAccessGVK.GroupVersion().WithKind("DNSResolverAccessBindingList"), &unstructured.UnstructuredList{})

	network := &networkingv1alpha.Network{
		ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "blue", UID: "network-uid"},
	}
	location := &locationsv1alpha1.Location{
		ObjectMeta: metav1.ObjectMeta{Name: "ord-1"},
		Spec:       locationsv1alpha1.LocationSpec{Topology: map[string]string{internalDNSRegionTopologyKey: "us-central"}},
	}
	first := testNetworkContext("first", "context-1")
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(network, location, first).Build()
	r := &InternalDNSProjectReconciler{
		Config: config.InternalDNSConfig{
			Frontend: "fd53::53", ServicePrefix: "fd70:100::/64",
		},
		Projects: fixedProjectIdentity("project-uid"),
		Now:      func() time.Time { return now },
	}

	if _, err := r.reconcile(ctx, cl, "project", client.ObjectKeyFromObject(first)); err != nil {
		t.Fatal(err)
	}
	dnsContext := newInternalDNSObject(internalDNSContextGVK, "tenant", internalDNSResourceName("blue", network.UID))
	if err := cl.Get(ctx, client.ObjectKeyFromObject(dnsContext), dnsContext); err != nil {
		t.Fatal(err)
	}
	domain, _, _ := unstructured.NestedString(dnsContext.Object, "spec", "managedNamespace", "domainSuffix")
	if domain != internalDNSDefaultDomain {
		t.Fatalf("network default domain = %q", domain)
	}
	dnsContext.SetUID("dns-context-uid")
	dnsContext.SetGeneration(1)
	if err := unstructured.SetNestedSlice(dnsContext.Object, []any{map[string]any{"type": "Ready", "status": "True", "observedGeneration": int64(1)}}, "status", "conditions"); err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedField(dnsContext.Object, internalDNSDefaultDomain, "status", "managedNamespace", "suffix"); err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedMap(dnsContext.Object, map[string]any{
		"name": "blue-internal",
	}, "status", "managedNamespace", "dnsZoneRef"); err != nil {
		t.Fatal(err)
	}
	if err := cl.Update(ctx, dnsContext); err != nil {
		t.Fatal(err)
	}

	if _, err := r.reconcile(ctx, cl, "project", client.ObjectKeyFromObject(first)); err != nil {
		t.Fatal(err)
	}
	bindingName := internalDNSRegionalName(dnsContext.GetName(), "us-central")
	binding := newInternalDNSObject(internalDNSAccessGVK, "tenant", bindingName)
	if err := cl.Get(ctx, client.ObjectKeyFromObject(binding), binding); err != nil {
		t.Fatal(err)
	}
	binding.SetUID("access-uid")
	binding.SetGeneration(1)
	if err := unstructured.SetNestedField(binding.Object, int64(1), "status", "observedGeneration"); err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedField(binding.Object, "dns-binding-uid", "status", "bindingRef", "uid"); err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedSlice(binding.Object, []any{map[string]any{
		"type": "Ready", "status": "True", "reason": "Ready", "observedGeneration": int64(1),
	}}, "status", "conditions"); err != nil {
		t.Fatal(err)
	}
	if err := cl.Update(ctx, binding); err != nil {
		t.Fatal(err)
	}

	if _, err := r.reconcile(ctx, cl, "project", client.ObjectKeyFromObject(first)); err != nil {
		t.Fatal(err)
	}
	assertDNSIntent(t, ctx, cl, "first", bindingName)
	originalStatus, _, _ := unstructured.NestedMap(dnsContext.Object, "status")
	for _, invalid := range []struct {
		path  []string
		value any
	}{
		{path: []string{"status", "managedNamespace", "suffix"}, value: "different.internal"},
		{path: []string{"status", "conditions"}, value: []any{map[string]any{"type": "Ready", "status": "True", "observedGeneration": int64(0)}}},
	} {
		if err := unstructured.SetNestedField(dnsContext.Object, invalid.value, invalid.path...); err != nil {
			t.Fatal(err)
		}
		if err := cl.Update(ctx, dnsContext); err != nil {
			t.Fatal(err)
		}
		if _, err := r.reconcile(ctx, cl, "project", client.ObjectKeyFromObject(first)); err != nil {
			t.Fatal(err)
		}
		current := &networkingv1alpha.NetworkContext{}
		if err := cl.Get(ctx, client.ObjectKeyFromObject(first), current); err != nil {
			t.Fatal(err)
		}
		if current.Annotations[InternalDNSSuffixAnnotation] != "datum.internal" {
			t.Fatalf("unavailable allocation observation withdrew DNS intent: %#v", current.Annotations)
		}
		if err := unstructured.SetNestedMap(dnsContext.Object, originalStatus, "status"); err != nil {
			t.Fatal(err)
		}
		if err := cl.Update(ctx, dnsContext); err != nil {
			t.Fatal(err)
		}
		if _, err := r.reconcile(ctx, cl, "project", client.ObjectKeyFromObject(first)); err != nil {
			t.Fatal(err)
		}
		assertDNSIntent(t, ctx, cl, "first", bindingName)
	}

	second := testNetworkContext("second", "context-2")
	if err := cl.Create(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err := r.reconcile(ctx, cl, "project", client.ObjectKeyFromObject(second)); err != nil {
		t.Fatal(err)
	}
	assertDNSIntent(t, ctx, cl, "second", bindingName)
	assertUnstructuredCount(t, ctx, cl, internalDNSContextGVK, "tenant", 1)
	assertUnstructuredCount(t, ctx, cl, internalDNSAccessGVK, "tenant", 1)
	firstCurrent := &networkingv1alpha.NetworkContext{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: "tenant", Name: "first"}, firstCurrent); err != nil {
		t.Fatal(err)
	}
	if err := r.cleanupRegion(ctx, cl, firstCurrent); err != nil {
		t.Fatal(err)
	}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: "tenant", Name: bindingName}, binding); err != nil {
		t.Fatalf("regional access was removed while another context remained: %v", err)
	}

	now = now.Add(3 * time.Minute)
	if _, err := r.reconcile(ctx, cl, "project", client.ObjectKeyFromObject(first)); err != nil {
		t.Fatal(err)
	}
	current := &networkingv1alpha.NetworkContext{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: "tenant", Name: "first"}, current); err != nil {
		t.Fatal(err)
	}
	if current.Annotations[InternalDNSAccessNameAnnotation] != bindingName {
		t.Fatalf("persistent access was withdrawn: %#v", current.Annotations)
	}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: "tenant", Name: bindingName}, binding); err != nil {
		t.Fatal(err)
	}
	if !bindingReady(binding) {
		t.Fatal("unchanged access lost readiness")
	}
	secondCurrent := &networkingv1alpha.NetworkContext{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: "tenant", Name: "second"}, secondCurrent); err != nil {
		t.Fatal(err)
	}
	if err := cl.Delete(ctx, firstCurrent); err != nil {
		t.Fatal(err)
	}
	if err := r.cleanupRegion(ctx, cl, secondCurrent); err != nil {
		t.Fatal(err)
	}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: "tenant", Name: bindingName}, binding); !apierrors.IsNotFound(err) {
		t.Fatalf("regional access survived the last context: %v", err)
	}
}

func TestInternalDNSReadinessPreservesAppliedSettings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	scheme := runtime.NewScheme()
	if err := networkingv1alpha.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	projectContext := testNetworkContext("context", "project-context-uid")
	projectContext.Annotations = testDNSIntent(now.Add(time.Minute))
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&networkingv1alpha.NetworkContext{}).
		WithObjects(projectContext).Build()
	hubContext := projectContext.DeepCopy()
	hubContext.Status.DNSAccess = &networkingv1alpha.NetworkDNSAccessStatus{
		ContextRef:         networkingv1alpha.NetworkDNSObjectReference{Name: "resolver"},
		AccessBindingRef:   networkingv1alpha.NetworkDNSObjectReference{Name: "access"},
		ServiceDestination: projectContext.Annotations[InternalDNSServiceDestinationAnnotation],
	}
	hubContext.Status.Conditions = []metav1.Condition{{
		Type: networkingv1alpha.NetworkContextDNSReady, Status: metav1.ConditionTrue,
		Reason: networkingv1alpha.NetworkContextDNSReadyReasonReady,
	}}
	r := &InternalDNSReadinessReconciler{
		Config: config.InternalDNSConfig{Frontend: "fd53::53"},
		Now:    func() time.Time { return now },
	}

	if _, err := r.publish(ctx, cl, projectContext, hubContext); err != nil {
		t.Fatal(err)
	}
	current := &networkingv1alpha.NetworkContext{}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(projectContext), current); err != nil {
		t.Fatal(err)
	}
	if current.Spec.DNS == nil || current.Spec.DNS.AccessBindingRef.Name != "access" {
		t.Fatalf("verified settings were not published: %#v", current.Spec.DNS)
	}

	hubContext.Status.DNSAccess.ServiceDestination = "fd70:100::11"
	if _, err := r.publish(ctx, cl, current, hubContext); err != nil {
		t.Fatal(err)
	}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(projectContext), current); err != nil {
		t.Fatal(err)
	}
	if current.Spec.DNS == nil {
		t.Fatal("observation mismatch withdrew applied resolver settings")
	}

	hubContext.Status.DNSAccess.ServiceDestination = projectContext.Annotations[InternalDNSServiceDestinationAnnotation]
	hubContext.Status.Conditions[0].Status = metav1.ConditionUnknown
	r.Now = func() time.Time { return now.Add(2 * time.Minute) }
	if _, err := r.publish(ctx, cl, current, hubContext); err != nil {
		t.Fatal(err)
	}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(projectContext), current); err != nil {
		t.Fatal(err)
	}
	if current.Spec.DNS == nil {
		t.Fatal("resolver settings expired")
	}
	if condition := apimeta.FindStatusCondition(current.Status.Conditions, networkingv1alpha.NetworkContextDNSReady); condition == nil || condition.Status != metav1.ConditionUnknown {
		t.Fatalf("unavailable observation was not reported: %#v", current.Status.Conditions)
	}
}

func TestRecreatedContextReplacesAccessBinding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(internalDNSAccessGVK, &unstructured.Unstructured{})
	network := &networkingv1alpha.Network{ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "blue", UID: "network-uid"}}
	dnsContext := newInternalDNSObject(internalDNSContextGVK, "tenant", "resolver")
	dnsContext.SetUID("new-context")
	binding := newInternalDNSObject(internalDNSAccessGVK, "tenant", internalDNSRegionalName("resolver", "us-central"))
	binding.SetUID("access-uid")
	binding.SetAnnotations(map[string]string{internalDNSOwnerNetworkUIDAnnotation: string(network.UID)})
	binding.Object["spec"] = map[string]any{
		"contextRef": map[string]any{"name": "resolver", "uid": "old-context"},
		"region":     "us-central", "destinationAddress": "fd70:100::10",
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(binding).Build()
	r := &InternalDNSProjectReconciler{}
	_, ready, err := r.ensureAccess(ctx, cl, network, "project-uid", dnsContext, "us-central", "fd70:100::11")
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("replaced access was reported ready")
	}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(binding), binding); !apierrors.IsNotFound(err) {
		t.Fatalf("retired binding survived: %v", err)
	}
	current, ready, err := r.ensureAccess(ctx, cl, network, "project-uid", dnsContext, "us-central", "fd70:100::11")
	if err != nil {
		t.Fatal(err)
	}
	if ready || !accessBindingMatches(current, dnsContext, "us-central", "fd70:100::11") {
		t.Fatal("new context did not receive a pending access binding")
	}
}

func TestAccessReadinessRequiresCurrentGeneration(t *testing.T) {
	binding := newInternalDNSObject(internalDNSAccessGVK, "tenant", "access")
	binding.SetGeneration(2)
	binding.Object["status"] = map[string]any{"observedGeneration": int64(1), "conditions": []any{map[string]any{"type": "Ready", "status": "True", "observedGeneration": int64(1)}}}
	if bindingReady(binding) {
		t.Fatal("stale observation was accepted")
	}
	if err := unstructured.SetNestedField(binding.Object, int64(2), "status", "observedGeneration"); err != nil {
		t.Fatal(err)
	}
	if bindingReady(binding) {
		t.Fatal("stale condition was accepted")
	}
	if err := unstructured.SetNestedSlice(binding.Object, []any{map[string]any{"type": "Ready", "status": "True", "observedGeneration": int64(2)}}, "status", "conditions"); err != nil {
		t.Fatal(err)
	}
	if !bindingReady(binding) {
		t.Fatal("current observation was rejected")
	}
}

func testNetworkContext(name string, uid types.UID) *networkingv1alpha.NetworkContext {
	return &networkingv1alpha.NetworkContext{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "tenant", Name: name, UID: uid,
			Labels: map[string]string{networkingv1alpha.NetworkUIDLabel: "network-uid"},
		},
		Spec: networkingv1alpha.NetworkContextSpec{
			Network:  networkingv1alpha.LocalNetworkRef{Name: "blue"},
			Location: locationsv1alpha1.LocationReference{Name: "ord-1"},
		},
	}
}

func testDNSIntent(_ time.Time) map[string]string {
	return map[string]string{
		InternalDNSContextNameAnnotation:        "resolver",
		InternalDNSAccessNameAnnotation:         "access",
		InternalDNSSuffixAnnotation:             "blue.internal",
		InternalDNSRegionAnnotation:             "us-central",
		InternalDNSServiceDestinationAnnotation: "fd70:100::10",
	}
}

func assertDNSIntent(t *testing.T, ctx context.Context, cl client.Client, name, accessName string) {
	t.Helper()
	current := &networkingv1alpha.NetworkContext{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: "tenant", Name: name}, current); err != nil {
		t.Fatal(err)
	}
	if current.Annotations[InternalDNSAccessNameAnnotation] != accessName {
		t.Fatalf("unexpected DNS intent: %#v", current.Annotations)
	}
}

func assertUnstructuredCount(
	t *testing.T,
	ctx context.Context,
	cl client.Client,
	gvk schema.GroupVersionKind,
	namespace string,
	want int,
) {
	t.Helper()
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
	if err := cl.List(ctx, list, client.InNamespace(namespace)); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != want {
		t.Fatalf("%s count = %d, want %d", gvk.Kind, len(list.Items), want)
	}
}
