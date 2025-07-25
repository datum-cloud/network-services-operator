package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
)

func TestHTTPRouteGC(t *testing.T) {

	testScheme := runtime.NewScheme()
	assert.NoError(t, scheme.AddToScheme(testScheme))
	assert.NoError(t, gatewayv1.Install(testScheme))
	assert.NoError(t, discoveryv1.AddToScheme(testScheme))

	upstreamNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
			UID:  uuid.NewUUID(),
		},
	}

	upstreamHTTPRouteUID := uuid.NewUUID()

	downstreamNamespaceName := fmt.Sprintf("ns-%s", upstreamNamespace.UID)

	downstreamEndpointSlice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: downstreamNamespaceName,
			Name:      fmt.Sprintf("route-%s-rule-%d-backendref-%d", upstreamHTTPRouteUID, 0, 0),
		},
	}

	upstreamHTTPRoute := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "test",
			Name:      "test",
			UID:       upstreamHTTPRouteUID,
			Finalizers: []string{
				gatewayControllerGCFinalizer,
			},
			DeletionTimestamp: ptr.To(metav1.Now()),
		},
		Spec: gatewayv1.HTTPRouteSpec{
			Rules: []gatewayv1.HTTPRouteRule{
				{
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Group: ptr.To(gatewayv1.Group(discoveryv1.GroupName)),
									Kind:  ptr.To(gatewayv1.Kind("EndpointSlice")),
									Name:  gatewayv1.ObjectName(downstreamEndpointSlice.Name),
								},
							},
						},
					},
				},
			},
		},
	}

	fakeUpstreamClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(upstreamHTTPRoute, upstreamNamespace).
		Build()

	fakeDownstreamClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(downstreamEndpointSlice).
		Build()

	ctx := context.Background()

	mgr := &fakeMockManager{cl: fakeUpstreamClient}

	reconciler := &GatewayDownstreamGCReconciler{
		mgr:               mgr,
		DownstreamCluster: &fakeCluster{cl: fakeDownstreamClient},
	}

	_, err := reconciler.Reconcile(
		ctx,
		GVKRequest{
			GVK: gatewayv1.SchemeGroupVersion.WithKind("HTTPRoute"),
			Request: mcreconcile.Request{
				ClusterName: "test",
				Request: reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(upstreamHTTPRoute),
				},
			},
		},
	)
	assert.NoError(t, err, "reconcile failed")

	err = fakeUpstreamClient.Get(ctx, client.ObjectKeyFromObject(upstreamHTTPRoute), &gatewayv1.HTTPRoute{})
	assert.True(t, apierrors.IsNotFound(err))

	err = fakeDownstreamClient.Get(ctx, client.ObjectKeyFromObject(downstreamEndpointSlice), &discoveryv1.EndpointSlice{})
	assert.True(t, apierrors.IsNotFound(err))
}
