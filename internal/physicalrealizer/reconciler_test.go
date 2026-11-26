// SPDX-License-Identifier: AGPL-3.0-only

package physicalrealizer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func TestADeletedClaimWhoseNetworkIsStillCarriedKeepsItsFinalizer(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))

	data := physicalClaim("ns-customer-example", "server-data", "app", "")
	leaving := physicalClaim("ns-customer-example", "server-data-old", "app", "")
	leaving.DeletionTimestamp = ptrTo(metav1.Now())
	hold := testHold()
	hold.Spec.ConsumerRef = &networkingv1alpha.SwitchPortHoldConsumerRef{
		APIGroup: "resourcemanager.miloapis.com", Kind: "Project", Name: "customer-example",
	}
	hold.Spec.NetworkClaims = []networkingv1alpha.SwitchPortHoldClaimRef{
		{Namespace: data.Namespace, Name: data.Name, Network: "app"},
		{Namespace: leaving.Namespace, Name: leaving.Name, Network: "retired"},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(hold, data, leaving).
		WithStatusSubresource(&networkingv1alpha.NetworkInterfaceClaim{}, &networkingv1alpha.NetworkInterface{}).
		Build()
	sw := NewSimulatedSwitch()
	r := &Reconciler{Client: cl, APIReader: cl, Driver: sw}

	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hold)})
	require.NoError(t, err)
	require.Equal(t, []Membership{{Network: network("ns-customer-example", "app"),
		Isolation: networkingv1alpha.NetworkInterfaceIsolationShared}}, sw.Carried(Port{Switch: testSwitch, Name: testPort}))

	var stored networkingv1alpha.NetworkInterfaceClaim
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(leaving), &stored))
	require.True(t, controllerutil.ContainsFinalizer(&stored, networkingv1alpha.NetworkInterfaceClaimDetachFinalizer),
		"the port still carries the network the deleted claim names")
}
