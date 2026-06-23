package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	mgrconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
)

const testControllerName = "gateway.networking.datumapis.com/test-controller"

var (
	testGVK = schema.GroupVersionKind{Group: "gateway.envoyproxy.io", Version: "v1alpha1", Kind: "SecurityPolicy"}
)

func TestReplicatorMirrorsResource(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	if !assert.NoError(t, err, "add core to scheme") {
		return
	}

	ctx := context.Background()

	upstreamNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-suite",
			UID:  types.UID("ns-uid"),
		},
	}
	upstreamObj := newUnstructuredObject(
		upstreamNs.Name,
		"example",
		map[string]string{"replicate": "true"},
		map[string]any{"foo": "bar"},
	)
	upstreamObj.SetUID("policy-uid")

	upstreamStatusTemplate := &unstructured.Unstructured{}
	upstreamStatusTemplate.SetGroupVersionKind(testGVK)
	upstreamClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(upstreamStatusTemplate).
		WithObjects(upstreamNs, upstreamObj.DeepCopy()).
		Build()

	downstreamClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := newReplicatorForTest(upstreamClient, downstreamClient, scheme)

	req := gvkRequestFor(upstreamObj)

	_, err = reconciler.Reconcile(ctx, req)
	if !assert.NoError(t, err, "first reconcile") {
		return
	}
	_, err = reconciler.Reconcile(ctx, req)
	if !assert.NoError(t, err, "second reconcile") {
		return
	}

	// Upstream gains a finalizer
	var updatedUpstream unstructured.Unstructured
	updatedUpstream.SetGroupVersionKind(testGVK)
	err = upstreamClient.Get(ctx, client.ObjectKeyFromObject(upstreamObj), &updatedUpstream)
	if !assert.NoError(t, err, "get upstream") {
		return
	}

	assert.Contains(t, updatedUpstream.GetFinalizers(), gatewayResourceReplicatorFinalizer, "finalizer missing")

	// Downstream object mirrors spec & metadata
	var downstream unstructured.Unstructured
	downstream.SetGroupVersionKind(testGVK)
	err = downstreamClient.Get(ctx, client.ObjectKey{Name: "example", Namespace: "ns-ns-uid"}, &downstream)
	if !assert.NoError(t, err, "get downstream") {
		return
	}

	if got, want := downstream.Object["spec"], upstreamObj.Object["spec"]; !apiequality.Semantic.DeepEqual(got, want) {
		assert.Equal(t, want, got, "spec mismatch")
	}

	// Populate downstream status and reconcile to propagate upstream status updates.
	err = downstreamClient.Get(ctx, client.ObjectKey{Name: "example", Namespace: "ns-ns-uid"}, &downstream)
	if !assert.NoError(t, err, "get downstream for status update") {
		return
	}

	downstreamPolicyStatus := gwapiv1alpha2.PolicyStatus{
		Ancestors: []gwapiv1.PolicyAncestorStatus{
			{
				AncestorRef: gwapiv1.ParentReference{
					Group:     ptr.To(gwapiv1.Group("gateway.networking.k8s.io")),
					Kind:      ptr.To(gwapiv1.Kind("Gateway")),
					Name:      gwapiv1.ObjectName("example-gateway"),
					Namespace: ptr.To(gwapiv1.Namespace("ns-ns-uid")),
				},
				ControllerName: gwapiv1.GatewayController("gateway.envoyproxy.io/controller"),
				Conditions: []metav1.Condition{
					{
						Type:               string(gwapiv1.PolicyConditionAccepted),
						Status:             metav1.ConditionFalse,
						Reason:             string(gwapiv1.PolicyReasonTargetNotFound),
						Message:            "downstream attachment target missing",
						LastTransitionTime: metav1.Now(),
						ObservedGeneration: 1,
					},
				},
			},
		},
	}

	statusMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&downstreamPolicyStatus)
	if !assert.NoError(t, err, "convert policy status") {
		return
	}

	downstream.Object["status"] = statusMap
	err = downstreamClient.Update(ctx, &downstream)
	if !assert.NoError(t, err, "update downstream status") {
		return
	}

	resourceCfg := reconciler.resources[gvkKey(testGVK)]
	downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy("upstream", upstreamClient, downstreamClient)

	var upstreamForStatus unstructured.Unstructured
	upstreamForStatus.SetGroupVersionKind(testGVK)
	err = upstreamClient.Get(ctx, client.ObjectKeyFromObject(upstreamObj), &upstreamForStatus)
	if !assert.NoError(t, err, "get upstream for status sync") {
		return
	}

	downstreamMeta, err := downstreamStrategy.ObjectMetaFromUpstreamObject(ctx, &upstreamForStatus)
	if !assert.NoError(t, err, "downstream metadata") {
		return
	}

	err = reconciler.syncUpstreamStatus(ctx, resourceCfg, upstreamClient, &upstreamForStatus, downstreamMeta, downstreamStrategy)
	if !assert.NoError(t, err, "sync upstream status") {
		return
	}

	var upstreamWithStatus unstructured.Unstructured
	upstreamWithStatus.SetGroupVersionKind(testGVK)
	err = upstreamClient.Get(ctx, client.ObjectKeyFromObject(upstreamObj), &upstreamWithStatus)
	if !assert.NoError(t, err, "get upstream post-status sync") {
		return
	}

	status, ok := upstreamWithStatus.Object["status"].(map[string]any)
	if !assert.True(t, ok, "upstream status missing or wrong type: %v", updatedUpstream.Object["status"]) {
		return
	}

	ancestorsAny, ok := status["ancestors"].([]any)
	if !assert.True(t, ok && len(ancestorsAny) > 0, "upstream status ancestors missing: %v", status) {
		return
	}

	ancestor, ok := ancestorsAny[0].(map[string]any)
	if !assert.True(t, ok, "unexpected ancestor type: %T", ancestorsAny[0]) {
		return
	}

	ancestorRef, ok := ancestor["ancestorRef"].(map[string]any)
	if !assert.True(t, ok, "ancestorRef missing: %v", ancestor) {
		return
	}

	if namespace, _ := ancestorRef["namespace"].(string); namespace != upstreamNs.Name {
		assert.Equal(t, upstreamNs.Name, namespace, "ancestor namespace not rewritten")
	}

	conditionsAny, ok := ancestor["conditions"].([]any)
	if !assert.True(t, ok && len(conditionsAny) > 0, "ancestor conditions missing: %v", ancestor) {
		return
	}

	condition, ok := conditionsAny[0].(map[string]any)
	if !assert.True(t, ok, "unexpected condition type: %T", conditionsAny[0]) {
		return
	}

	assert.Equal(t, "referenced resource not found in upstream namespace", condition["message"], "condition message not filtered")

	assert.Equal(t, testControllerName, ancestor["controllerName"], "controllerName not rewritten")

	// Anchor created
	var anchor corev1.ConfigMap
	if err := downstreamClient.Get(ctx, client.ObjectKey{Name: "anchor-policy-uid", Namespace: "ns-ns-uid"}, &anchor); err != nil {
		t.Fatalf("anchor missing: %v", err)
	}
}

