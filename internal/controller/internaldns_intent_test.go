// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

const testDNSServicePrefix = "fd70:100::/64"

// testDNSDestination is the destination allocated to the resolver context
// acceptedDNSAccess returns.
var testDNSDestination = func() string {
	destination, err := internalDNSDestination(testDNSServicePrefix, "resolver-uid")
	if err != nil {
		panic(err)
	}
	return destination
}()

// verifiedDNSIntent is testDNSIntent naming the access acceptedDNSAccess
// returns.
func verifiedDNSIntent() map[string]string {
	intent := testDNSIntent(time.Time{})
	intent[InternalDNSServiceDestinationAnnotation] = testDNSDestination
	return intent
}

func internalDNSIntentScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := networkingv1alpha.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	scheme.AddKnownTypeWithName(internalDNSContextGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(internalDNSContextGVK.GroupVersion().WithKind("DNSResolverContextList"), &unstructured.UnstructuredList{})
	scheme.AddKnownTypeWithName(internalDNSAccessGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(internalDNSAccessGVK.GroupVersion().WithKind("DNSResolverAccessBindingList"), &unstructured.UnstructuredList{})
	return scheme
}

// acceptedDNSAccess returns a network, its resolver context and an access
// binding DNS accepted, matching testDNSIntent.
func acceptedDNSAccess(t *testing.T) (*networkingv1alpha.Network, *unstructured.Unstructured, *unstructured.Unstructured) {
	t.Helper()
	network := &networkingv1alpha.Network{
		ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "blue", UID: "network-uid"},
	}
	dnsContext := newInternalDNSObject(internalDNSContextGVK, "tenant", "resolver")
	dnsContext.SetUID("resolver-uid")
	dnsContext.SetAnnotations(map[string]string{internalDNSOwnerNetworkUIDAnnotation: "network-uid"})
	dnsContext.Object["spec"] = map[string]any{"consumerID": "project-uid/network-uid"}
	dnsContext.Object["status"] = map[string]any{"managedNamespace": map[string]any{"suffix": "blue.internal"}}

	binding := newInternalDNSObject(internalDNSAccessGVK, "tenant", "access")
	binding.SetGeneration(1)
	binding.SetAnnotations(map[string]string{internalDNSOwnerNetworkUIDAnnotation: "network-uid"})
	binding.Object["spec"] = map[string]any{
		"contextRef":         map[string]any{"name": "resolver", "uid": "resolver-uid"},
		"region":             "us-central",
		"destinationAddress": testDNSDestination,
	}
	binding.Object["status"] = map[string]any{"conditions": []any{
		map[string]any{"type": "Accepted", "status": "True", "observedGeneration": int64(1)},
	}}
	return network, dnsContext, binding
}

