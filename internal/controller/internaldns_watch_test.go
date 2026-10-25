// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// TestInternalDNSObjectReachesItsNetworkContexts checks that a resolver
// context or access binding change re-reconciles the network contexts of the
// network that owns it, and only those.
func TestInternalDNSObjectReachesItsNetworkContexts(t *testing.T) {
	t.Parallel()
	scheme := runtime.NewScheme()
	if err := networkingv1alpha.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	first := testNetworkContext("first", "context-1")
	second := testNetworkContext("second", "context-2")
	other := testNetworkContext("other", "context-3")
	other.Labels[networkingv1alpha.NetworkUIDLabel] = "other-network-uid"
	other.Spec.Network.Name = "green"
	elsewhere := testNetworkContext("first", "context-4")
	elsewhere.Namespace = "other-tenant"
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(first, second, other, elsewhere).Build()

	for _, obj := range []*unstructured.Unstructured{
		newInternalDNSObject(internalDNSContextGVK, "tenant", "dns-blue"),
		newInternalDNSObject(internalDNSAccessGVK, "tenant", "dns-blue-us-central"),
	} {
		obj.SetAnnotations(map[string]string{internalDNSOwnerNetworkUIDAnnotation: "network-uid"})
		requests := internalDNSNetworkContextRequests(context.Background(), cl, "project-a", obj)
		got := map[string]bool{}
		for _, request := range requests {
			if request.ClusterName != "project-a" {
				t.Fatalf("request went to cluster %q", request.ClusterName)
			}
			got[request.NamespacedName.String()] = true
		}
		if len(got) != 2 || !got["tenant/first"] || !got["tenant/second"] {
			t.Fatalf("%s reached %v, want tenant/first and tenant/second", obj.GetKind(), got)
		}
	}

	unowned := newInternalDNSObject(internalDNSContextGVK, "tenant", "dns-blue")
	if requests := internalDNSNetworkContextRequests(context.Background(), cl, "project-a", unowned); len(requests) != 0 {
		t.Fatalf("an object without an owning network reached %d network contexts", len(requests))
	}
}

// TestInternalDNSObjectChangedPredicate checks that the watch passes the
// status changes the project reconciler waits on and drops the rest.
func TestInternalDNSObjectChangedPredicate(t *testing.T) {
	t.Parallel()
	base := func() *unstructured.Unstructured {
		obj := newInternalDNSObject(internalDNSAccessGVK, "tenant", "dns-blue-us-central")
		obj.SetUID(types.UID("binding-uid"))
		obj.SetGeneration(1)
		obj.SetAnnotations(map[string]string{internalDNSOwnerNetworkUIDAnnotation: "network-uid"})
		obj.Object["status"] = map[string]any{
			"observedGeneration": int64(1),
			"conditions": []any{map[string]any{
				"type": "Accepted", "status": "True", "observedGeneration": int64(1),
			}},
		}
		return obj
	}
	pred := internalDNSObjectChangedPredicate()

	cases := map[string]struct {
		mutate func(*unstructured.Unstructured)
		want   bool
	}{
		"unrelated status write": {mutate: func(obj *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(obj.Object, "2026-10-24T00:00:00Z", "status", "lastPublished")
		}, want: false},
		"becomes ready": {mutate: func(obj *unstructured.Unstructured) {
			conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
			conditions = append(conditions, map[string]any{
				"type": "Ready", "status": "True", "observedGeneration": int64(1),
			})
			_ = unstructured.SetNestedSlice(obj.Object, conditions, "status", "conditions")
		}, want: true},
		"observes a new generation": {mutate: func(obj *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(obj.Object, int64(2), "status", "observedGeneration")
		}, want: true},
		"spec changes": {mutate: func(obj *unstructured.Unstructured) {
			obj.SetGeneration(2)
		}, want: true},
		"managed namespace published": {mutate: func(obj *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(obj.Object, internalDNSDefaultDomain, "status", "managedNamespace", "suffix")
			_ = unstructured.SetNestedField(obj.Object, "zone", "status", "managedNamespace", "dnsZoneRef", "name")
		}, want: true},
		"owner network changes": {mutate: func(obj *unstructured.Unstructured) {
			obj.SetAnnotations(map[string]string{internalDNSOwnerNetworkUIDAnnotation: "other-network-uid"})
		}, want: true},
		"recreated": {mutate: func(obj *unstructured.Unstructured) {
			obj.SetUID(types.UID("new-binding-uid"))
		}, want: true},
		"starts deleting": {mutate: func(obj *unstructured.Unstructured) {
			now := metav1.Now()
			obj.SetDeletionTimestamp(&now)
		}, want: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			oldObj, newObj := base(), base()
			tc.mutate(newObj)
			if got := pred.Update(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}); got != tc.want {
				t.Fatalf("update passed = %v, want %v", got, tc.want)
			}
		})
	}
	if !pred.Create(event.CreateEvent{Object: base()}) || !pred.Delete(event.DeleteEvent{Object: base()}) {
		t.Fatal("creation and deletion must pass")
	}
}