func TestTypedEnqueueRequestForGVKFiltersBySelector(t *testing.T) {
	selector := labels.SelectorFromSet(map[string]string{"replicate": "true"})
	factory := typedEnqueueRequestForGVK(testGVK, selector)
	h := factory("upstream", nil)
	queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[GVKRequest]())
	t.Cleanup(queue.ShutDown)

	ctx := context.Background()

	nonMatching := newUnstructuredObject("workloads", "example", map[string]string{"replicate": "false"}, nil)
	h.Create(ctx, event.TypedCreateEvent[client.Object]{Object: nonMatching}, queue)
	if !assert.Equal(t, 0, queue.Len(), "expected queue empty for non-matching labels") {
		return
	}

	matching := newUnstructuredObject("workloads", "example", map[string]string{"replicate": "true"}, nil)
	h.Create(ctx, event.TypedCreateEvent[client.Object]{Object: matching}, queue)
	if !assert.Equal(t, 1, queue.Len(), "expected single request for matching labels") {
		return
	}

	item, shutdown := queue.Get()
	assert.False(t, shutdown, "unexpected queue shutdown")
	queue.Done(item)

	assert.Equal(t, testGVK, item.GVK, "unexpected GVK enqueued")
}

func TestReplicatorCleansDownstreamOnDelete(t *testing.T) {
	scheme := runtime.NewScheme()
	err := corev1.AddToScheme(scheme)
	if !assert.NoError(t, err, "add core to scheme") {
		return
	}
	ctx := context.Background()

	upstreamNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-suite",
			UID:  types.UID("ns-uid"),
		},
	}
	upstreamObj := newUnstructuredObject(upstreamNs.Name, "example", map[string]string{"replicate": "true"}, map[string]any{"foo": "bar"})
	upstreamObj.SetUID("policy-uid")

	upstreamStatusTemplate := &unstructured.Unstructured{}
	upstreamStatusTemplate.SetGroupVersionKind(testGVK)
	upstreamClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(upstreamStatusTemplate).
		WithObjects(upstreamNs, upstreamObj.DeepCopy()).
		Build()

	downstreamClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := newReplicatorForTest(upstreamClient, downstreamClient, scheme)

	req := gvkRequestFor(upstreamObj)

	_, err = reconciler.Reconcile(ctx, req)
	if !assert.NoError(t, err, "first reconcile") {
		return
	}
	_, err = reconciler.Reconcile(ctx, req)
	if !assert.NoError(t, err, "second reconcile") {
		return
	}

	// mark upstream deletion
	var upstream unstructured.Unstructured
	upstream.SetGroupVersionKind(testGVK)
	err = upstreamClient.Get(ctx, client.ObjectKeyFromObject(upstreamObj), &upstream)
	if !assert.NoError(t, err, "get upstream") {
		return
	}

	deleteTime := metav1.NewTime(time.Now())
	upstream.SetDeletionTimestamp(&deleteTime)

	// Build a fresh upstream client mirroring the deletion state so the fake tracker accepts it.
	var ns corev1.Namespace
	err = upstreamClient.Get(ctx, client.ObjectKeyFromObject(upstreamNs), &ns)
	if !assert.NoError(t, err, "get namespace") {
		return
	}

	upstreamWithDeletion := upstream.DeepCopy()
	upstreamWithDeletion.SetDeletionTimestamp(&deleteTime)

	upstreamStatusTemplateWithDeletion := &unstructured.Unstructured{}
	upstreamStatusTemplateWithDeletion.SetGroupVersionKind(testGVK)
	upstreamClientWithDeletion := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(upstreamStatusTemplateWithDeletion).
		WithObjects(ns.DeepCopy(), upstreamWithDeletion).
		Build()

	deleteReconciler := newReplicatorForTest(upstreamClientWithDeletion, downstreamClient, scheme)

	_, err = deleteReconciler.Reconcile(ctx, req)
	if !assert.NoError(t, err, "reconcile delete") {
		return
	}

	var downstream unstructured.Unstructured
	downstream.SetGroupVersionKind(testGVK)
	if err := downstreamClient.Get(ctx, client.ObjectKey{Name: "example", Namespace: "ns-ns-uid"}, &downstream); !assert.True(t, apierrors.IsNotFound(err), "downstream should be removed, err=%v", err) {
		return
	}

	var anchor corev1.ConfigMap
	if err := downstreamClient.Get(ctx, client.ObjectKey{Name: "anchor-policy-uid", Namespace: "ns-ns-uid"}, &anchor); !assert.True(t, apierrors.IsNotFound(err), "anchor should be removed, err=%v", err) {
		return
	}

	var upstreamFinal unstructured.Unstructured
	upstreamFinal.SetGroupVersionKind(testGVK)
	if err := upstreamClientWithDeletion.Get(ctx, client.ObjectKey{Name: "example", Namespace: "workloads"}, &upstreamFinal); !assert.True(t, apierrors.IsNotFound(err), "expected upstream object removed, err=%v", err) {
		return
	}
}

