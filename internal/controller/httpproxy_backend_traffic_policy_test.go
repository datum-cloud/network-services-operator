// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/managedfields"
	"k8s.io/apimachinery/pkg/util/uuid"
	clientgoapplyconfigurations "k8s.io/client-go/applyconfigurations"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayapplyconfiguration "sigs.k8s.io/gateway-api/applyconfiguration"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

func backendTrafficPolicyTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, gatewayv1.Install(s))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(s))
	require.NoError(t, discoveryv1.AddToScheme(s))
	require.NoError(t, networkingv1alpha.AddToScheme(s))
	require.NoError(t, networkingv1alpha1.AddToScheme(s))
	return s
}

func setPolicyAccepted(policy *envoygatewayv1alpha1.BackendTrafficPolicy, status metav1.ConditionStatus, reason, message string) {
	policy.Status.Ancestors = []gatewayv1.PolicyAncestorStatus{{
		AncestorRef: gatewayv1.ParentReference{
			Group:     ptr.To(gatewayv1.Group(gatewayv1.GroupName)),
			Kind:      ptr.To(gatewayv1.Kind(KindGateway)),
			Namespace: ptr.To(gatewayv1.Namespace(policy.Namespace)),
			Name:      "test",
		},
		ControllerName: "gateway.envoyproxy.io/gatewayclass-controller",
		Conditions: []metav1.Condition{{
			Type:               string(gatewayv1.PolicyConditionAccepted),
			Status:             status,
			Reason:             reason,
			Message:            message,
			LastTransitionTime: metav1.Now(),
		}},
	}}
}

func TestHTTPProxyReportsDisplacedBackendTrafficPolicy(t *testing.T) {
	ctx := context.Background()
	testScheme := backendTrafficPolicyTestScheme(t)

	namespaceUID := types.UID("22222222-2222-2222-2222-222222222222")
	downstreamNamespace := fmt.Sprintf("ns-%s", namespaceUID)

	httpProxy := newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
		controllerutil.AddFinalizer(h, httpProxyFinalizer)
		h.Spec.HealthCheck = &networkingv1alpha.HTTPProxyHealthCheck{
			Passive: &networkingv1alpha.HTTPProxyPassiveHealthCheck{},
		}
	})
	upstreamNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: httpProxy.Namespace, UID: namespaceUID}}
	gatewayClass := &gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "test-gateway-class"},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: "test-gateway-class"},
	}

	upstream := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithTypeConverters(gatewayapplyconfiguration.NewTypeConverter(testScheme), clientgoapplyconfigurations.NewTypeConverter(testScheme), managedfields.NewDeducedTypeConverter()).
		WithObjects(httpProxy, upstreamNamespace, gatewayClass).
		WithStatusSubresource(httpProxy, &gatewayv1.Gateway{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				obj.SetUID(uuid.NewUUID())
				obj.SetCreationTimestamp(metav1.Now())
				return c.Create(ctx, obj, opts...)
			},
		}).
		Build()

	downstream := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: downstreamNamespace}}).
		WithStatusSubresource(&envoygatewayv1alpha1.BackendTrafficPolicy{}).
		Build()

	reconciler := &HTTPProxyReconciler{
		mgr: &fakeMockManager{cl: upstream},
		Config: config.NetworkServicesOperator{
			HTTPProxy: config.HTTPProxyConfig{GatewayClassName: "test-gateway-class"},
			Gateway: config.GatewayConfig{
				ControllerName: gatewayv1.GatewayController("test-gateway-class"),
				TargetDomain:   "example.com",
			},
		},
		DownstreamCluster: &fakeCluster{cl: downstream},
	}
	req := mcreconcile.Request{
		Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(httpProxy)},
		ClusterName: "test-cluster",
	}

	_, err := reconciler.Reconcile(ctx, req)
	require.NoError(t, err)

	var gateway gatewayv1.Gateway
	require.NoError(t, upstream.Get(ctx, client.ObjectKeyFromObject(httpProxy), &gateway))
	apimeta.SetStatusCondition(&gateway.Status.Conditions, metav1.Condition{
		Type: string(gatewayv1.GatewayConditionAccepted), Status: metav1.ConditionTrue, Reason: string(gatewayv1.GatewayReasonAccepted),
	})
	setGatewayProgrammedWithDefaultHTTPSListener(&gateway)
	require.NoError(t, upstream.Status().Update(ctx, &gateway))

	var route gatewayv1.HTTPRoute
	require.NoError(t, upstream.Get(ctx, client.ObjectKeyFromObject(httpProxy), &route))
	require.NotEmpty(t, route.UID)

	programmed := func() *metav1.Condition {
		t.Helper()
		_, err := reconciler.Reconcile(ctx, req)
		require.NoError(t, err)
		var got networkingv1alpha.HTTPProxy
		require.NoError(t, upstream.Get(ctx, client.ObjectKeyFromObject(httpProxy), &got))
		c := apimeta.FindStatusCondition(got.Status.Conditions, networkingv1alpha.HTTPProxyConditionProgrammed)
		require.NotNil(t, c)
		return c
	}

	c := programmed()
	assert.Equal(t, metav1.ConditionTrue, c.Status, "no policy downstream yet: %s", c.Message)

	policy := &envoygatewayv1alpha1.BackendTrafficPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: downstreamNamespace,
			Name:      fmt.Sprintf("route-%s-panic-threshold", route.UID),
		},
	}
	require.NoError(t, downstream.Create(ctx, policy))
	setPolicyAccepted(policy, metav1.ConditionFalse, string(gatewayv1.PolicyReasonConflicted),
		"Unable to target HTTPRoute test, another BackendTrafficPolicy has already attached to it")
	require.NoError(t, downstream.Status().Update(ctx, policy))

	c = programmed()
	assert.Equal(t, metav1.ConditionFalse, c.Status)
	assert.Equal(t, networkingv1alpha.HTTPProxyReasonBackendSettingsNotApplied, c.Reason)
	assert.Contains(t, c.Message, "another BackendTrafficPolicy is attached to its route")
	assert.Contains(t, c.Message, `Gateway "test"`)
	t.Logf("conflicted: %s=%s %s: %s", c.Type, c.Status, c.Reason, c.Message)

	setPolicyAccepted(policy, metav1.ConditionFalse, "Invalid", "something else")
	require.NoError(t, downstream.Status().Update(ctx, policy))
	c = programmed()
	assert.Equal(t, metav1.ConditionFalse, c.Status)
	assert.Equal(t, networkingv1alpha.HTTPProxyReasonBackendSettingsNotApplied, c.Reason)
	assert.NotContains(t, c.Message, "something else", "edge messages can name downstream objects")

	setPolicyAccepted(policy, metav1.ConditionTrue, string(gatewayv1.PolicyReasonAccepted), "Policy has been accepted.")
	require.NoError(t, downstream.Status().Update(ctx, policy))

	c = programmed()
	assert.Equal(t, metav1.ConditionTrue, c.Status)
	assert.Equal(t, networkingv1alpha.HTTPProxyReasonProgrammed, c.Reason)
}