func TestVerifyInternalDNSIntent(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		annotations func(map[string]string)
		objects     func(*networkingv1alpha.Network, *unstructured.Unstructured, *unstructured.Unstructured)
		wantErr     bool
	}{
		"accepted access": {},
		"another destination": {
			annotations: func(a map[string]string) { a[InternalDNSServiceDestinationAnnotation] = "fd70:100::99" },
			wantErr:     true,
		},
		"another region": {
			annotations: func(a map[string]string) { a[InternalDNSRegionAnnotation] = "us-east" },
			wantErr:     true,
		},
		"another suffix": {
			annotations: func(a map[string]string) { a[InternalDNSSuffixAnnotation] = "red.internal" },
			wantErr:     true,
		},
		"destination DNS accepted but not allocated to the context": {
			annotations: func(a map[string]string) { a[InternalDNSServiceDestinationAnnotation] = "fd70:100::99" },
			objects: func(_ *networkingv1alpha.Network, _, b *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(b.Object, "fd70:100::99", "spec", "destinationAddress")
			},
			wantErr: true,
		},
		"binding being deleted": {
			objects: func(_ *networkingv1alpha.Network, _, b *unstructured.Unstructured) {
				now := metav1.Now()
				b.SetDeletionTimestamp(&now)
				b.SetFinalizers([]string{"example.test/hold"})
			},
			wantErr: true,
		},
		"missing binding": {
			annotations: func(a map[string]string) { a[InternalDNSAccessNameAnnotation] = "elsewhere" },
			wantErr:     true,
		},
		"binding of another network": {
			objects: func(_ *networkingv1alpha.Network, _, b *unstructured.Unstructured) {
				b.SetAnnotations(map[string]string{internalDNSOwnerNetworkUIDAnnotation: "other-network"})
			},
			wantErr: true,
		},
		"context of another network": {
			objects: func(_ *networkingv1alpha.Network, c, _ *unstructured.Unstructured) {
				c.SetAnnotations(map[string]string{internalDNSOwnerNetworkUIDAnnotation: "other-network"})
			},
			wantErr: true,
		},
		"binding for another context lifetime": {
			objects: func(_ *networkingv1alpha.Network, _, b *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(b.Object, "old-resolver-uid", "spec", "contextRef", "uid")
			},
			wantErr: true,
		},
		"binding DNS refused": {
			objects: func(_ *networkingv1alpha.Network, _, b *unstructured.Unstructured) {
				_ = unstructured.SetNestedSlice(b.Object, []any{map[string]any{
					"type": "Accepted", "status": "False", "observedGeneration": int64(1),
				}}, "status", "conditions")
			},
			wantErr: true,
		},
		"binding not yet accepted at its generation": {
			objects: func(_ *networkingv1alpha.Network, _, b *unstructured.Unstructured) {
				b.SetGeneration(2)
			},
			wantErr: true,
		},
		"binding accepted but not ready": {
			objects: func(_ *networkingv1alpha.Network, _, b *unstructured.Unstructured) {
				_ = unstructured.SetNestedSlice(b.Object, []any{
					map[string]any{"type": "Accepted", "status": "True", "observedGeneration": int64(1)},
					map[string]any{"type": "Ready", "status": "False", "observedGeneration": int64(1)},
				}, "status", "conditions")
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			network, dnsContext, binding := acceptedDNSAccess(t)
			if tc.objects != nil {
				tc.objects(network, dnsContext, binding)
			}
			networkContext := testNetworkContext("context", "context-uid")
			networkContext.Annotations = verifiedDNSIntent()
			if tc.annotations != nil {
				tc.annotations(networkContext.Annotations)
			}
			cl := fake.NewClientBuilder().WithScheme(internalDNSIntentScheme(t)).
				WithObjects(network, dnsContext, binding).Build()

			err := verifyInternalDNSIntent(context.Background(), cl, networkContext, networkContext.Spec.Network.Name, testDNSServicePrefix)
			if tc.wantErr != (err != nil) {
				t.Fatalf("verify = %v, want error %v", err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, errInternalDNSIntentUnverified) {
				t.Fatalf("a mismatch was reported as a failed check: %v", err)
			}
		})
	}
}