// TestReplicatorMirrorsNSOPolicyTypesSkipsUpstreamStatusSync verifies that
// when the replicator handles TrafficProtectionPolicy or HTTPProxy it:
//   - copies spec into the downstream ns-<uid> namespace, and
//   - does NOT clear or overwrite existing upstream status (those conditions are
//     set by NSO's own TPP / HTTPProxy controllers, not by a downstream actor).
func TestReplicatorMirrorsNSOPolicyTypesSkipsUpstreamStatusSync(t *testing.T) {
	for _, gvk := range []schema.GroupVersionKind{
		{Group: "networking.datumapis.com", Version: "v1alpha", Kind: "TrafficProtectionPolicy"},
		{Group: "networking.datumapis.com", Version: "v1alpha", Kind: "HTTPProxy"},
	} {
		t.Run(gvk.Kind, func(t *testing.T) {
			scheme := runtime.NewScheme()
			assert.NoError(t, corev1.AddToScheme(scheme))

			ctx := context.Background()

			upstreamNs := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "test-suite", UID: types.UID("ns-uid")},
			}

			// Pre-populate the upstream object with a status already set by
			// NSO (simulates real-world state after the TPP/HTTPProxy controller
			// has run). The replicator must leave this status intact.
			upstreamStatusTemplate := &unstructured.Unstructured{}
			upstreamStatusTemplate.SetGroupVersionKind(gvk)

			upstreamObj := &unstructured.Unstructured{}
			upstreamObj.SetGroupVersionKind(gvk)
			upstreamObj.SetNamespace(upstreamNs.Name)
			upstreamObj.SetName("test-policy")
			upstreamObj.SetUID("policy-uid")
			upstreamObj.Object["spec"] = map[string]any{"key": "val"}
			// Simulate NSO-set upstream status that must not be cleared.
			upstreamObj.Object["status"] = map[string]any{
				"conditions": []any{
					map[string]any{
						"type":   "Accepted",
						"status": "True",
						"reason": "Accepted",
					},
				},
			}

			upstreamClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(upstreamStatusTemplate).
				WithObjects(upstreamNs, upstreamObj.DeepCopy()).
				Build()

			downstreamClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			reconciler := newReplicatorForGVKTest(gvk, upstreamClient, downstreamClient, scheme)

			req := GVKRequest{
				GVK: gvk,
				Request: mcreconcile.Request{
					ClusterName: "upstream",
					Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(upstreamObj)},
				},
			}

			// Two passes: first adds the finalizer, second does the replication.
			_, err := reconciler.Reconcile(ctx, req)
			assert.NoError(t, err, "first reconcile")
			_, err = reconciler.Reconcile(ctx, req)
			assert.NoError(t, err, "second reconcile")

			// Downstream object must exist with the same spec.
			var downstream unstructured.Unstructured
			downstream.SetGroupVersionKind(gvk)
			assert.NoError(t, downstreamClient.Get(ctx,
				client.ObjectKey{Name: "test-policy", Namespace: "ns-ns-uid"}, &downstream),
				"downstream object must be created")
			assert.Equal(t, upstreamObj.Object["spec"], downstream.Object["spec"],
				"downstream spec must mirror upstream spec")

			// Upstream status must be unchanged — the replicator must NOT zero it.
			var upstreamAfter unstructured.Unstructured
			upstreamAfter.SetGroupVersionKind(gvk)
			assert.NoError(t, upstreamClient.Get(ctx, client.ObjectKeyFromObject(upstreamObj), &upstreamAfter))
			assert.Equal(t, upstreamObj.Object["status"], upstreamAfter.Object["status"],
				"replicator must not clear or overwrite upstream NSO-set status")
		})
	}
}

