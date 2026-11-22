// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	discoveryv1 "k8s.io/api/discovery/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestAFailingRouteLeavesTheGatewaysOtherRoutesConverging(t *testing.T) {
	rig := newMissingBackendRig(t,
		routeToEndpointSlice("wrong-port", "wrong-port"),
		routeToEndpointSlice("unnamed-port", "unnamed-port"),
		routeToEndpointSlice("served", "present"),
	)
	wrongPort := backendSlice("wrong-port")
	wrongPort.Ports[0].Port = ptr.To(int32(9090))
	unnamedPort := backendSlice("unnamed-port")
	unnamedPort.Ports[0].Name = nil
	for _, slice := range []*discoveryv1.EndpointSlice{wrongPort, unnamedPort, backendSlice("present")} {
		require.NoError(t, rig.upstream.Create(rig.ctx, slice))
	}

	reconcile := func() error {
		result := rig.reconciler.ensureDownstreamGatewayHTTPRoutes(rig.ctx, rig.upstream, rig.gateway, "test", rig.hubGateway, rig.strategy, nil, nil, nil)
		_, err := result.Complete(rig.ctx)
		return err
	}

	_ = reconcile()

	hubServed := rig.hubRoute("served")
	hubServed.Status.Parents = []gatewayv1.RouteParentStatus{{
		ParentRef:      gatewayv1.ParentReference{Name: gatewayv1.ObjectName(rig.hubGateway.Name)},
		ControllerName: "gateway.envoyproxy.io/gatewayclass-controller",
		Conditions: []metav1.Condition{{
			Type: string(gatewayv1.RouteConditionAccepted), Status: metav1.ConditionTrue,
			Reason: string(gatewayv1.RouteReasonAccepted), LastTransitionTime: metav1.Now(),
		}},
	}}
	require.NoError(t, rig.downstream.Status().Update(rig.ctx, hubServed))

	err := reconcile()

	assert.ErrorContains(t, err, "port not found in upstream endpointslice", "the wrong-port route's error")
	assert.ErrorContains(t, err, "no port name defined in upstream endpointslice", "the unnamed-port route's error")

	var served gatewayv1.HTTPRoute
	require.NoError(t, rig.upstream.Get(rig.ctx, client.ObjectKey{Namespace: rig.gateway.Namespace, Name: "served"}, &served))
	require.Len(t, served.Status.Parents, 1, "the served route's status is saved")
	accepted := apimeta.FindStatusCondition(served.Status.Parents[0].Conditions, string(gatewayv1.RouteConditionAccepted))
	require.NotNil(t, accepted, "the served route reports the edge's Accepted condition")
	assert.Equal(t, metav1.ConditionTrue, accepted.Status)

	var gateway gatewayv1.Gateway
	require.NoError(t, rig.upstream.Get(rig.ctx, client.ObjectKeyFromObject(rig.gateway), &gateway))
	require.NotEmpty(t, gateway.Status.Listeners, "the listener status is written")
	for _, listener := range gateway.Status.Listeners {
		assert.Equalf(t, int32(3), listener.AttachedRoutes, "listener %s counts the three routes", listener.Name)
	}
}
