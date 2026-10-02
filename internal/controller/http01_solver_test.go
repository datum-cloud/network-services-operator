// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestEnsureAndDeleteHTTP01SolverRoutes(t *testing.T) {
	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, gatewayv1.Install(testScheme))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(testScheme))

	gateway := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns-1", Name: "gw", UID: uuid.NewUUID()},
	}
	cl := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(gateway).Build()
	ctx := context.Background()

	solver := http01SolverRoute{
		name:   "gw-acme-abc",
		token:  "the-token",
		key:    "the-key",
		labels: map[string]string{tlsCertificateSolverLabel: "gw-https-0"},
	}

	require.NoError(t, ensureHTTP01SolverRoutes(ctx, cl, testScheme, gateway, gateway, solver))
	require.NoError(t, ensureHTTP01SolverRoutes(ctx, cl, testScheme, gateway, gateway, solver), "re-running is idempotent")

	key := client.ObjectKey{Namespace: "ns-1", Name: "gw-acme-abc"}

	var route gatewayv1.HTTPRoute
	require.NoError(t, cl.Get(ctx, key, &route))
	assert.Equal(t, labelValueTrue, route.Labels[http01SolverLabel])
	assert.Equal(t, "gw-https-0", route.Labels[tlsCertificateSolverLabel])
	require.Len(t, route.Spec.ParentRefs, 1)
	assert.Equal(t, gatewayv1.ObjectName("gw"), route.Spec.ParentRefs[0].Name)
	require.Len(t, route.Spec.Rules, 1)
	assert.Equal(t, "/.well-known/acme-challenge/the-token", ptr.Deref(route.Spec.Rules[0].Matches[0].Path.Value, ""))
	assert.Equal(t, gatewayv1.ObjectName("gw-acme-abc"), route.Spec.Rules[0].Filters[0].ExtensionRef.Name)
	owner := metav1.GetControllerOf(&route)
	require.NotNil(t, owner)
	assert.Equal(t, gateway.UID, owner.UID)

	var filter envoygatewayv1alpha1.HTTPRouteFilter
	require.NoError(t, cl.Get(ctx, key, &filter))
	assert.Equal(t, "the-key", ptr.Deref(filter.Spec.DirectResponse.Body.Inline, ""))
	assert.Equal(t, labelValueTrue, filter.Labels[http01SolverLabel])
	assert.Equal(t, gateway.UID, metav1.GetControllerOf(&filter).UID)

	require.NoError(t, deleteHTTP01SolverRoutes(ctx, cl, "ns-1", "gw-acme-abc"))
	assert.True(t, apierrors.IsNotFound(cl.Get(ctx, key, &gatewayv1.HTTPRoute{})))
	assert.True(t, apierrors.IsNotFound(cl.Get(ctx, key, &envoygatewayv1alpha1.HTTPRouteFilter{})))
	assert.NoError(t, deleteHTTP01SolverRoutes(ctx, cl, "ns-1", "gw-acme-abc"), "deleting again is a no-op")
}