// TestReplicatorMirrorsConnectorSpecAndLivenessAnnotation verifies that when the
// replicator handles a Connector it:
//   - copies spec into the downstream ns-<uid> namespace, AND
//   - mirrors the upstream .status verbatim into the UpstreamStatusAnnotation
//     onto the downstream object's metadata so the edge extension server can read
//     connector liveness locally.
//
// The annotation — not the status subresource — carries the status because
// Karmada propagates a resource template's spec + metadata to member clusters
// but NOT its status.
func TestReplicatorMirrorsConnectorSpecAndLivenessAnnotation(t *testing.T) {
	connectorGVK := schema.GroupVersionKind{
		Group: "networking.datumapis.com", Version: "v1alpha1", Kind: "Connector",
	}

	scheme := runtime.NewScheme()
	assert.NoError(t, corev1.AddToScheme(scheme))

	ctx := context.Background()

	upstreamNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "test-suite", UID: types.UID("ns-uid")},
	}

	connectorStatus := map[string]any{
		"conditions": []any{
			map[string]any{
				"type":   "Ready",
				"status": "True",
				"reason": "Ready",
			},
		},
		"connectionDetails": map[string]any{
			"type": "PublicKey",
			"publicKey": map[string]any{
				"id": "node-abc",
			},
		},
	}

	upstreamStatusTemplate := &unstructured.Unstructured{}
	upstreamStatusTemplate.SetGroupVersionKind(connectorGVK)

	upstreamObj := &unstructured.Unstructured{}
	upstreamObj.SetGroupVersionKind(connectorGVK)
	upstreamObj.SetNamespace(upstreamNs.Name)
	upstreamObj.SetName("connector-1")
	upstreamObj.SetUID("connector-uid")
	upstreamObj.Object["spec"] = map[string]any{"connectorClassName": "iroh"}
	upstreamObj.Object["status"] = connectorStatus

	upstreamClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(upstreamStatusTemplate).
		WithObjects(upstreamNs, upstreamObj.DeepCopy()).
		Build()

	downstreamClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	reconciler := newReplicatorForGVKTest(connectorGVK, upstreamClient, downstreamClient, scheme)

	req := GVKRequest{
		GVK: connectorGVK,
		Request: mcreconcile.Request{
			ClusterName: "upstream",
			Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(upstreamObj)},
		},
	}

	// Two passes: first adds the finalizer, second does the spec + annotation sync.
	_, err := reconciler.Reconcile(ctx, req)
	assert.NoError(t, err, "first reconcile")
	_, err = reconciler.Reconcile(ctx, req)
	assert.NoError(t, err, "second reconcile")

	// Downstream spec must mirror upstream.
	var downstream unstructured.Unstructured
	downstream.SetGroupVersionKind(connectorGVK)
	assert.NoError(t, downstreamClient.Get(ctx,
		client.ObjectKey{Name: "connector-1", Namespace: "ns-ns-uid"}, &downstream),
		"downstream connector must be created")

	assert.Equal(t, upstreamObj.Object["spec"], downstream.Object["spec"],
		"downstream spec must mirror upstream spec")

	// Key assertion: the annotation carries the upstream .status verbatim — the
	// replicator mirrors the whole status object resource-agnostically, with no
	// bespoke per-type shape.
	expected, err := json.Marshal(connectorStatus)
	assert.NoError(t, err)
	assert.Equal(t, string(expected),
		downstream.GetAnnotations()[networkingv1alpha1.UpstreamStatusAnnotation],
		"downstream annotation must carry the full upstream status verbatim")

	// The replicator must NOT mirror the status subresource downstream (Karmada
	// would not propagate it to members anyway).
	_, hasStatus := downstream.Object["status"]
	assert.False(t, hasStatus, "replicator must not write the downstream status subresource for connectors")

	// Upstream status must be untouched (skipUpstreamStatusSync=true).
	var upstreamAfter unstructured.Unstructured
	upstreamAfter.SetGroupVersionKind(connectorGVK)
	assert.NoError(t, upstreamClient.Get(ctx, client.ObjectKeyFromObject(upstreamObj), &upstreamAfter))
	assert.Equal(t, connectorStatus, upstreamAfter.Object["status"],
		"replicator must not clear or modify upstream Connector status")
}