func TestReplicationCarriesOnlyVerifiedDNSIntent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scheme := internalDNSIntentScheme(t)
	gvk := schema.GroupVersionKind{Group: groupNetworkingDatumAPIs, Version: versionV1Alpha, Kind: KindNetworkContext}
	network, dnsContext, binding := acceptedDNSAccess(t)
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant", UID: "ns-uid"}}
	networkContext := testNetworkContext("context", "context-uid")
	networkContext.Annotations = verifiedDNSIntent()
	networkContext.Finalizers = []string{gatewayResourceReplicatorFinalizer}

	upstream := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(namespace, network, dnsContext, binding, networkContext).Build()
	downstream := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := newReplicatorForGVKTest(gvk, upstream, downstream, scheme)
	r.Config.InternalDNS.Enabled = true
	r.Config.InternalDNS.ServicePrefix = testDNSServicePrefix
	req := GVKRequest{GVK: gvk, Request: mcreconcile.Request{
		ClusterName: "upstream",
		Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(networkContext)},
	}}
	replicated := func() map[string]string {
		t.Helper()
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		copy := &unstructured.Unstructured{}
		copy.SetGroupVersionKind(gvk)
		if err := downstream.Get(ctx, client.ObjectKey{Namespace: "ns-ns-uid", Name: "context"}, copy); err != nil {
			t.Fatal(err)
		}
		return copy.GetAnnotations()
	}
	setUpstream := func(mutate func(map[string]string)) {
		t.Helper()
		current := &networkingv1alpha.NetworkContext{}
		if err := upstream.Get(ctx, client.ObjectKeyFromObject(networkContext), current); err != nil {
			t.Fatal(err)
		}
		if current.Annotations == nil {
			current.Annotations = map[string]string{}
		}
		mutate(current.Annotations)
		if err := upstream.Update(ctx, current); err != nil {
			t.Fatal(err)
		}
	}

	if got := replicated(); got[InternalDNSServiceDestinationAnnotation] != testDNSDestination {
		t.Fatalf("verified intent was not replicated: %v", got)
	}

	setUpstream(func(a map[string]string) { a[InternalDNSServiceDestinationAnnotation] = "fd70:100::99" })
	if got := replicated(); got[InternalDNSServiceDestinationAnnotation] != testDNSDestination {
		t.Fatalf("an unverified destination replaced the verified one: %v", got)
	}
	if result, err := r.Reconcile(ctx, req); err != nil || result.RequeueAfter == 0 {
		t.Fatalf("held intent is not checked again: %v, %v", result, err)
	}

	setCopy := func(mutate func(map[string]string)) {
		t.Helper()
		copy := &unstructured.Unstructured{}
		copy.SetGroupVersionKind(gvk)
		if err := downstream.Get(ctx, client.ObjectKey{Namespace: "ns-ns-uid", Name: "context"}, copy); err != nil {
			t.Fatal(err)
		}
		annotations := copy.GetAnnotations()
		mutate(annotations)
		copy.SetAnnotations(annotations)
		if err := downstream.Update(ctx, copy); err != nil {
			t.Fatal(err)
		}
	}
	setCopy(func(a map[string]string) { a[InternalDNSServiceDestinationAnnotation] = "fd70:100::98" })
	if got := replicated(); got[InternalDNSServiceDestinationAnnotation] != "" || got[InternalDNSAccessNameAnnotation] != "" {
		t.Fatalf("a copy kept annotations that never verified: %v", got)
	}

	setUpstream(func(a map[string]string) {
		maps.DeleteFunc(a, func(string, string) bool { return true })
		a[InternalDNSSuffixAnnotation] = "red.internal"
		a[InternalDNSRegionAnnotation] = "us-central"
	})
	if got := replicated(); got[InternalDNSSuffixAnnotation] != "" || got[InternalDNSRegionAnnotation] != "" {
		t.Fatalf("an incomplete set reached the copy unverified: %v", got)
	}
	setUpstream(func(a map[string]string) { maps.Copy(a, verifiedDNSIntent()) })
	if got := replicated(); got[InternalDNSServiceDestinationAnnotation] != testDNSDestination {
		t.Fatalf("verified intent was not replicated again: %v", got)
	}

	setUpstream(func(a map[string]string) { maps.DeleteFunc(a, func(string, string) bool { return true }) })
	if got := replicated(); got[InternalDNSServiceDestinationAnnotation] != "" {
		t.Fatalf("withdrawn intent stayed on the copy: %v", got)
	}

	r.Config.InternalDNS.Enabled = false
	setUpstream(func(a map[string]string) { maps.Copy(a, verifiedDNSIntent()) })
	if got := replicated(); got[InternalDNSServiceDestinationAnnotation] != "" {
		t.Fatalf("intent was replicated with internal DNS off: %v", got)
	}
}

func TestInternalDNSReadinessWaitsForVerifiedIntent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := networkingv1alpha.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	projectContext := testNetworkContext("context", "project-context-uid")
	projectContext.Annotations = verifiedDNSIntent()
	hubContext := projectContext.DeepCopy()
	projectContext.Annotations[InternalDNSSuffixAnnotation] = "red.internal"
	hubContext.Status.DNSAccess = &networkingv1alpha.NetworkDNSAccessStatus{
		ContextRef:         networkingv1alpha.NetworkDNSObjectReference{Name: "resolver"},
		AccessBindingRef:   networkingv1alpha.NetworkDNSObjectReference{Name: "access"},
		ServiceDestination: testDNSDestination,
		Nameservers:        []string{"fd53::53"},
	}
	hubContext.Status.Conditions = []metav1.Condition{{
		Type: networkingv1alpha.NetworkContextDNSReady, Status: metav1.ConditionTrue,
		Reason: networkingv1alpha.NetworkContextDNSReadyReasonReady,
	}}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&networkingv1alpha.NetworkContext{}).
		WithObjects(projectContext).Build()
	r := &InternalDNSReadinessReconciler{Config: config.InternalDNSConfig{Frontend: "fd53::53"}}

	if _, err := r.publish(ctx, cl, projectContext, hubContext); err != nil {
		t.Fatal(err)
	}
	current := &networkingv1alpha.NetworkContext{}
	if err := cl.Get(ctx, client.ObjectKeyFromObject(projectContext), current); err != nil {
		t.Fatal(err)
	}
	if current.Spec.DNS != nil {
		t.Fatalf("settings were published from intent the hub copy does not carry: %#v", current.Spec.DNS)
	}
	if condition := apimeta.FindStatusCondition(current.Status.Conditions, networkingv1alpha.NetworkContextDNSReady); condition == nil ||
		condition.Status == metav1.ConditionTrue {
		t.Fatalf("DNS reported ready from unverified intent: %#v", current.Status.Conditions)
	}
}
