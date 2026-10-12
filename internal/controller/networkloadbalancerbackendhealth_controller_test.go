// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/nlbhealth"
)

func TestBackendHealthReconcileRequiresLiveSourceAndVPCDialer(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))
	iface := &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "cell-project", Name: "nic-1", UID: types.UID("interface-1"),
			Labels: map[string]string{networkingv1alpha.NetworkInterfaceLocationLabel: "us-east-1"},
		},
		Spec: networkingv1alpha.NetworkInterfaceSpec{
			Network:   networkingv1alpha.LocalNetworkRef{Name: "default"},
			Addresses: []networkingv1alpha.NetworkInterfaceAddress{{Address: "fd20::1/128"}},
		},
		Status: networkingv1alpha.NetworkInterfaceStatus{
			Phase: networkingv1alpha.NetworkInterfacePhaseBound, VPC: "vpc-1",
			AttachmentRef: &networkingv1alpha.NetworkInterfaceAttachmentRef{Name: "attach-1"},
			Conditions: []metav1.Condition{{Type: networkingv1alpha.NetworkInterfaceHolderAvailable,
				Status: metav1.ConditionTrue, Reason: "Ready", LastTransitionTime: metav1.Now()}},
		},
	}
	health := &networkingv1alpha.NetworkLoadBalancerBackendHealth{
		ObjectMeta: metav1.ObjectMeta{Namespace: "network-services-operator-system", Name: "route-1-us-east-1", Generation: 4},
		Spec: networkingv1alpha.NetworkLoadBalancerBackendHealthSpec{
			RouteRef:  networkingv1alpha.NetworkLoadBalancerBackendHealthRouteRef{Namespace: "project", Name: "route-1", UID: types.UID("route-1"), Generation: 2},
			ParentRef: networkingv1alpha.NetworkLoadBalancerBackendHealthParentRef{Name: "load-balancer", UID: types.UID("load-balancer-1"), Generation: 3, Listener: "game"},
			Location:  "us-east-1", NetworkName: "default", FabricVPCID: "vpc-1",
			Targets: []networkingv1alpha.NetworkLoadBalancerBackendHealthTarget{{
				Name: "origin-1", SourceNamespace: "cell-project", SourceInterface: "nic-1", SourceUID: iface.UID,
				AttachmentName: "attach-1", AttachmentUID: types.UID("attachment-1"), Address: "fd20::1",
				ServingPort: 8443, Weight: 1,
				Probe: networkingv1alpha.NetworkLoadBalancerProbe{Protocol: networkingv1alpha.NetworkLoadBalancerProbeTCP, Port: 8443},
			}},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(iface, health).
		WithStatusSubresource(iface, health).Build()
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	dials := 0
	r := &NetworkLoadBalancerBackendHealthReconciler{
		Client: cl, Now: func() time.Time { return now },
		Prober: nlbhealth.Prober{DialContext: func(_ context.Context, vpc, _, _ string) (net.Conn, error) {
			require.Equal(t, "vpc-1", vpc)
			dials++
			a, b := net.Pipe()
			b.Close()
			return a, nil
		}},
	}
	key := client.ObjectKeyFromObject(health)
	for i := 0; i < 2; i++ {
		result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
		require.NoError(t, err)
		require.Equal(t, nlbhealth.ProbeInterval, result.RequeueAfter)
		now = now.Add(nlbhealth.ProbeInterval)
	}
	require.Equal(t, 2, dials)
	require.NoError(t, cl.Get(context.Background(), key, health))
	require.Len(t, nlbhealth.HealthyTargets(health, now), 1)
	require.True(t, apimeta.IsStatusConditionTrue(health.Status.Conditions, "ProbesEvaluated"))

	require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(iface), iface))
	iface.Status.Phase = networkingv1alpha.NetworkInterfacePhaseAvailable
	require.NoError(t, cl.Status().Update(context.Background(), iface))
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	require.Equal(t, 2, dials)
	require.NoError(t, cl.Get(context.Background(), key, health))
	require.Empty(t, nlbhealth.HealthyTargets(health, now))

	noDialer := &NetworkLoadBalancerBackendHealthReconciler{Client: cl}
	require.ErrorContains(t, noDialer.SetupWithManager(nil), "VPC-aware")
}