// TestReplicatorConnectorNotReadyLivenessAnnotation verifies that a Connector
// whose Ready condition is False mirrors that not-ready status verbatim into the
// annotation (no connectionDetails).
func TestReplicatorConnectorNotReadyLivenessAnnotation(t *testing.T) {
	connectorGVK := schema.GroupVersionKind{
		Group: "networking.datumapis.com", Version: "v1alpha1", Kind: "Connector",
	}

	scheme := runtime.NewScheme()
	assert.NoError(t, corev1.AddToScheme(scheme))

	ctx := context.Background()

	upstreamNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "test-suite", UID: types.UID("ns-uid")},
	}

	connectorStatus := map[string]any{
		"conditions": []any{
			map[string]any{"type": "Ready", "status": "False", "reason": "ConnectorNotReady"},
		},
	}

	upstreamStatusTemplate := &unstructured.Unstructured{}
	upstreamStatusTemplate.SetGroupVersionKind(connectorGVK)

	upstreamObj := &unstructured.Unstructured{}
	upstreamObj.SetGroupVersionKind(connectorGVK)
	upstreamObj.SetNamespace(upstreamNs.Name)
	upstreamObj.SetName("connector-down")
	upstreamObj.SetUID("connector-down-uid")
	upstreamObj.Object["spec"] = map[string]any{"connectorClassName": "iroh"}
	upstreamObj.Object["status"] = connectorStatus

	upstreamClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(upstreamStatusTemplate).
		WithObjects(upstreamNs, upstreamObj.DeepCopy()).
		Build()

	downstreamClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := newReplicatorForGVKTest(connectorGVK, upstreamClient, downstreamClient, scheme)

	req := GVKRequest{
		GVK: connectorGVK,
		Request: mcreconcile.Request{
			ClusterName: "upstream",
			Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(upstreamObj)},
		},
	}

	_, err := reconciler.Reconcile(ctx, req)
	assert.NoError(t, err, "first reconcile")
	_, err = reconciler.Reconcile(ctx, req)
	assert.NoError(t, err, "second reconcile")

	var downstream unstructured.Unstructured
	downstream.SetGroupVersionKind(connectorGVK)
	assert.NoError(t, downstreamClient.Get(ctx,
		client.ObjectKey{Name: "connector-down", Namespace: "ns-ns-uid"}, &downstream))

	expected, err := json.Marshal(connectorStatus)
	assert.NoError(t, err)
	assert.Equal(t, string(expected),
		downstream.GetAnnotations()[networkingv1alpha1.UpstreamStatusAnnotation],
		"not-ready connector must mirror its not-ready status verbatim into the annotation")
}

