// SPDX-License-Identifier: AGPL-3.0-only

package publicudp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	api "go.datum.net/network-services-operator/api/v1alpha"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func fixture(t *testing.T) (*Reconciler, *api.NetworkService, *api.NetworkInterface) {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = api.AddToScheme(scheme)
	_ = core.AddToScheme(scheme)
	s := &api.NetworkService{ObjectMeta: metav1.ObjectMeta{Name: "masque", Namespace: "default", UID: "service-uid", Generation: 1}}
	s.Spec.PublicUDP = &api.NetworkServicePublicUDP{Location: "iad"}
	s.Spec.Ports = []api.NetworkServicePort{{Name: "masque", Port: 443, Protocol: api.NetworkServiceProtocolUDP}}
	s.Spec.NetworkInterfaces.Selector.MatchLabels = map[string]string{"app": "edge"}
	n := &api.NetworkInterface{ObjectMeta: metav1.ObjectMeta{Name: "edge-nic", Namespace: "tenant", UID: "nic-uid", Generation: 1, Labels: map[string]string{"app": "edge"}}}
	n.Spec.Network.Name = "edge"
	n.Spec.ClaimRef = &api.NetworkInterfaceClaimRef{Name: "claim"}
	n.Spec.ExternalAddresses = []api.NetworkInterfaceExternalAddress{{Class: "public-ipv4", Address: "192.0.2.44"}}
	n.Status.Phase = api.NetworkInterfacePhaseBound
	n.Status.VPC = "vpc"
	n.Status.AttachmentRef = &api.NetworkInterfaceAttachmentRef{APIGroup: "galactic.datumapis.com", Kind: "VPCAttachment", Name: "attachment"}
	for _, kind := range []string{api.NetworkInterfaceAllocated, api.NetworkInterfaceProgrammed, api.NetworkInterfaceHolderAvailable} {
		n.Status.Conditions = append(n.Status.Conditions, metav1.Condition{Type: kind, Status: metav1.ConditionTrue, ObservedGeneration: 1})
	}
	p := n.DeepCopy()
	p.Namespace = "default"
	p.Labels[api.NetworkInterfaceProjectionLabel] = "true"
	p.Labels[api.NetworkInterfaceSourceNamespaceLabel] = "tenant"
	p.Labels[api.NetworkInterfaceLocationLabel] = "iad"
	a := object("VPCAttachment", "tenant", "attachment")
	a.SetUID("attachment-uid")
	a.SetOwnerReferences([]metav1.OwnerReference{*metav1.NewControllerRef(n, api.GroupVersion.WithKind("NetworkInterface"))})
	a.Object["spec"] = map[string]interface{}{"interfaceRef": map[string]interface{}{"name": n.Name}}
	a.Object["status"] = map[string]interface{}{"vpc": "vpc", "node": "compute"}
	ns := &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant", Labels: map[string]string{"meta.datumapis.com/upstream-cluster-name": "cluster-project", "meta.datumapis.com/upstream-namespace": "default"}}}
	claim := &api.NetworkInterfaceClaim{ObjectMeta: metav1.ObjectMeta{Name: "claim", Namespace: "tenant"}}
	r := &Reconciler{Project: fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(s).WithObjects(s, p).Build(), Cell: fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns, n, claim).WithRuntimeObjects(a).Build(), ProjectName: "project", Location: "iad", FabricNamespace: "galactic-system"}
	return r, s, n
}
func TestResolveRejectsForeignTenantAndSpoofedAllocation(t *testing.T) {
	for _, test := range []string{"foreign-project", "foreign-network", "private-vip", "unhealthy", "foreign-attachment", "missing-claim", "releasing", "stale-generation", "multiple-interfaces"} {
		t.Run(test, func(t *testing.T) {
			r, s, n := fixture(t)
			ctx := context.Background()
			switch test {
			case "releasing":
				n.Annotations = map[string]string{api.NetworkInterfaceAddressReleaseStarted: "true"}
				require.NoError(t, r.Cell.Update(ctx, n))
			case "stale-generation":
				n.Generation++
				require.NoError(t, r.Cell.Update(ctx, n))
			case "multiple-interfaces":
				p := &api.NetworkInterface{}
				require.NoError(t, r.Project.Get(ctx, client.ObjectKey{Namespace: s.Namespace, Name: n.Name}, p))
				p.Name = "second"
				p.ResourceVersion = ""
				require.NoError(t, r.Project.Create(ctx, p))
			case "foreign-project":
				r.ProjectName = "other"
			case "foreign-network":
				n.Spec.Network.Name = "other"
				_ = r.Cell.Update(ctx, n)
			case "private-vip":
				n.Spec.ExternalAddresses[0].Address = "10.0.0.1"
				_ = r.Cell.Update(ctx, n)
			case "unhealthy":
				n.Status.Conditions = nil
				_ = r.Cell.Update(ctx, n)
			case "foreign-attachment":
				a := object("VPCAttachment", "tenant", "attachment")
				_ = r.Cell.Get(ctx, client.ObjectKeyFromObject(a), a)
				a.SetOwnerReferences(nil)
				_ = r.Cell.Update(ctx, a)
			case "missing-claim":
				_ = r.Cell.Delete(ctx, &api.NetworkInterfaceClaim{ObjectMeta: metav1.ObjectMeta{Name: "claim", Namespace: "tenant"}})
			}
			if _, _, _, err := r.resolve(ctx, s); err == nil {
				t.Fatal("unsafe publication accepted")
			}
		})
	}
}
func TestPublishAndWithdraw(t *testing.T) {
	r, s, _ := fixture(t)
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: s.Namespace, Name: s.Name}}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	rules := &unstructured.UnstructuredList{}
	rules.SetGroupVersionKind(object("NetworkServiceList", "", "").GroupVersionKind())
	if err := r.Cell.List(ctx, rules); err != nil {
		t.Fatal(err)
	}
	if len(rules.Items) != 1 {
		t.Fatal("no native service")
	}
	rule := rules.Items[0]
	protocol, _, _ := unstructured.NestedString(rule.Object, "spec", "protocol")
	if protocol != "udp" {
		t.Fatal(protocol)
	}
	if err := r.Project.Get(ctx, req.NamespacedName, s); err != nil {
		t.Fatal(err)
	}
	if err := r.Project.Delete(ctx, s); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Cell.List(ctx, rules); err != nil {
		t.Fatal(err)
	}
	if len(rules.Items) != 0 {
		t.Fatal("native service leaked")
	}
	var n api.NetworkInterface
	if err := r.Cell.Get(ctx, client.ObjectKey{Namespace: "tenant", Name: "edge-nic"}, &n); err != nil {
		t.Fatal(err)
	}
	if len(n.Finalizers) != 0 {
		t.Fatal("allocation hold leaked")
	}
}

