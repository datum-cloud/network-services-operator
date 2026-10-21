// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func TestProjectBindingCarriesNetworkAddressing(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "create"
		if existing {
			name = "repair"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			require.NoError(t, networkingv1alpha.AddToScheme(scheme))
			network := &networkingv1alpha.Network{ObjectMeta: metav1.ObjectMeta{Name: "producer", Namespace: "default", UID: "network-id", Generation: 2}, Spec: networkingv1alpha.NetworkSpec{IPFamilies: []networkingv1alpha.IPFamily{networkingv1alpha.IPv6Protocol}, MTU: 1440}}
			binding := networkBindingForTest("default", "presence", "producer", "us-central-1")
			objects := []client.Object{&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}}, network, binding}
			if existing {
				objects = append(objects, &networkingv1alpha.NetworkContext{ObjectMeta: metav1.ObjectMeta{Name: networkContextNameForBinding(binding), Namespace: "default"}})
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&networkingv1alpha.NetworkBinding{}, &networkingv1alpha.NetworkContext{}).WithObjects(objects...).Build()
			r := &NetworkBindingReconciler{mgr: &replicatorFakeManager{clusters: map[multicluster.ClusterName]cluster.Cluster{"project": &replicatorFakeCluster{c: c, scheme: scheme}}}}
			req := mcreconcile.Request{ClusterName: "project", Request: ctrl.Request{NamespacedName: client.ObjectKeyFromObject(binding)}}
			_, err := r.Reconcile(ctx, req)
			require.NoError(t, err)
			var result networkingv1alpha.NetworkContext
			require.NoError(t, c.Get(ctx, client.ObjectKey{Namespace: "default", Name: networkContextNameForBinding(binding)}, &result))
			require.Equal(t, network.Spec.IPFamilies, result.Spec.IPFamilies)
			require.Equal(t, network.Spec.MTU, result.Spec.MTU)
			require.Equal(t, network.Generation, result.Spec.NetworkGeneration)
			require.Equal(t, string(network.UID), result.Labels[networkingv1alpha.NetworkUIDLabel])
			require.True(t, metav1.IsControlledBy(&result, network))
			network.Spec.MTU = 1400
			network.Generation++
			require.NoError(t, c.Update(ctx, network))
			requests := networkBindingRequestsForNetwork(ctx, c, "project", network)
			require.Len(t, requests, 1)
			_, err = r.Reconcile(ctx, requests[0])
			require.NoError(t, err)
			require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(&result), &result))
			require.EqualValues(t, 1400, result.Spec.MTU)
			require.Equal(t, network.Generation, result.Spec.NetworkGeneration)
		})
	}
}
