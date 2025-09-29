package controller

import (
	"context"
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
		Ancestors: []gwapiv1alpha2.PolicyAncestorStatus{
			{
				AncestorRef: gwapiv1alpha2.ParentReference{
					Group:     ptr.To(gwapiv1.Group("gateway.networking.k8s.io")),
					Kind:      ptr.To(gwapiv1.Kind("Gateway")),
					Name:      gwapiv1.ObjectName("example-gateway"),
					Namespace: ptr.To(gwapiv1.Namespace("ns-ns-uid")),
				},
				ControllerName: gwapiv1.GatewayController("gateway.envoyproxy.io/controller"),
				Conditions: []metav1.Condition{
					{
						Type:               string(gwapiv1alpha2.PolicyConditionAccepted),
						Status:             metav1.ConditionFalse,
						Reason:             string(gwapiv1alpha2.PolicyReasonTargetNotFound),
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

func newReplicatorForTest(upstream client.Client, downstream client.Client, scheme *runtime.Scheme) *GatewayResourceReplicatorReconciler {
	upstreamCluster := &replicatorFakeCluster{scheme: scheme, c: upstream}
	downstreamCluster := &replicatorFakeCluster{scheme: scheme, c: downstream}

	resource := replicationResource{gvk: testGVK, controllerName: testControllerName}
	if cfg, ok := defaultReplicationResourceConfigs[gvkKey(testGVK)]; ok {
		resource.statusTransform = cfg.statusTransform
		resource.conditionHandlers = cfg.conditionHandlers
	}

	reconciler := &GatewayResourceReplicatorReconciler{
		Config: config.NetworkServicesOperator{
			Gateway: config.GatewayConfig{
				ControllerName: gwapiv1.GatewayController(testControllerName),
				ResourceReplicator: config.GatewayResourceReplicatorConfig{
					Resources: []config.ReplicatedResourceConfig{
						{Group: testGVK.Group, Version: testGVK.Version, Kind: testGVK.Kind},
					},
				},
			},
		},
		DownstreamCluster: downstreamCluster,
		resources: map[string]replicationResource{
			gvkKey(testGVK): resource,
		},
	}

	reconciler.mgr = &replicatorFakeManager{clusters: map[string]cluster.Cluster{"upstream": upstreamCluster}}

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
func (f *replicatorFakeCluster) GetRESTMapper() meta.RESTMapper { return nil }
func (f *replicatorFakeCluster) GetAPIReader() client.Reader    { return f.c }
func (f *replicatorFakeCluster) Start(context.Context) error    { return nil }

type replicatorFakeManager struct {
	mcmanager.Manager
	clusters map[string]cluster.Cluster
}

func (f *replicatorFakeManager) GetCluster(_ context.Context, name string) (cluster.Cluster, error) {
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
func (f *replicatorFakeManager) Engage(context.Context, string, cluster.Cluster) error {
	return nil
}