func TestWithdrawalWaitsForBindingsAndNativeFinalizers(t *testing.T) {
	r, s, n := fixture(t)
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(s)}
	_, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	rules := &unstructured.UnstructuredList{}
	rules.SetGroupVersionKind(object("NetworkServiceList", "", "").GroupVersionKind())
	require.NoError(t, r.Cell.List(ctx, rules))
	require.Len(t, rules.Items, 1)
	rule := &rules.Items[0]
	rule.SetFinalizers([]string{"galactic.datumapis.com/drain"})
	require.NoError(t, r.Cell.Update(ctx, rule))
	binding := object("ServiceVIPBinding", r.FabricNamespace, "binding")
	binding.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "galactic.datumapis.com/v1alpha1", Kind: "NetworkService", Name: rule.GetName(), UID: rule.GetUID()}})
	require.NoError(t, r.Cell.Create(ctx, binding))
	require.NoError(t, r.Project.Get(ctx, req.NamespacedName, s))
	require.NoError(t, r.Project.Delete(ctx, s))
	_, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.NoError(t, r.Cell.Get(ctx, client.ObjectKeyFromObject(n), n))
	require.NotEmpty(t, n.Finalizers)
	require.NoError(t, r.Cell.Get(ctx, client.ObjectKeyFromObject(rule), rule))
	require.NotNil(t, rule.GetDeletionTimestamp())
	rule.SetFinalizers(nil)
	require.NoError(t, r.Cell.Update(ctx, rule))
	_, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.NoError(t, r.Cell.Get(ctx, client.ObjectKeyFromObject(n), n))
	require.NotEmpty(t, n.Finalizers)
	require.NoError(t, r.Cell.Delete(ctx, binding))
	_, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.NoError(t, r.Cell.Get(ctx, client.ObjectKeyFromObject(n), n))
	require.Empty(t, n.Finalizers)
}