// TestReplicatorConnectorLivenessAnnotationIdempotent verifies that repeated
// reconciles of an already-synced Connector keep the status annotation stable
// and do not error.
func TestReplicatorConnectorLivenessAnnotationIdempotent(t *testing.T) {
	connectorGVK := schema.GroupVersionKind{
		Group: "networking.datumapis.com", Version: "v1alpha1", Kind: "Connector",
	}

	scheme := runtime.NewScheme()
	assert.NoError(t, corev1.AddToScheme(scheme))

	ctx := context.Background()

	upstreamNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "test-suite", UID: types.UID("ns-uid")},
	}

	connectorStatus := map[string]any{
		"conditions": []any{
			map[string]any{"type": "Ready", "status": "True", "reason": "Ready"},
		},
	}

	upstreamStatusTemplate := &unstructured.Unstructured{}
	upstreamStatusTemplate.SetGroupVersionKind(connectorGVK)

	upstreamObj := &unstructured.Unstructured{}
	upstreamObj.SetGroupVersionKind(connectorGVK)
	upstreamObj.SetNamespace(upstreamNs.Name)
	upstreamObj.SetName("connector-idem")
	upstreamObj.SetUID("connector-idem-uid")
	upstreamObj.Object["spec"] = map[string]any{"connectorClassName": "iroh"}
	upstreamObj.Object["status"] = connectorStatus

	upstreamClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(upstreamStatusTemplate).
		WithObjects(upstreamNs, upstreamObj.DeepCopy()).
		Build()

	wrappedDownstream := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := newReplicatorForGVKTest(connectorGVK, upstreamClient, wrappedDownstream, scheme)

	req := GVKRequest{
		GVK: connectorGVK,
		Request: mcreconcile.Request{
			ClusterName: "upstream",
			Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(upstreamObj)},
		},
	}

	// Pass 1: add finalizer. Pass 2: spec + annotation sync. Pass 3: idempotent.
	_, err := reconciler.Reconcile(ctx, req)
	assert.NoError(t, err, "first reconcile")
	_, err = reconciler.Reconcile(ctx, req)
	assert.NoError(t, err, "second reconcile")
	_, err = reconciler.Reconcile(ctx, req)
	assert.NoError(t, err, "third reconcile (idempotent)")

	var downstream unstructured.Unstructured
	downstream.SetGroupVersionKind(connectorGVK)
	assert.NoError(t, wrappedDownstream.Get(ctx,
		client.ObjectKey{Name: "connector-idem", Namespace: "ns-ns-uid"}, &downstream))

	expected, err := json.Marshal(connectorStatus)
	assert.NoError(t, err)
	assert.Equal(t, string(expected),
		downstream.GetAnnotations()[networkingv1alpha1.UpstreamStatusAnnotation],
		"status annotation must remain stable after an idempotent reconcile")
}