func TestEnqueueHTTPProxyForDownstreamBackendTrafficPolicy(t *testing.T) {
	owned := func(kind string) *envoygatewayv1alpha1.BackendTrafficPolicy {
		return &envoygatewayv1alpha1.BackendTrafficPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns-downstream",
				Name:      "route-uid-panic-threshold",
				Labels: map[string]string{
					downstreamclient.UpstreamOwnerClusterNameLabel: downstreamclient.UpstreamClusterNameLabelValue("project-a"),
					downstreamclient.UpstreamOwnerGroupLabel:       gatewayv1.GroupName,
					downstreamclient.UpstreamOwnerKindLabel:        kind,
					downstreamclient.UpstreamOwnerNameLabel:        "lb",
					downstreamclient.UpstreamOwnerNamespaceLabel:   "default",
				},
			},
		}
	}

	handler := (&HTTPProxyReconciler{}).enqueueHTTPProxyForDownstreamBackendTrafficPolicy()("", nil)
	enqueued := func(policy *envoygatewayv1alpha1.BackendTrafficPolicy) []mcreconcile.Request {
		q := newTestQueue()
		handler.Update(context.Background(), event.TypedUpdateEvent[*envoygatewayv1alpha1.BackendTrafficPolicy]{ObjectOld: policy, ObjectNew: policy}, q)
		return drainQueue(q)
	}

	assert.Equal(t, []mcreconcile.Request{{
		ClusterName: "project-a",
		Request:     ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "lb"}},
	}}, enqueued(owned("HTTPRoute")))
	assert.Empty(t, enqueued(owned("BackendTrafficPolicy")), "a customer policy replicated downstream belongs to no HTTPProxy")
}
