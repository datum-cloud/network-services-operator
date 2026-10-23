// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
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

func TestBackendHealthFullProbeSweepPublishesFreshConsecutiveSuccesses(t *testing.T) {
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
			AttachmentRef: &networkingv1alpha.NetworkInterfaceAttachmentRef{Name: "attach-1", UID: types.UID("attachment-1")},
			Conditions: []metav1.Condition{{Type: networkingv1alpha.NetworkInterfaceHolderAvailable,
				Status: metav1.ConditionTrue, Reason: "Ready", LastTransitionTime: metav1.Now()}},
		},
	}
	health := &networkingv1alpha.NetworkLoadBalancerBackendHealth{
		ObjectMeta: metav1.ObjectMeta{Namespace: "network-services-operator-system", Name: "health-64", Generation: 1},
		Spec: networkingv1alpha.NetworkLoadBalancerBackendHealthSpec{
			RouteRef: networkingv1alpha.NetworkLoadBalancerBackendHealthRouteRef{
				Namespace: "project", Name: "route-1", UID: types.UID("route-1"), Generation: 1,
			},
			ParentRef: networkingv1alpha.NetworkLoadBalancerBackendHealthParentRef{
				Name: "load-balancer", UID: types.UID("load-balancer-1"), Generation: 1, Listener: "game",
			},
			Location: "us-east-1", NetworkName: "default", FabricVPCID: "vpc-1",
		},
	}
	for i := 0; i < 64; i++ {
		health.Spec.Targets = append(health.Spec.Targets, networkingv1alpha.NetworkLoadBalancerBackendHealthTarget{
			Name: fmt.Sprintf("origin-%02d", i), SourceNamespace: "cell-project", SourceInterface: "nic-1", SourceUID: iface.UID,
			AttachmentName: "attach-1", AttachmentUID: types.UID("attachment-1"), Address: "fd20::1",
			ServingPort: 8443,
			Probe:       networkingv1alpha.NetworkLoadBalancerProbe{Protocol: networkingv1alpha.NetworkLoadBalancerProbeTCP, Port: 8443},
		})
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(iface, health).
		WithStatusSubresource(iface, health).Build()
	start := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(start.UnixNano())
	var dials, active, maximum atomic.Int32
	waves := make([]chan struct{}, 16)
	for i := range waves {
		waves[i] = make(chan struct{})
	}
	r := &NetworkLoadBalancerBackendHealthReconciler{
		Client: cl, Now: func() time.Time { return time.Unix(0, clock.Load()) },
		Prober: nlbhealth.Prober{DialContext: func(ctx context.Context, _, _, _ string) (net.Conn, error) {
			current := active.Add(1)
			defer active.Add(-1)
			for old := maximum.Load(); current > old; old = maximum.Load() {
				if maximum.CompareAndSwap(old, current) {
					break
				}
			}
			n := int(dials.Add(1))
			wave := waves[(n-1)/probesPerHealthObject]
			if n%probesPerHealthObject == 0 {
				clock.Add(int64(nlbhealth.ProbeTimeout))
				close(wave)
			}
			select {
			case <-wave:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			a, b := net.Pipe()
			_ = b.Close()
			return a, nil
		}},
	}
	key := client.ObjectKeyFromObject(health)
	for sweep := 0; sweep < 2; sweep++ {
		result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
		require.NoError(t, err)
		require.Equal(t, nlbhealth.ProbeInterval, result.RequeueAfter)
		require.NoError(t, cl.Get(context.Background(), key, health))
		require.Len(t, health.Status.Targets, 64)
		if sweep == 0 {
			publishedAt := time.Unix(0, clock.Load())
			for _, status := range health.Status.Targets {
				require.True(t, status.ValidUntil.After(publishedAt), "first-sweep result expired before publication: %s", status.Name)
			}
			require.Empty(t, nlbhealth.HealthyTargets(health, time.Unix(0, clock.Load())))
		} else {
			require.Len(t, nlbhealth.HealthyTargets(health, time.Unix(0, clock.Load())), 64)
			// The next 64-target sweep consumes eight two-second waves before
			// publishing. Every previously healthy target must remain eligible
			// immediately before that publication, including the first wave.
			beforeNextPublication := time.Unix(0, clock.Load()).Add(nlbhealth.ProbeInterval + nlbhealth.MaxSweepDuration - time.Nanosecond)
			require.Len(t, nlbhealth.HealthyTargets(health, beforeNextPublication), 64)
		}
		clock.Add(int64(nlbhealth.ProbeInterval))
	}
	require.EqualValues(t, 128, dials.Load())
	require.LessOrEqual(t, maximum.Load(), int32(probesPerHealthObject))
	// Results are never reusable after the freshness window, even after success.
	require.Empty(t, nlbhealth.HealthyTargets(health, time.Unix(0, clock.Load()).Add(nlbhealth.ResultLifetime)))
}

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
			AttachmentRef: &networkingv1alpha.NetworkInterfaceAttachmentRef{Name: "attach-1", UID: types.UID("attachment-1")},
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
				ServingPort: 8443,
				Probe:       networkingv1alpha.NetworkLoadBalancerProbe{Protocol: networkingv1alpha.NetworkLoadBalancerProbeTCP, Port: 8443},
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
			if err := b.Close(); err != nil {
				return nil, err
			}
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

	// Replacing an attachment under the same name must not reuse prior health.
	require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(iface), iface))
	iface.Status.AttachmentRef.UID = types.UID("attachment-2")
	require.NoError(t, cl.Status().Update(context.Background(), iface))
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	require.Equal(t, 2, dials)
	require.NoError(t, cl.Get(context.Background(), key, health))
	require.Empty(t, nlbhealth.HealthyTargets(health, now))
	require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(iface), iface))
	iface.Status.AttachmentRef.UID = ""
	require.NoError(t, cl.Status().Update(context.Background(), iface))
	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	require.Equal(t, 2, dials)
	require.NoError(t, cl.Get(context.Background(), key, health))
	require.Empty(t, nlbhealth.HealthyTargets(health, now))

	require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(iface), iface))
	iface.Status.Phase = networkingv1alpha.NetworkInterfacePhaseAvailable
	require.NoError(t, cl.Status().Update(context.Background(), iface))
	_, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	require.Equal(t, 2, dials)
	require.NoError(t, cl.Get(context.Background(), key, health))
	require.Empty(t, nlbhealth.HealthyTargets(health, now))

	noDialer := &NetworkLoadBalancerBackendHealthReconciler{Client: cl}
	_, err = noDialer.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	require.NoError(t, cl.Get(context.Background(), key, health))
	require.Equal(t, "NoDataPlaneDialer", apimeta.FindStatusCondition(health.Status.Conditions, "ProbesEvaluated").Reason)
}

