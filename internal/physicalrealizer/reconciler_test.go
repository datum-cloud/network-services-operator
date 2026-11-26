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

func fakeRealizer(t *testing.T, objects ...client.Object) (*Reconciler, client.Client, *SimulatedSwitch) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&networkingv1alpha.NetworkInterfaceClaim{}, &networkingv1alpha.NetworkInterface{}).
		Build()
	sw := NewSimulatedSwitch()
	return &Reconciler{Client: cl, APIReader: cl, Driver: sw}, cl, sw
}

func holdRecording(claims ...*networkingv1alpha.NetworkInterfaceClaim) *networkingv1alpha.SwitchPortHold {
	hold := testHold()
	hold.Spec.ConsumerRef = &networkingv1alpha.SwitchPortHoldConsumerRef{
		APIGroup: "resourcemanager.miloapis.com", Kind: "Project", Name: "customer-example",
	}
	for _, claim := range claims {
		hold.Spec.NetworkClaims = append(hold.Spec.NetworkClaims, networkingv1alpha.SwitchPortHoldClaimRef{
			Namespace: claim.Namespace, Name: claim.Name, Network: claim.Spec.Network.Name,
		})
	}
	return hold
}

var testPortKey = Port{Switch: testSwitch, Name: testPort}

func TestADeletedClaimWhoseNetworkIsStillCarriedKeepsItsFinalizer(t *testing.T) {
	ctx := context.Background()
	data := physicalClaim("ns-customer-example", "server-data", "app", "")
	leaving := physicalClaim("ns-customer-example", "server-data-old", "app", "")
	leaving.DeletionTimestamp = ptrTo(metav1.Now())
	hold := holdRecording(data)
	hold.Spec.NetworkClaims = append(hold.Spec.NetworkClaims, networkingv1alpha.SwitchPortHoldClaimRef{
		Namespace: leaving.Namespace, Name: leaving.Name, Network: "retired",
	})
	r, cl, sw := fakeRealizer(t, hold, data, leaving)

	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hold)})
	require.NoError(t, err)
	carried := sw.Carried(testPortKey)
	require.Len(t, carried, 1)
	require.Equal(t, network("ns-customer-example", "app"), carried[0].Network)

	var stored networkingv1alpha.NetworkInterfaceClaim
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(leaving), &stored))
	require.True(t, controllerutil.ContainsFinalizer(&stored, networkingv1alpha.NetworkInterfaceClaimDetachFinalizer),
		"the port still carries the network the deleted claim names")
}

func TestAHoldNotNamedForItsPortIsRefused(t *testing.T) {
	ctx := context.Background()
	data := physicalClaim("ns-customer-example", "server-data", "app", "")
	hold := holdRecording(data)
	hold.Name = networkingv1alpha.SwitchPortHoldName(testSwitch, "Ethernet1/99")
	r, _, sw := fakeRealizer(t, hold, data)
	stale := []Membership{{Network: network("ns-customer-example", "retired"), VLAN: 100}}
	require.NoError(t, sw.Apply(ctx, testPortKey, stale))
	before := len(sw.History())

	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hold)})
	require.NoError(t, err)
	require.Len(t, sw.History(), before, "no port is touched for a hold named for another port")
	require.Equal(t, stale, sw.Carried(testPortKey))

	_, err = r.Reconcile(ctx, SweepRequest)
	require.NoError(t, err)
	require.Empty(t, sw.Carried(testPortKey), "a misnamed hold protects no port from the sweep")
}

func TestTheSweepClearsOnlyPortsNoHoldNames(t *testing.T) {
	ctx := context.Background()
	data := physicalClaim("ns-customer-example", "server-data", "app", "")
	hold := holdRecording(data)
	r, _, sw := fakeRealizer(t, hold, data)
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hold)})
	require.NoError(t, err)
	carried := sw.Carried(testPortKey)
	require.Len(t, carried, 1)

	orphan := Port{Switch: testSwitch, Name: "Ethernet1/13"}
	require.NoError(t, sw.Apply(ctx, orphan, []Membership{{Network: network("ns-customer-example", "app"), VLAN: 100}}))

	result, err := r.Reconcile(ctx, SweepRequest)
	require.NoError(t, err)
	require.Equal(t, SweepInterval, result.RequeueAfter, "the sweep runs again on its own")
	require.Empty(t, sw.Carried(orphan))
	require.Equal(t, carried, sw.Carried(testPortKey), "a port a hold names is left alone")

	sw.FailReads(true)
	_, err = r.Reconcile(ctx, SweepRequest)
	require.ErrorIs(t, err, ErrSwitchUnreachable)
}

func TestAPortWhoseHoldIsGoneAtTheStartIsCleared(t *testing.T) {
	ctx := context.Background()
	r, _, sw := fakeRealizer(t)
	require.NoError(t, sw.Apply(ctx, testPortKey, []Membership{{Network: network("ns-customer-example", "app"), VLAN: 100}}))

	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{
		Name: networkingv1alpha.SwitchPortHoldName(testSwitch, testPort),
	}})
	require.NoError(t, err)
	require.Empty(t, sw.Carried(testPortKey))
}

// vanishingHold reads a hold once and deletes it right after, as an operator
// deleting it in the middle of a pass would.
type vanishingHold struct {
	client.Client
	read bool
}

func (c *vanishingHold) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := c.Client.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if hold, ok := obj.(*networkingv1alpha.SwitchPortHold); ok && !c.read {
		c.read = true
		return c.Delete(ctx, hold.DeepCopy())
	}
	return nil
}

func TestAPortWhoseHoldVanishesMidPassIsCleared(t *testing.T) {
	ctx := context.Background()
	data := physicalClaim("ns-customer-example", "server-data", "app", "")
	hold := holdRecording(data)
	r, cl, sw := fakeRealizer(t, hold, data)
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hold)})
	require.NoError(t, err)
	require.Len(t, sw.Carried(testPortKey), 1)

	reader := &vanishingHold{Client: cl}
	r.APIReader = reader
	_, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hold)})
	require.NoError(t, err)
	require.True(t, reader.read)
	require.Empty(t, sw.Carried(testPortKey))
}
