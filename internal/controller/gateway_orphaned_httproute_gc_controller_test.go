package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
)

func TestOrphanedDownstreamHTTPRouteGC(t *testing.T) {
	testScheme := runtime.NewScheme()
	assert.NoError(t, scheme.AddToScheme(testScheme))
	assert.NoError(t, gatewayv1.Install(testScheme))

	const upstreamCluster = "test-cluster"
	const upstreamNamespace = "test-ns"
	const upstreamHTTPRouteName = "test-route"
	const gatewayName = "test-gateway"

	clusterLabel := fmt.Sprintf("cluster-%s", upstreamCluster)
	downstreamNamespace := fmt.Sprintf("ns-%s", uuid.NewUUID())

	makeDownstreamRoute := func(age time.Duration) *gatewayv1.HTTPRoute {
		return &gatewayv1.HTTPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:         downstreamNamespace,
				Name:              upstreamHTTPRouteName,
				CreationTimestamp: metav1.NewTime(time.Now().Add(-age)),
				Labels: map[string]string{
					downstreamclient.UpstreamOwnerClusterNameLabel: clusterLabel,
					downstreamclient.UpstreamOwnerNamespaceLabel:   upstreamNamespace,
					downstreamclient.UpstreamOwnerNameLabel:        upstreamHTTPRouteName,
				},
			},
			Spec: gatewayv1.HTTPRouteSpec{
				CommonRouteSpec: gatewayv1.CommonRouteSpec{
					ParentRefs: []gatewayv1.ParentReference{
						{Name: gatewayName},
					},
				},
			},
		}
	}

	upstreamGateway := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: upstreamNamespace,
			Name:      gatewayName,
		},
	}

	makeRequest := func() mcreconcile.Request {
		return mcreconcile.Request{
			ClusterName: multicluster.ClusterName(upstreamCluster),
			Request: reconcile.Request{
				NamespacedName: client.ObjectKey{
					Namespace: upstreamNamespace,
					Name:      upstreamHTTPRouteName,
				},
			},
		}
	}

	t.Run("orphaned route deleted when upstream gateway missing", func(t *testing.T) {
		downstreamRoute := makeDownstreamRoute(10 * time.Minute)

		fakeUpstreamClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: upstreamNamespace}}).
			Build()

		fakeDownstreamClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(downstreamRoute).
			Build()

		reconciler := &OrphanedDownstreamHTTPRouteGCReconciler{
			mgr:               &fakeMockManager{cl: fakeUpstreamClient},
			DownstreamCluster: &fakeCluster{cl: fakeDownstreamClient},
			minOrphanAge:      1 * time.Millisecond,
		}

		_, err := reconciler.Reconcile(context.Background(), makeRequest())
		assert.NoError(t, err)

		err = fakeDownstreamClient.Get(context.Background(), client.ObjectKeyFromObject(downstreamRoute), &gatewayv1.HTTPRoute{})
		assert.True(t, apierrors.IsNotFound(err), "orphaned downstream HTTPRoute should have been deleted")
	})

	t.Run("healthy route not deleted when upstream gateway exists", func(t *testing.T) {
		downstreamRoute := makeDownstreamRoute(10 * time.Minute)

		fakeUpstreamClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(upstreamGateway).
			Build()

		fakeDownstreamClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(downstreamRoute).
			Build()

		reconciler := &OrphanedDownstreamHTTPRouteGCReconciler{
			mgr:               &fakeMockManager{cl: fakeUpstreamClient},
			DownstreamCluster: &fakeCluster{cl: fakeDownstreamClient},
			minOrphanAge:      1 * time.Millisecond,
		}

		_, err := reconciler.Reconcile(context.Background(), makeRequest())
		assert.NoError(t, err)

		err = fakeDownstreamClient.Get(context.Background(), client.ObjectKeyFromObject(downstreamRoute), &gatewayv1.HTTPRoute{})
		assert.NoError(t, err, "healthy downstream HTTPRoute should not have been deleted")
	})

	t.Run("young route not deleted; requeue returned", func(t *testing.T) {
		downstreamRoute := makeDownstreamRoute(1 * time.Second)

		fakeUpstreamClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			Build()

		fakeDownstreamClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(downstreamRoute).
			Build()

		reconciler := &OrphanedDownstreamHTTPRouteGCReconciler{
			mgr:               &fakeMockManager{cl: fakeUpstreamClient},
			DownstreamCluster: &fakeCluster{cl: fakeDownstreamClient},
			minOrphanAge:      orphanMinAge,
		}

		result, err := reconciler.Reconcile(context.Background(), makeRequest())
		assert.NoError(t, err)
		assert.Greater(t, result.RequeueAfter, time.Duration(0), "should requeue until min age is reached")

		err = fakeDownstreamClient.Get(context.Background(), client.ObjectKeyFromObject(downstreamRoute), &gatewayv1.HTTPRoute{})
		assert.NoError(t, err, "young downstream HTTPRoute should not have been deleted")
	})

	t.Run("route with multiple parentRefs kept if any gateway still exists", func(t *testing.T) {
		downstreamRoute := makeDownstreamRoute(10 * time.Minute)
		downstreamRoute.Spec.ParentRefs = []gatewayv1.ParentReference{
			{Name: "missing-gateway"},
			{Name: gatewayName},
		}

		fakeUpstreamClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(upstreamGateway).
			Build()

		fakeDownstreamClient := fake.NewClientBuilder().
			WithScheme(testScheme).
			WithObjects(downstreamRoute).
			Build()

		reconciler := &OrphanedDownstreamHTTPRouteGCReconciler{
			mgr:               &fakeMockManager{cl: fakeUpstreamClient},
			DownstreamCluster: &fakeCluster{cl: fakeDownstreamClient},
			minOrphanAge:      1 * time.Millisecond,
		}

		_, err := reconciler.Reconcile(context.Background(), makeRequest())
		assert.NoError(t, err)

		err = fakeDownstreamClient.Get(context.Background(), client.ObjectKeyFromObject(downstreamRoute), &gatewayv1.HTTPRoute{})
		assert.NoError(t, err, "route with one live parent should not be deleted")
	})

	t.Run("no downstream routes is a no-op", func(t *testing.T) {
		fakeUpstreamClient := fake.NewClientBuilder().WithScheme(testScheme).Build()
		fakeDownstreamClient := fake.NewClientBuilder().WithScheme(testScheme).Build()

		reconciler := &OrphanedDownstreamHTTPRouteGCReconciler{
			mgr:               &fakeMockManager{cl: fakeUpstreamClient},
			DownstreamCluster: &fakeCluster{cl: fakeDownstreamClient},
			minOrphanAge:      1 * time.Millisecond,
		}

		_, err := reconciler.Reconcile(context.Background(), makeRequest())
		assert.NoError(t, err)
	})
}
