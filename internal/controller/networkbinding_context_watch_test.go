// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func networkBindingForTest(namespace, name, network, location string) *networkingv1alpha.NetworkBinding {
	return &networkingv1alpha.NetworkBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: networkingv1alpha.NetworkBindingSpec{
			Network:  networkingv1alpha.NetworkRef{Name: network},
			Location: locationsv1alpha1.LocationReference{Name: location},
		},
	}
}

// A binding must be re-enqueued when the context it resolves to changes, which
// is what lets it notice the context being deleted out from under it.
func TestNetworkBindingRequestsForContext(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))

	const ns = "default"

	match := networkBindingForTest(ns, "matching", "ipv6-test", "us-central-1")
	otherNetwork := networkBindingForTest(ns, "other-network", "ipv4-test", "us-central-1")
	otherLocation := networkBindingForTest(ns, "other-location", "ipv6-test", "us-east-1")
	otherNamespace := networkBindingForTest("elsewhere", "other-namespace", "ipv6-test", "us-central-1")

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(match, otherNetwork, otherLocation, otherNamespace).
		Build()

	networkContext := &networkingv1alpha.NetworkContext{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "ipv6-test-us-central-1"},
	}

	requests := networkBindingRequestsForContext(context.Background(), c, "cluster-a", networkContext)

	require.Len(t, requests, 1, "only the binding resolving to this context should be enqueued")
	require.Equal(t, "matching", requests[0].Name)
	require.Equal(t, ns, requests[0].Namespace)
	require.Equal(t, "cluster-a", string(requests[0].ClusterName))
}

func TestNetworkBindingRequestsForContextIgnoresOtherTypes(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))

	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	requests := networkBindingRequestsForContext(
		context.Background(),
		c,
		"cluster-a",
		&networkingv1alpha.NetworkBinding{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "not-a-context"}},
	)

	require.Nil(t, requests)
}