func TestBackendHealthProbeDeadlineStartsAfterGlobalSlotAcquisition(t *testing.T) {
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
			AttachmentRef: &networkingv1alpha.NetworkInterfaceAttachmentRef{Name: "attach-1", UID: types.UID("attachment-1")},
			Conditions: []metav1.Condition{{Type: networkingv1alpha.NetworkInterfaceHolderAvailable,
				Status: metav1.ConditionTrue, Reason: "Ready", LastTransitionTime: metav1.Now()}},
		},
	}
	health := &networkingv1alpha.NetworkLoadBalancerBackendHealth{
		ObjectMeta: metav1.ObjectMeta{Namespace: "network-services-operator-system", Name: "health-1", Generation: 1},
		Spec: networkingv1alpha.NetworkLoadBalancerBackendHealthSpec{
			RouteRef: networkingv1alpha.NetworkLoadBalancerBackendHealthRouteRef{
				Namespace: "project", Name: "route-1", UID: types.UID("route-1"), Generation: 1,
			},
			ParentRef: networkingv1alpha.NetworkLoadBalancerBackendHealthParentRef{
				Name: "load-balancer", UID: types.UID("load-balancer-1"), Generation: 1, Listener: "game",
			},
			Location: "us-east-1", NetworkName: "default", FabricVPCID: "vpc-1",
			Targets: []networkingv1alpha.NetworkLoadBalancerBackendHealthTarget{{
				Name: "origin-1", SourceNamespace: "cell-project", SourceInterface: "nic-1", SourceUID: iface.UID,
				AttachmentName: "attach-1", AttachmentUID: types.UID("attachment-1"), Address: "fd20::1",
				ServingPort: 8443,
				Probe:       networkingv1alpha.NetworkLoadBalancerProbe{Protocol: networkingv1alpha.NetworkLoadBalancerProbeTCP, Port: 8443},
			}},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(iface, health).
		WithStatusSubresource(iface, health).Build()
	probed := make(chan struct{}, 1)
	r := &NetworkLoadBalancerBackendHealthReconciler{
		Client: cl,
		Prober: nlbhealth.Prober{DialContext: func(ctx context.Context, _, _, _ string) (net.Conn, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			probed <- struct{}{}
			a, b := net.Pipe()
			_ = b.Close()
			return a, nil
		}},
	}
	for range cap(cellProbeSlots) {
		cellProbeSlots <- struct{}{}
	}
	t.Cleanup(func() {
		for len(cellProbeSlots) > 0 {
			<-cellProbeSlots
		}
	})
	finished := make(chan error, 1)
	go func() {
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(health)})
		finished <- err
	}()
	select {
	case <-probed:
		t.Fatal("probe ran without acquiring a global slot")
	case err := <-finished:
		t.Fatalf("reconcile finished before a slot was released: %v", err)
	case <-time.After(nlbhealth.ProbeTimeout + 100*time.Millisecond):
	}
	<-cellProbeSlots
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("reconcile did not complete after a slot was released")
	}
	require.Len(t, probed, 1)
	require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(health), health))
	require.Equal(t, "Warming", health.Status.Targets[0].Reason)
}
