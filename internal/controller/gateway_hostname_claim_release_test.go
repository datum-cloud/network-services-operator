// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

type claimRig struct {
	t          *testing.T
	ctx        context.Context
	reconciler *GatewayReconciler
	upstream   client.Client
	downstream client.Client
	strategy   downstreamclient.ResourceStrategy
	gateway    client.ObjectKey
	heldClaim  *corev1.ConfigMap
}

var claimRigListener = gatewayv1.Listener{
	Name:     "custom",
	Port:     DefaultHTTPPort,
	Protocol: gatewayv1.HTTPProtocolType,
	Hostname: ptr.To(gatewayv1.Hostname("example.com")),
}

func newClaimRig(t *testing.T, listeners func([]gatewayv1.Listener) []gatewayv1.Listener) *claimRig {
	t.Helper()
	testScheme := claimsTestScheme(t)
	require.NoError(t, discoveryv1.AddToScheme(testScheme))
	require.NoError(t, cmv1.AddToScheme(testScheme))

	testConfig := config.NetworkServicesOperator{Gateway: config.GatewayConfig{
		DownstreamGatewayClassName:            "test-suite",
		DownstreamHostnameAccountingNamespace: claimsNamespace,
		TargetDomain:                          "test-suite.com",
		IPFamilies:                            []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol},
	}}
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test", UID: uuid.NewUUID()}}
	gateway := newGateway(testConfig, namespace.Name, "test", func(g *gatewayv1.Gateway) {
		g.Spec.Listeners = listeners(g.Spec.Listeners)
	})
	domain := newDomain(namespace.Name, "example.com", func(d *networkingv1alpha.Domain) {
		apimeta.SetStatusCondition(&d.Status.Conditions, metav1.Condition{
			Type:   networkingv1alpha.DomainConditionVerified,
			Status: metav1.ConditionTrue,
		})
	})
	class := &gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: "test"},
	}
	heldClaim := claimFor("project-holder", "example.com", time.Now())
	for _, obj := range []client.Object{domain, class} {
		obj.SetUID(uuid.NewUUID())
		obj.SetCreationTimestamp(metav1.Now())
	}

	upstream := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(gateway, namespace, domain, class).
		WithStatusSubresource(gateway, domain).
		Build()
	downstream := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(heldClaim).
		WithStatusSubresource(&gatewayv1.Gateway{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				obj.SetUID(uuid.NewUUID())
				obj.SetCreationTimestamp(metav1.Now())
				return cl.Create(ctx, obj, opts...)
			},
		}).
		Build()

	return &claimRig{
		t:   t,
		ctx: context.Background(),
		reconciler: &GatewayReconciler{
			mgr:               &fakeMockManager{cl: upstream},
			Config:            testConfig,
			DownstreamCluster: &fakeCluster{cl: downstream},
		},
		upstream:   upstream,
		downstream: downstream,
		strategy:   downstreamclient.NewMappedNamespaceResourceStrategy("test", upstream, downstream),
		gateway:    client.ObjectKeyFromObject(gateway),
		heldClaim:  heldClaim,
	}
}

func (r *claimRig) storeCopy(listeners []gatewayv1.Listener, annotations map[string]string) {
	r.t.Helper()
	var upstreamGateway gatewayv1.Gateway
	require.NoError(r.t, r.upstream.Get(r.ctx, r.gateway, &upstreamGateway))
	meta, err := r.strategy.ObjectMetaFromUpstreamObject(r.ctx, &upstreamGateway)
	require.NoError(r.t, err)
	meta.Annotations = annotations
	require.NoError(r.t, r.downstream.Create(r.ctx, &gatewayv1.Gateway{
		ObjectMeta: meta,
		Spec:       gatewayv1.GatewaySpec{GatewayClassName: "test-suite", Listeners: listeners},
	}))
}

func (r *claimRig) reconcileOnce() *gatewayv1.Gateway {
	r.t.Helper()
	var current gatewayv1.Gateway
	require.NoError(r.t, r.upstream.Get(r.ctx, r.gateway, &current))
	r.reconciler.prepareUpstreamGateway(&current)
	result, downstreamGateway := r.reconciler.ensureDownstreamGateway(r.ctx, "test-suite", r.upstream, &current, r.strategy)
	require.NoError(r.t, result.Err)
	_, err := result.Complete(r.ctx)
	require.NoError(r.t, err)

	var stored gatewayv1.Gateway
	require.NoError(r.t, r.downstream.Get(r.ctx, client.ObjectKeyFromObject(downstreamGateway), &stored))
	return &stored
}

func TestEnsureDownstreamGateway_AwaitingMarkFollowsTheRefusal(t *testing.T) {
	rig := newClaimRig(t, func(defaults []gatewayv1.Listener) []gatewayv1.Listener {
		return append(defaults, claimRigListener)
	})

	assert.Equal(t, awaitingHostnameClaimValue, rig.reconcileOnce().Annotations[awaitingHostnameClaimAnnotation],
		"another gateway holds the hostname")

	require.NoError(t, rig.downstream.Delete(rig.ctx, rig.heldClaim))
	assert.NotContains(t, rig.reconcileOnce().Annotations, awaitingHostnameClaimAnnotation, "the hostname is free and now claimed")
}

