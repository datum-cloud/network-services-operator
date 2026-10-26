// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	cloudv1alpha1 "go.datum.net/galactic/api/cloud/v1alpha1"
	networkv1alpha1 "go.datum.net/galactic/api/network/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const nlbProgrammingTestNamespace = "ns-project"

func nlbProgrammingFixture(t *testing.T) (client.Client, *NetworkLoadBalancerCellProgrammingReconciler, *networkingv1alpha.NetworkLoadBalancerCellIntent) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))
	require.NoError(t, cloudv1alpha1.AddToScheme(scheme))
	require.NoError(t, networkv1alpha1.AddToScheme(scheme))

	isController := true
	iface := &networkingv1alpha.NetworkInterface{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: nlbProgrammingTestNamespace, Name: "web-0-eth0", UID: types.UID("iface-1"),
			Labels: map[string]string{networkingv1alpha.NetworkInterfaceLocationLabel: "us-central-1"},
		},
		Spec: networkingv1alpha.NetworkInterfaceSpec{
			Network:   networkingv1alpha.LocalNetworkRef{Name: "demo"},
			Addresses: []networkingv1alpha.NetworkInterfaceAddress{{Address: "fd20:0:30::1:0:0/96"}},
		},
		Status: networkingv1alpha.NetworkInterfaceStatus{
			Phase: networkingv1alpha.NetworkInterfacePhaseBound, VPC: "1Z",
			AttachmentRef: &networkingv1alpha.NetworkInterfaceAttachmentRef{Name: "web-0-eth0", UID: types.UID("attachment-1")},
			Conditions: []metav1.Condition{{Type: networkingv1alpha.NetworkInterfaceHolderAvailable,
				Status: metav1.ConditionTrue, Reason: "Ready", LastTransitionTime: metav1.Now()}},
		},
	}
	attachment := &cloudv1alpha1.VPCAttachment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: nlbProgrammingTestNamespace, Name: "web-0-eth0", UID: types.UID("attachment-1"), Generation: 2,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: networkingv1alpha.GroupVersion.String(), Kind: "NetworkInterface",
				Name: iface.Name, UID: iface.UID, Controller: &isController,
			}},
		},
		Spec: cloudv1alpha1.VPCAttachmentSpec{
			VPC:          cloudv1alpha1.VPCRef{Name: "demo-us-central-1"},
			InterfaceRef: &cloudv1alpha1.NetworkInterfaceRef{Name: iface.Name},
			Interface:    cloudv1alpha1.VPCAttachmentInterface{Addresses: []cloudv1alpha1.IPAddress{"fd20:0:30::1:0:0/96"}},
		},
		Status: cloudv1alpha1.VPCAttachmentStatus{
			ObservedGeneration: 2, VPC: "1Z", Node: "eris-giune",
			Conditions: []metav1.Condition{{Type: cloudv1alpha1.ConditionTypeProgrammed,
				Status: metav1.ConditionTrue, Reason: "Programmed", LastTransitionTime: metav1.Now()}},
		},
	}
	intent := &networkingv1alpha.NetworkLoadBalancerCellIntent{
		ObjectMeta: metav1.ObjectMeta{Namespace: nlbProgrammingTestNamespace, Name: "nlb-0123456789abcdef0123456789abcdef"},
		Spec: networkingv1alpha.NetworkLoadBalancerCellIntentSpec{
			Location: "us-central-1", NetworkName: "demo",
			VIPFamily: networkingv1alpha.IPv6Protocol, VIP: "2607:ed40:63::2",
			Protocol: networkingv1alpha.NetworkLoadBalancerProtocolTCP, ListenerPort: 80,
			State: networkingv1alpha.NetworkLoadBalancerCellIntentValidate,
			Backends: []networkingv1alpha.NetworkLoadBalancerCellIntentBackend{{
				SourceNamespace: nlbProgrammingTestNamespace, SourceName: iface.Name, SourceUID: string(iface.UID),
				Family: networkingv1alpha.IPv6Protocol, Address: "fd20:0:30::1:0:0", Port: 8080,
			}},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(iface, attachment, intent).Build()
	r := &NetworkLoadBalancerCellProgrammingReconciler{
		Client: cl, GatewayNamespace: "galactic-system",
		resolveLocation: func(context.Context) (string, error) { return "us-central-1", nil },
	}
	return cl, r, intent
}

func reconcileNLBProgramming(t *testing.T, r *NetworkLoadBalancerCellProgrammingReconciler, intent *networkingv1alpha.NetworkLoadBalancerCellIntent) {
	t.Helper()
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(intent)})
	require.NoError(t, err)
}

