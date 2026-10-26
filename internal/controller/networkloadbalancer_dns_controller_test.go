// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const testNLBHostname = "dfw.crater-spare-b56jc.prism.example.net"

func dnsTestLoadBalancer(announced bool) *networkingv1alpha.NetworkLoadBalancer {
	address := allocatedCellIntentAddress("2001:db8::1", 1)
	if announced {
		address.Conditions = append(address.Conditions, metav1.Condition{
			Type: networkingv1alpha.NetworkLoadBalancerAnnounced, Status: metav1.ConditionTrue,
			Reason: "Announced", ObservedGeneration: 1, LastTransitionTime: metav1.Now(),
		})
	}
	return &networkingv1alpha.NetworkLoadBalancer{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "edge", UID: types.UID("lb-1"), Generation: 1},
		Status: networkingv1alpha.NetworkLoadBalancerStatus{Endpoints: []networkingv1alpha.NetworkLoadBalancerEndpointStatus{{
			RequestName: "regional", Location: "dfw", CanonicalHostname: testNLBHostname,
			Addresses: []networkingv1alpha.NetworkLoadBalancerAddressStatus{address},
		}}},
	}
}

func dnsTestClients(t *testing.T, lb *networkingv1alpha.NetworkLoadBalancer) (client.Client, client.Client) {
	t.Helper()
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test", UID: types.UID("project-ns")}}
	project := fake.NewClientBuilder().WithScheme(networkServiceTestScheme(t)).
		WithObjects(namespace, lb).WithStatusSubresource(lb).Build()
	hub := fake.NewClientBuilder().WithScheme(project.Scheme()).
		WithObjects(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-project-ns"}}).Build()
	return project, hub
}

func hubDNSEndpoint(t *testing.T, hub client.Client) (*unstructured.Unstructured, error) {
	t.Helper()
	var endpoint unstructured.Unstructured
	endpoint.SetGroupVersionKind(dnsEndpointGVK)
	err := hub.Get(context.Background(), client.ObjectKey{Namespace: "ns-project-ns", Name: "nlb-lb-1"}, &endpoint)
	return &endpoint, err
}

func TestNLBDNSPublishesAnnouncedAddress(t *testing.T) {
	ctx := context.Background()
	lb := dnsTestLoadBalancer(true)
	project, hub := dnsTestClients(t, lb)
	reconciler := &NetworkLoadBalancerDNSReconciler{HubCluster: &fakeCluster{cl: hub}, Enabled: true}

	_, err := reconciler.reconcileDNS(ctx, "project", project, client.ObjectKeyFromObject(lb))
	require.NoError(t, err)

	endpoint, err := hubDNSEndpoint(t, hub)
	require.NoError(t, err)
	records, _, err := unstructured.NestedSlice(endpoint.Object, "spec", "endpoints")
	require.NoError(t, err)
	require.Equal(t, []any{map[string]any{
		"dnsName": testNLBHostname, "recordType": "AAAA", "recordTTL": nlbDNSRecordTTL, "targets": []any{"2001:db8::1"},
	}}, records)
	require.Equal(t, "lb-1", endpoint.GetLabels()[networkLoadBalancerOwnerLabel])

	require.NoError(t, project.Get(ctx, client.ObjectKeyFromObject(lb), lb))
	require.Contains(t, lb.Finalizers, nlbDNSFinalizer)
	require.True(t, lb.Status.Endpoints[0].Addresses[0].DNSPublished)
}

func TestNLBDNSWaitsForAnnouncement(t *testing.T) {
	ctx := context.Background()
	lb := dnsTestLoadBalancer(false)
	project, hub := dnsTestClients(t, lb)
	reconciler := &NetworkLoadBalancerDNSReconciler{HubCluster: &fakeCluster{cl: hub}, Enabled: true}

	_, err := reconciler.reconcileDNS(ctx, "project", project, client.ObjectKeyFromObject(lb))
	require.NoError(t, err)

	_, err = hubDNSEndpoint(t, hub)
	require.True(t, apierrors.IsNotFound(err))
	require.NoError(t, project.Get(ctx, client.ObjectKeyFromObject(lb), lb))
	require.False(t, lb.Status.Endpoints[0].Addresses[0].DNSPublished)
}

func TestNLBDNSPublishOnAllocationLeavesReleaseUnblocked(t *testing.T) {
	ctx := context.Background()
	lb := dnsTestLoadBalancer(false)
	project, hub := dnsTestClients(t, lb)
	reconciler := &NetworkLoadBalancerDNSReconciler{HubCluster: &fakeCluster{cl: hub}, Enabled: true, PublishOnAllocation: true}

	_, err := reconciler.reconcileDNS(ctx, "project", project, client.ObjectKeyFromObject(lb))
	require.NoError(t, err)

	_, err = hubDNSEndpoint(t, hub)
	require.NoError(t, err)
	require.NoError(t, project.Get(ctx, client.ObjectKeyFromObject(lb), lb))
	require.False(t, lb.Status.Endpoints[0].Addresses[0].DNSPublished)
}

func TestNLBDNSWithdrawsWhenAnnouncementEnds(t *testing.T) {
	ctx := context.Background()
	lb := dnsTestLoadBalancer(true)
	project, hub := dnsTestClients(t, lb)
	reconciler := &NetworkLoadBalancerDNSReconciler{HubCluster: &fakeCluster{cl: hub}, Enabled: true}
	key := client.ObjectKeyFromObject(lb)

	_, err := reconciler.reconcileDNS(ctx, "project", project, key)
	require.NoError(t, err)

	require.NoError(t, project.Get(ctx, key, lb))
	lb.Status.Endpoints[0].Addresses[0].Conditions = lb.Status.Endpoints[0].Addresses[0].Conditions[:1]
	require.NoError(t, project.Status().Update(ctx, lb))
	_, err = reconciler.reconcileDNS(ctx, "project", project, key)
	require.NoError(t, err)

	_, err = hubDNSEndpoint(t, hub)
	require.True(t, apierrors.IsNotFound(err))
	require.NoError(t, project.Get(ctx, key, lb))
	require.False(t, lb.Status.Endpoints[0].Addresses[0].DNSPublished)
}

func TestNLBDNSWithdrawsBeforeDeletion(t *testing.T) {
	ctx := context.Background()
	lb := dnsTestLoadBalancer(true)
	project, hub := dnsTestClients(t, lb)
	reconciler := &NetworkLoadBalancerDNSReconciler{HubCluster: &fakeCluster{cl: hub}, Enabled: true}
	key := client.ObjectKeyFromObject(lb)

	_, err := reconciler.reconcileDNS(ctx, "project", project, key)
	require.NoError(t, err)
	require.NoError(t, project.Get(ctx, key, lb))
	lb.Finalizers = append(lb.Finalizers, "test.example/hold")
	require.NoError(t, project.Update(ctx, lb))
	require.NoError(t, project.Delete(ctx, lb))

	_, err = reconciler.reconcileDNS(ctx, "project", project, key)
	require.NoError(t, err)

	_, err = hubDNSEndpoint(t, hub)
	require.True(t, apierrors.IsNotFound(err))
	require.NoError(t, project.Get(ctx, key, lb))
	require.NotContains(t, lb.Finalizers, nlbDNSFinalizer)
	require.False(t, lb.Status.Endpoints[0].Addresses[0].DNSPublished)
	require.False(t, lb.DeletionTimestamp.IsZero())
	require.WithinDuration(t, time.Now(), lb.DeletionTimestamp.Time, time.Minute)
}

func TestNLBDNSWithdrawsAndReleasesWhenDisabled(t *testing.T) {
	ctx := context.Background()
	lb := dnsTestLoadBalancer(true)
	project, hub := dnsTestClients(t, lb)
	reconciler := &NetworkLoadBalancerDNSReconciler{HubCluster: &fakeCluster{cl: hub}, Enabled: true}
	key := client.ObjectKeyFromObject(lb)

	_, err := reconciler.reconcileDNS(ctx, "project", project, key)
	require.NoError(t, err)
	_, err = hubDNSEndpoint(t, hub)
	require.NoError(t, err)

	reconciler.Enabled = false
	_, err = reconciler.reconcileDNS(ctx, "project", project, key)
	require.NoError(t, err)

	_, err = hubDNSEndpoint(t, hub)
	require.True(t, apierrors.IsNotFound(err))
	require.NoError(t, project.Get(ctx, key, lb))
	require.NotContains(t, lb.Finalizers, nlbDNSFinalizer)
	require.False(t, lb.Status.Endpoints[0].Addresses[0].DNSPublished)

	_, err = reconciler.reconcileDNS(ctx, "project", project, key)
	require.NoError(t, err)
	require.NoError(t, project.Get(ctx, key, lb))
	require.NotContains(t, lb.Finalizers, nlbDNSFinalizer)
}