// TestReplicatorReMirrorsConnectorAfterReadyFlip verifies that after an upstream
// Connector's Ready condition flips False→True (a status-only change), a
// reconcile re-mirrors the new status into the downstream upstream-status
// annotation — the downstream annotation tracks upstream status changes.
func TestReplicatorReMirrorsConnectorAfterReadyFlip(t *testing.T) {
	connectorGVK := schema.GroupVersionKind{
		Group: "networking.datumapis.com", Version: "v1alpha1", Kind: "Connector",
	}

	scheme := runtime.NewScheme()
	assert.NoError(t, corev1.AddToScheme(scheme))

	ctx := context.Background()

	upstreamNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "test-suite", UID: types.UID("ns-uid")},
	}

	// connectionDetails are populated (agent connected) while Ready is still False.
	connectionDetails := map[string]any{
		"type": "PublicKey",
		"publicKey": map[string]any{
			"id": "378843c806c8c93c5770abaa19bc47e04e9f56977c6e7cc28044a09ef5a1cd23",
		},
	}
	notReadyStatus := map[string]any{
		"conditions": []any{
			map[string]any{
				"type":    "Ready",
				"status":  "False",
				"reason":  "ConnectorNotReady",
				"message": "Connector lease has expired. Agent may be offline.",
			},
		},
		"connectionDetails": connectionDetails,
	}

	upstreamStatusTemplate := &unstructured.Unstructured{}
	upstreamStatusTemplate.SetGroupVersionKind(connectorGVK)

	upstreamObj := &unstructured.Unstructured{}
	upstreamObj.SetGroupVersionKind(connectorGVK)
	upstreamObj.SetNamespace(upstreamNs.Name)
	upstreamObj.SetName("connector-209")
	upstreamObj.SetUID("connector-209-uid")
	upstreamObj.Object["spec"] = map[string]any{"connectorClassName": "iroh"}
	upstreamObj.Object["status"] = notReadyStatus

	upstreamClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(upstreamStatusTemplate).
		WithObjects(upstreamNs, upstreamObj.DeepCopy()).
		Build()

	downstreamClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := newReplicatorForGVKTest(connectorGVK, upstreamClient, downstreamClient, scheme)

	req := GVKRequest{
		GVK: connectorGVK,
		Request: mcreconcile.Request{
			ClusterName: "upstream",
			Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(upstreamObj)},
		},
	}

	// Initial replication while Ready:False (finalizer pass + sync pass).
	_, err := reconciler.Reconcile(ctx, req)
	assert.NoError(t, err, "first reconcile")
	_, err = reconciler.Reconcile(ctx, req)
	assert.NoError(t, err, "second reconcile")

	dsKey := client.ObjectKey{Name: "connector-209", Namespace: "ns-ns-uid"}

	var downstream unstructured.Unstructured
	downstream.SetGroupVersionKind(connectorGVK)
	assert.NoError(t, downstreamClient.Get(ctx, dsKey, &downstream))

	expectedNotReady, err := json.Marshal(notReadyStatus)
	assert.NoError(t, err)
	assert.Equal(t, string(expectedNotReady),
		downstream.GetAnnotations()[networkingv1alpha1.UpstreamStatusAnnotation],
		"initial mirror must capture the Ready:False state")

	// Flip upstream Ready False→True via a status-only update (no spec change).
	var upstreamLive unstructured.Unstructured
	upstreamLive.SetGroupVersionKind(connectorGVK)
	assert.NoError(t, upstreamClient.Get(ctx, client.ObjectKeyFromObject(upstreamObj), &upstreamLive))
	readyStatus := map[string]any{
		"conditions": []any{
			map[string]any{
				"type":    "Ready",
				"status":  "True",
				"reason":  "ConnectorReady",
				"message": "The connector is ready to tunnel traffic.",
			},
		},
		"connectionDetails": connectionDetails,
	}
	upstreamLive.Object["status"] = readyStatus
	assert.NoError(t, upstreamClient.Status().Update(ctx, &upstreamLive),
		"flip upstream connector to Ready:True via status subresource")

	// Reconcile again, as the upstream status watch would in production.
	_, err = reconciler.Reconcile(ctx, req)
	assert.NoError(t, err, "reconcile after Ready flip")

	downstream = unstructured.Unstructured{}
	downstream.SetGroupVersionKind(connectorGVK)
	assert.NoError(t, downstreamClient.Get(ctx, dsKey, &downstream))

	expectedReady, err := json.Marshal(readyStatus)
	assert.NoError(t, err)
	assert.Equal(t, string(expectedReady),
		downstream.GetAnnotations()[networkingv1alpha1.UpstreamStatusAnnotation],
		"after Ready flips True, the replicator must re-mirror the annotation with Ready:True")
}

func newReplicatorForTest(upstream client.Client, downstream client.Client, scheme *runtime.Scheme) *GatewayResourceReplicatorReconciler {
	return newReplicatorForGVKTest(testGVK, upstream, downstream, scheme)
}