func TestNLBCellProgrammingWritesGatewayServiceForVerifiedBackends(t *testing.T) {
	cl, r, intent := nlbProgrammingFixture(t)
	reconcileNLBProgramming(t, r, intent)

	var service networkv1alpha1.NetworkService
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: "galactic-system", Name: intent.Name}, &service))
	require.Equal(t, "1Z", service.Spec.VPCRef)
	require.Equal(t, []string{"2607:ed40:63::2"}, service.Spec.VIPAddresses)
	require.Equal(t, networkv1alpha1.NetworkServiceProtocolTCP, service.Spec.Protocol)
	require.EqualValues(t, 80, service.Spec.Port)
	require.EqualValues(t, 8080, service.Spec.BackendPort)
	label := networkLoadBalancerBackendLabel(intent.Name)
	require.Equal(t, map[string]string{label: nlbProgrammingBackendLabelValue}, service.Spec.BackendSelector.MatchLabels)

	var attachment cloudv1alpha1.VPCAttachment
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: nlbProgrammingTestNamespace, Name: "web-0-eth0"}, &attachment))
	require.Equal(t, nlbProgrammingBackendLabelValue, attachment.Labels[label])
}

func TestNLBCellProgrammingWithdrawsWhenBackendIdentityChanges(t *testing.T) {
	cl, r, intent := nlbProgrammingFixture(t)
	reconcileNLBProgramming(t, r, intent)

	require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(intent), intent))
	intent.Spec.Backends[0].SourceUID = "replaced-interface"
	require.NoError(t, cl.Update(context.Background(), intent))
	reconcileNLBProgramming(t, r, intent)

	err := cl.Get(context.Background(), client.ObjectKey{Namespace: "galactic-system", Name: intent.Name}, &networkv1alpha1.NetworkService{})
	require.True(t, apierrors.IsNotFound(err))
	var attachment cloudv1alpha1.VPCAttachment
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: nlbProgrammingTestNamespace, Name: "web-0-eth0"}, &attachment))
	require.NotContains(t, attachment.Labels, networkLoadBalancerBackendLabel(intent.Name))
}

func TestNLBCellProgrammingIgnoresOtherLocations(t *testing.T) {
	cl, r, intent := nlbProgrammingFixture(t)
	r.resolveLocation = func(context.Context) (string, error) { return "us-east-1", nil }
	reconcileNLBProgramming(t, r, intent)

	err := cl.Get(context.Background(), client.ObjectKey{Namespace: "galactic-system", Name: intent.Name}, &networkv1alpha1.NetworkService{})
	require.True(t, apierrors.IsNotFound(err))
}

func TestNLBCellProgrammingWithdrawsDeletedIntent(t *testing.T) {
	cl, r, intent := nlbProgrammingFixture(t)
	reconcileNLBProgramming(t, r, intent)
	require.NoError(t, cl.Delete(context.Background(), intent))
	reconcileNLBProgramming(t, r, intent)

	err := cl.Get(context.Background(), client.ObjectKey{Namespace: "galactic-system", Name: intent.Name}, &networkv1alpha1.NetworkService{})
	require.True(t, apierrors.IsNotFound(err))
}

func TestNLBCellProgrammingRefusesBackendsOutsideTheIntentNamespace(t *testing.T) {
	cl, r, intent := nlbProgrammingFixture(t)
	require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(intent), intent))
	intent.Spec.Backends[0].SourceNamespace = "ns-other-project"
	require.NoError(t, cl.Update(context.Background(), intent))
	reconcileNLBProgramming(t, r, intent)

	err := cl.Get(context.Background(), client.ObjectKey{Namespace: "galactic-system", Name: intent.Name}, &networkv1alpha1.NetworkService{})
	require.True(t, apierrors.IsNotFound(err))
}