func TestEnsureDownstreamGateway_KeepsAnnotationsItDoesNotOwn(t *testing.T) {
	rig := newClaimRig(t, func(defaults []gatewayv1.Listener) []gatewayv1.Listener {
		return append(defaults, claimRigListener)
	})
	reissued := reissuanceAnnotationKey("gw-cert")
	rig.storeCopy([]gatewayv1.Listener{claimRigListener}, map[string]string{reissued: "2"})

	annotations := rig.reconcileOnce().Annotations
	assert.Equal(t, "2", annotations[reissued], "the reissuance count another step keeps on the copy")
	assert.Equal(t, awaitingHostnameClaimValue, annotations[awaitingHostnameClaimAnnotation])
}

func TestEnsureDownstreamGateway_NoListenerLeftKeepsTheServingCopy(t *testing.T) {
	rig := newClaimRig(t, func([]gatewayv1.Listener) []gatewayv1.Listener {
		return []gatewayv1.Listener{claimRigListener}
	})
	serving := []gatewayv1.Listener{{
		Name:     "served",
		Port:     DefaultHTTPPort,
		Protocol: gatewayv1.HTTPProtocolType,
		Hostname: ptr.To(gatewayv1.Hostname("served.example.com")),
	}}
	reissued := reissuanceAnnotationKey("gw-cert")
	rig.storeCopy(serving, map[string]string{reissued: "2"})

	stored := rig.reconcileOnce()
	assert.Equal(t, serving, stored.Spec.Listeners)
	assert.Equal(t, awaitingHostnameClaimValue, stored.Annotations[awaitingHostnameClaimAnnotation])
	assert.Equal(t, "2", stored.Annotations[reissued])
}

func TestReleasedHostnameClaimReconcilesAwaitingGateways(t *testing.T) {
	hubGateway := func(project, name string, awaiting bool) *gatewayv1.Gateway {
		gw := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns-" + project,
			Name:      name,
			Labels: map[string]string{
				downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-" + project,
				downstreamclient.UpstreamOwnerNamespaceLabel:   "default",
				downstreamclient.UpstreamOwnerNameLabel:        name,
			},
		}}
		if awaiting {
			markAwaitingHostnameClaim(gw, true)
		}
		return gw
	}
	request := func(project, name string) mcreconcile.Request {
		return mcreconcile.Request{
			Request:     ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: name}},
			ClusterName: multicluster.ClusterName(project),
		}
	}

	tests := []struct {
		name     string
		gateways []client.Object
		listErr  error
		want     []mcreconcile.Request
	}{
		{
			name:     "none awaiting",
			gateways: []client.Object{hubGateway("project-a", "serving", false)},
		},
		{
			name:     "one awaiting among others",
			gateways: []client.Object{hubGateway("project-a", "awaiting", true), hubGateway("project-a", "serving", false)},
			want:     []mcreconcile.Request{request("project-a", "awaiting")},
		},
		{
			name: "several awaiting, in different projects",
			gateways: []client.Object{
				hubGateway("project-a", "awaiting-a", true),
				hubGateway("project-c", "awaiting-c", true),
				hubGateway("project-c", "serving", false),
			},
			want: []mcreconcile.Request{request("project-a", "awaiting-a"), request("project-c", "awaiting-c")},
		},
		{
			name:     "the cache read fails",
			gateways: []client.Object{hubGateway("project-a", "awaiting", true)},
			listErr:  errors.New("cache not synced"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub := fake.NewClientBuilder().
				WithScheme(claimsTestScheme(t)).
				WithIndex(&gatewayv1.Gateway{}, awaitingHostnameClaimIndex, awaitingHostnameClaimIndexFunc).
				WithObjects(tt.gateways...).
				WithInterceptorFuncs(interceptor.Funcs{
					List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
						if tt.listErr != nil {
							return tt.listErr
						}
						return cl.List(ctx, list, opts...)
					},
				}).
				Build()

			h := (&GatewayReconciler{}).listGatewaysAwaitingHostnameClaim("", &fakeCluster{cl: hub})
			queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[mcreconcile.Request]())
			t.Cleanup(queue.ShutDown)

			released := claimFor("project-holder", "example.com", time.Now())
			h.Delete(context.Background(), event.TypedDeleteEvent[*corev1.ConfigMap]{Object: released}, queue)

			var got []mcreconcile.Request
			for queue.Len() > 0 {
				req, _ := queue.Get()
				queue.Done(req)
				got = append(got, req)
			}
			assert.ElementsMatch(t, tt.want, got)
		})
	}
}

func TestHostnameClaimReleasePredicatePassesOnlyClaimDeletions(t *testing.T) {
	released := hostnameClaimReleasePredicate(claimsNamespace)
	claim := claimFor("project-holder", "example.com", time.Now())
	elsewhere := claimFor("project-holder", "example.com", time.Now())
	elsewhere.Namespace = "other"
	ownerless := claimFor("project-holder", "example.com", time.Now())
	delete(ownerless.Data, jsonKeyOwner)

	assert.True(t, released.Delete(event.TypedDeleteEvent[*corev1.ConfigMap]{Object: claim}), "a claim deleted")
	assert.False(t, released.Delete(event.TypedDeleteEvent[*corev1.ConfigMap]{Object: elsewhere}), "a ConfigMap outside the claims namespace")
	assert.False(t, released.Delete(event.TypedDeleteEvent[*corev1.ConfigMap]{Object: ownerless}), "a ConfigMap that claims nothing")
	assert.False(t, released.Create(event.TypedCreateEvent[*corev1.ConfigMap]{Object: claim}), "a claim made")
	assert.False(t, released.Update(event.TypedUpdateEvent[*corev1.ConfigMap]{ObjectOld: claim, ObjectNew: claim}), "a claim changed")
	assert.False(t, released.Generic(event.TypedGenericEvent[*corev1.ConfigMap]{Object: claim}))
}