// newReplicatorForGVKTest builds a GatewayResourceReplicatorReconciler wired
// for the given GVK with fake upstream/downstream clients. It pulls the config
// from defaultReplicationResourceConfigs so that type-specific flags
// (skipUpstreamStatusSync, mirrorStatusToAnnotation) are honoured in tests.
func newReplicatorForGVKTest(gvk schema.GroupVersionKind, upstream client.Client, downstream client.Client, scheme *runtime.Scheme) *GatewayResourceReplicatorReconciler {
	upstreamCluster := &replicatorFakeCluster{scheme: scheme, c: upstream}
	downstreamCluster := &replicatorFakeCluster{scheme: scheme, c: downstream}

	resource := replicationResource{gvk: gvk, downstreamGVK: gvk, controllerName: testControllerName}
	if cfg, ok := defaultReplicationResourceConfigs[gvkKey(gvk)]; ok {
		resource.replicationResourceConfig = cfg
	}

	reconciler := &GatewayResourceReplicatorReconciler{
		Config: config.NetworkServicesOperator{
			Gateway: config.GatewayConfig{
				ControllerName: gwapiv1.GatewayController(testControllerName),
				ResourceReplicator: config.GatewayResourceReplicatorConfig{
					Resources: []config.ReplicatedResourceConfig{
						{Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind},
					},
				},
			},
		},
		DownstreamCluster: downstreamCluster,
		resources: map[string]replicationResource{
			gvkKey(gvk): resource,
		},
	}

	reconciler.mgr = &replicatorFakeManager{clusters: map[multicluster.ClusterName]cluster.Cluster{"upstream": upstreamCluster}}

	return reconciler
}

func gvkRequestFor(obj client.Object) GVKRequest {
	return GVKRequest{
		GVK: testGVK,
		Request: mcreconcile.Request{
			ClusterName: "upstream",
			Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(obj)},
		},
	}
}

// nolint:unparam
func newUnstructuredObject(namespace, name string, labels map[string]string, spec map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(testGVK)
	obj.SetNamespace(namespace)
	obj.SetName(name)
	obj.SetLabels(labels)
	if spec != nil {
		obj.Object["spec"] = spec
	}
	return obj
}

type replicatorFakeCluster struct {
	scheme *runtime.Scheme
	c      client.Client
}

func (f *replicatorFakeCluster) GetHTTPClient() *http.Client          { return &http.Client{} }
func (f *replicatorFakeCluster) GetConfig() *rest.Config              { return &rest.Config{} }
func (f *replicatorFakeCluster) GetCache() cache.Cache                { return nil }
func (f *replicatorFakeCluster) GetScheme() *runtime.Scheme           { return f.scheme }
func (f *replicatorFakeCluster) GetClient() client.Client             { return f.c }
func (f *replicatorFakeCluster) GetFieldIndexer() client.FieldIndexer { return nil }
func (f *replicatorFakeCluster) GetEventRecorderFor(string) record.EventRecorder {
	return record.NewFakeRecorder(10)
}
func (f *replicatorFakeCluster) GetEventRecorder(string) events.EventRecorder { return nil }
func (f *replicatorFakeCluster) GetRESTMapper() meta.RESTMapper               { return nil }
func (f *replicatorFakeCluster) GetAPIReader() client.Reader                  { return f.c }
func (f *replicatorFakeCluster) Start(context.Context) error                  { return nil }

type replicatorFakeManager struct {
	mcmanager.Manager
	clusters map[multicluster.ClusterName]cluster.Cluster
}

func (f *replicatorFakeManager) GetCluster(_ context.Context, name multicluster.ClusterName) (cluster.Cluster, error) {
	cl, ok := f.clusters[name]
	if !ok {
		return nil, fmt.Errorf("cluster %s not found", name)
	}
	return cl, nil
}

func (f *replicatorFakeManager) GetControllerOptions() mgrconfig.Controller {
	return mgrconfig.Controller{}
}
func (f *replicatorFakeManager) GetLogger() logr.Logger               { return logr.Discard() }
func (f *replicatorFakeManager) GetWebhookServer() webhook.Server     { return nil }
func (f *replicatorFakeManager) GetFieldIndexer() client.FieldIndexer { return nil }
func (f *replicatorFakeManager) GetProvider() multicluster.Provider   { return nil }
func (f *replicatorFakeManager) Engage(context.Context, multicluster.ClusterName, cluster.Cluster) error {
	return nil
}