func TestPublicationRequiresCurrentNativeReadiness(t *testing.T) {
	r, s, _ := fixture(t)
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(s)}
	_, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	rules := &unstructured.UnstructuredList{}
	rules.SetGroupVersionKind(object("NetworkServiceList", "", "").GroupVersionKind())
	require.NoError(t, r.Cell.List(ctx, rules))
	require.Len(t, rules.Items, 1)
	rule := &rules.Items[0]
	rule.SetGeneration(2)
	for _, generation := range []int64{1, 2} {
		rule.Object["status"] = map[string]interface{}{"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True", "observedGeneration": generation}}}
		require.NoError(t, r.Cell.Update(ctx, rule))
		_, err = r.Reconcile(ctx, req)
		require.NoError(t, err)
		require.NoError(t, r.Project.Get(ctx, req.NamespacedName, s))
		require.Equal(t, generation == 2, meta.IsStatusConditionTrue(s.Status.Conditions, Ready))
	}
}

func TestUnhealthyBackendWithdrawsPublication(t *testing.T) {
	r, s, n := fixture(t)
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(s)}
	_, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.NoError(t, r.Cell.Get(ctx, client.ObjectKeyFromObject(n), n))
	n.Status.Conditions = nil
	require.NoError(t, r.Cell.Update(ctx, n))
	for range 2 {
		_, err = r.Reconcile(ctx, req)
		require.NoError(t, err)
	}
	rules := &unstructured.UnstructuredList{}
	rules.SetGroupVersionKind(object("NetworkServiceList", "", "").GroupVersionKind())
	require.NoError(t, r.Cell.List(ctx, rules))
	require.Empty(t, rules.Items)
	require.NoError(t, r.Project.Get(ctx, req.NamespacedName, s))
	require.Empty(t, s.Status.PublicAddresses)
	require.True(t, meta.IsStatusConditionFalse(s.Status.Conditions, Ready))
}

func TestPortChangeWaitsForPreviousBindings(t *testing.T) {
	r, s, _ := fixture(t)
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(s)}
	_, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	rules := &unstructured.UnstructuredList{}
	rules.SetGroupVersionKind(object("NetworkServiceList", "", "").GroupVersionKind())
	require.NoError(t, r.Cell.List(ctx, rules))
	require.Len(t, rules.Items, 1)
	rule := &rules.Items[0]
	binding := object("ServiceVIPBinding", r.FabricNamespace, "old-binding")
	binding.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "galactic.datumapis.com/v1alpha1", Kind: "NetworkService", Name: rule.GetName(), UID: rule.GetUID()}})
	require.NoError(t, r.Cell.Create(ctx, binding))
	require.NoError(t, r.Project.Get(ctx, req.NamespacedName, s))
	s.Spec.Ports[0].Port = 444
	require.NoError(t, r.Project.Update(ctx, s))
	for range 2 {
		_, err = r.Reconcile(ctx, req)
		require.NoError(t, err)
	}
	require.NoError(t, r.Cell.List(ctx, rules))
	require.Empty(t, rules.Items)
	require.NoError(t, r.Cell.Delete(ctx, binding))
	_, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.NoError(t, r.Cell.List(ctx, rules))
	require.Len(t, rules.Items, 1)
	port, _, err := unstructured.NestedInt64(rules.Items[0].Object, "spec", "port")
	require.NoError(t, err)
	require.Equal(t, int64(444), port)
}

func TestConflictingNativeOwnerIsNotModified(t *testing.T) {
	r, s, _ := fixture(t)
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(s)}
	_, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	rules := &unstructured.UnstructuredList{}
	rules.SetGroupVersionKind(object("NetworkServiceList", "", "").GroupVersionKind())
	require.NoError(t, r.Cell.List(ctx, rules))
	require.Len(t, rules.Items, 1)
	rule := &rules.Items[0]
	rule.SetLabels(map[string]string{OwnerLabel: "another-owner"})
	require.NoError(t, r.Cell.Update(ctx, rule))
	before := rule.DeepCopy()
	_, err = r.Reconcile(ctx, req)
	require.ErrorContains(t, err, "ownership conflict")
	require.NoError(t, r.Cell.Get(ctx, client.ObjectKeyFromObject(rule), rule))
	require.Equal(t, before.Object, rule.Object)
}
