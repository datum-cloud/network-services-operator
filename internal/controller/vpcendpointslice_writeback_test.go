// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

// The values a real cell holds, taken from us-central-1-staging-lab so the
// shape under test is the shape galactic actually writes: one IPv6 endpoint,
// no ports, a tenant label, and the SID only in an annotation.
const (
	liveSliceName = "xcell2-dfw-dfw-0"
	liveSID       = "2607:ed40:8002:2:e001::"
	liveTenantID  = "Aty7F5rG-3zs"
	liveAddress   = "fd20:0:2::1:0:0"
)

// reachability stands a cell and the federation hub on two API servers. They
// are separate clusters in production and a cell namespace and its hub
// namespace carry the same name, so one server could not tell them apart.
type reachability struct {
	t   *testing.T
	ctx context.Context

	cell client.Client
	hub  client.Client

	namespace string
	writeBack *VPCEndpointSliceWriteBackReconciler
}

func newReachability(t *testing.T) *reachability {
	t.Helper()

	cellPlane, hubPlane := startPlanes(t)
	ctx := context.Background()

	namespace := "ns-" + sanitizeName(strings.ToLower(t.Name()))
	projectNamespace := "proj-" + sanitizeName(strings.ToLower(t.Name()))

	for _, cl := range []client.Client{cellPlane, hubPlane} {
		ns := &corev1.Namespace{}
		ns.Name = namespace
		ns.Labels = map[string]string{
			downstreamclient.UpstreamOwnerNamespaceLabel:   projectNamespace,
			downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-" + testProject,
		}
		require.NoError(t, cl.Create(ctx, ns))
	}

	return &reachability{
		t:         t,
		ctx:       ctx,
		cell:      cellPlane,
		hub:       hubPlane,
		namespace: namespace,
		writeBack: &VPCEndpointSliceWriteBackReconciler{
			Location:    config.LocationConfig{Name: testLocationName},
			HubCluster:  &hubFakeCluster{scheme: hubPlane.Scheme(), client: hubPlane},
			localReader: cellPlane,
		},
	}
}

// sliceOnCell writes the slice galactic-cni publishes for a VPC pod, in the
// shape it actually publishes it: owned by the Pod, ports unset.
func (r *reachability) sliceOnCell() *discoveryv1.EndpointSlice {
	r.t.Helper()

	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      liveSliceName,
			Namespace: r.namespace,
			Labels: map[string]string{
				VPCPodTenantIDLabel: liveTenantID,
			},
			Annotations: map[string]string{
				"galactic.datum.net/srv6-sid":  liveSID,
				"galactic.datum.net/tenant-id": liveTenantID,
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "v1",
				Kind:       "Pod",
				Name:       liveSliceName,
				UID:        "9d2c7d8b-6840-4024-b4c8-5573a0765667",
			}},
		},
		AddressType: discoveryv1.AddressTypeIPv6,
		Endpoints: []discoveryv1.Endpoint{{
			Addresses:  []string{liveAddress},
			Conditions: discoveryv1.EndpointConditions{Ready: ptr.To(true)},
			TargetRef: &corev1.ObjectReference{
				Kind:      "Pod",
				Name:      liveSliceName,
				Namespace: r.namespace,
				UID:       "9d2c7d8b-6840-4024-b4c8-5573a0765667",
			},
		}},
	}
	require.NoError(r.t, r.cell.Create(r.ctx, slice))

	return slice
}

func (r *reachability) publish(name string) {
	r.t.Helper()
	require.NoError(r.t, r.writeBack.publish(r.ctx, r.cell, client.ObjectKey{
		Namespace: r.namespace, Name: name,
	}))
}

func (r *reachability) hubCopy() (*discoveryv1.EndpointSlice, bool) {
	r.t.Helper()

	var copied discoveryv1.EndpointSlice
	err := r.hub.Get(r.ctx, client.ObjectKey{
		Namespace: r.namespace,
		Name:      federatedEndpointSliceName(testLocationName, liveSliceName),
	}, &copied)
	if err != nil {
		return nil, false
	}
	return &copied, true
}

// What the edge reads must be what the cell wrote. galactic-vrf refuses a
// slice whose address type is not IPv6 and errors on one with no address, so
// anything this hop normalises it breaks without saying so.
func TestAVPCEndpointSliceCrossesToTheHubUnaltered(t *testing.T) {
	r := newReachability(t)
	source := r.sliceOnCell()

	r.publish(liveSliceName)

	copied, found := r.hubCopy()
	require.True(t, found, "the cell's slice reaches the hub")

	require.Equal(t, source.AddressType, copied.AddressType)
	require.Equal(t, source.Endpoints, copied.Endpoints)
	require.Equal(t, source.Ports, copied.Ports)
	require.Nil(t, copied.Ports, "a slice galactic wrote no ports onto keeps none")

	require.Equal(t, liveTenantID, copied.Labels[VPCPodTenantIDLabel],
		"the label galactic-vrf selects on rides along")

	require.Empty(t, copied.OwnerReferences,
		"the pod does not exist on the hub, and a copy naming it would be collected at once")
}

// Nothing in this operator reads the SID. It is carried, never parsed, and a
// refactor that starts normalising it has to fail here first.
func TestTheSIDSurvivesTheHopByteForByte(t *testing.T) {
	r := newReachability(t)
	r.sliceOnCell()

	r.publish(liveSliceName)

	copied, found := r.hubCopy()
	require.True(t, found)

	require.Equal(t, liveSID, copied.Annotations["galactic.datum.net/srv6-sid"])
	require.Equal(t, liveTenantID, copied.Annotations["galactic.datum.net/tenant-id"])
}

// A slice whose SID has not appeared yet is still published. galactic only
// writes the annotation once the pod's node has a locator, and withholding the
// copy until then would hold up every other thing the edge does with it.
func TestASliceWithNoSIDYetIsStillPublished(t *testing.T) {
	r := newReachability(t)
	source := r.sliceOnCell()

	delete(source.Annotations, "galactic.datum.net/srv6-sid")
	require.NoError(t, r.cell.Update(r.ctx, source))

	r.publish(liveSliceName)

	copied, found := r.hubCopy()
	require.True(t, found, "the SID is not this hop's business to wait for")
	require.NotContains(t, copied.Annotations, "galactic.datum.net/srv6-sid")
}

// The copy cannot take the name the cell's slice carries. This cell is also a
// gateway cluster, so the propagation policy hands the copy straight back into
// the namespace the original sits in.
func TestTheCopyDoesNotLandOnTheCellsOwnSlice(t *testing.T) {
	r := newReachability(t)
	r.sliceOnCell()

	r.publish(liveSliceName)

	copied, found := r.hubCopy()
	require.True(t, found)
	require.NotEqual(t, liveSliceName, copied.Name,
		"sharing the name would hand galactic's object to federation")
	require.Equal(t, liveSliceName, copied.Annotations[VPCEndpointSliceSourceNameAnnotation])
}

// A copy that came back down from the hub is not a source. Publishing it again
// would loop it through the hub for as long as the pod lived.
func TestACopyIsNeverPublishedAgain(t *testing.T) {
	r := newReachability(t)

	returned := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      federatedEndpointSliceName(testLocationName, liveSliceName),
			Namespace: r.namespace,
			Labels: map[string]string{
				VPCPodTenantIDLabel:             liveTenantID,
				VPCEndpointSliceProjectionLabel: "true",
			},
			Annotations: map[string]string{
				VPCEndpointSliceSourceNameAnnotation: liveSliceName,
			},
		},
		AddressType: discoveryv1.AddressTypeIPv6,
		Endpoints: []discoveryv1.Endpoint{{
			Addresses: []string{liveAddress},
		}},
	}
	require.NoError(t, r.cell.Create(r.ctx, returned))

	r.publish(returned.Name)

	var onward discoveryv1.EndpointSlice
	err := r.hub.Get(r.ctx, client.ObjectKey{
		Namespace: r.namespace,
		Name:      federatedEndpointSliceName(testLocationName, returned.Name),
	}, &onward)
	require.Error(t, err, "a copy is not republished under a second layer of naming")
}

// An empty slice is never published, and it never withdraws one already up. A
// slice with no endpoint is a partial write, not a pod that went.
func TestAnEmptySliceNeitherPublishesNorWithdraws(t *testing.T) {
	r := newReachability(t)
	source := r.sliceOnCell()

	r.publish(liveSliceName)
	_, found := r.hubCopy()
	require.True(t, found)

	source.Endpoints = nil
	require.NoError(t, r.cell.Update(r.ctx, source))

	r.publish(liveSliceName)

	copied, found := r.hubCopy()
	require.True(t, found, "reachability is frozen, not withdrawn")
	require.Len(t, copied.Endpoints, 1, "the last thing the cell actually said stands")
	require.Equal(t, liveAddress, copied.Endpoints[0].Addresses[0])
}

// A deletion the cell saw is a deletion. galactic removes the slice on CNI DEL
// and the pod owner reference is its backstop.
func TestADeletedSliceTakesItsCopyWithIt(t *testing.T) {
	r := newReachability(t)
	source := r.sliceOnCell()

	r.publish(liveSliceName)
	_, found := r.hubCopy()
	require.True(t, found)

	require.NoError(t, r.cell.Delete(r.ctx, source))
	r.publish(liveSliceName)

	_, found = r.hubCopy()
	require.False(t, found)
}

// The sweep is what closes the gap a missed deletion opens: the cell
// unreachable when the pod went, or the process restarting across the event.
func TestTheSweepCollectsACopyWithNoSliceBehindIt(t *testing.T) {
	r := newReachability(t)
	source := r.sliceOnCell()

	r.publish(liveSliceName)

	// Nothing reconciles the slice. There is no second event to come back to.
	require.NoError(t, r.cell.Delete(r.ctx, source))

	_, found := r.hubCopy()
	require.True(t, found, "the copy outlives the slice until something notices")

	require.NoError(t, r.writeBack.sweep(r.ctx))

	_, found = r.hubCopy()
	require.False(t, found)
}

// A sweep that can still see the slice leaves the copy alone.
func TestTheSweepLeavesALiveSliceAlone(t *testing.T) {
	r := newReachability(t)
	r.sliceOnCell()

	r.publish(liveSliceName)
	require.NoError(t, r.writeBack.sweep(r.ctx))

	_, found := r.hubCopy()
	require.True(t, found)
}

// A cell only ever collects what it published. Another location's copies sit
// in the same hub namespaces and are none of its business — which is also what
// keeps a cell that has gone quiet from having its reachability withdrawn by
// a cell that is still running.
func TestTheSweepLeavesAnotherLocationsCopyAlone(t *testing.T) {
	r := newReachability(t)

	elsewhere := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      federatedEndpointSliceName("us-east-1", "xcell2-iad-iad-0"),
			Namespace: r.namespace,
			Labels: map[string]string{
				VPCPodTenantIDLabel:                             "R00NpQ65-guc",
				VPCEndpointSliceProjectionLabel:                 "true",
				networkingv1alpha.NetworkInterfaceLocationLabel: "us-east-1",
			},
			Annotations: map[string]string{
				VPCEndpointSliceSourceNameAnnotation: "xcell2-iad-iad-0",
			},
		},
		AddressType: discoveryv1.AddressTypeIPv6,
		Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{"fd20:0:2:1:0:1::"}}},
	}
	require.NoError(t, r.hub.Create(r.ctx, elsewhere))

	require.NoError(t, r.writeBack.sweep(r.ctx))

	var still discoveryv1.EndpointSlice
	require.NoError(t, r.hub.Get(r.ctx, client.ObjectKeyFromObject(elsewhere), &still))
}

// A slice that loses the tenant label is no longer one of these, and its copy
// goes with it. Two cells holding a pod of the same name must not collide on
// the hub, which is what the location in the copy's name is for.
func TestTwoLocationsDoNotCollideOnOneHubName(t *testing.T) {
	require.NotEqual(t,
		federatedEndpointSliceName("us-central-1", liveSliceName),
		federatedEndpointSliceName("us-east-1", liveSliceName),
	)
}

func TestALongNameStaysAValidObjectName(t *testing.T) {
	name := federatedEndpointSliceName(strings.Repeat("a", 60), strings.Repeat("b", 250))
	require.LessOrEqual(t, len(name), maxObjectNameLength)
	require.NotEqual(t,
		federatedEndpointSliceName(strings.Repeat("a", 60), strings.Repeat("b", 251)),
		name,
	)
}

// A pod name is a DNS subdomain and runs well past the 63 characters a label
// value stops at, so the name the copy was published from cannot be carried as
// one. A slice named beyond that limit has to survive the hop.
func TestALongPodNameStillPublishes(t *testing.T) {
	r := newReachability(t)

	name := strings.Repeat("p", 120) + "-0"
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   r.namespace,
			Labels:      map[string]string{VPCPodTenantIDLabel: liveTenantID},
			Annotations: map[string]string{"galactic.datum.net/srv6-sid": liveSID},
		},
		AddressType: discoveryv1.AddressTypeIPv6,
		Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{liveAddress}}},
	}
	require.NoError(t, r.cell.Create(r.ctx, slice))

	r.publish(name)

	var copied discoveryv1.EndpointSlice
	require.NoError(t, r.hub.Get(r.ctx, client.ObjectKey{
		Namespace: r.namespace,
		Name:      federatedEndpointSliceName(testLocationName, name),
	}, &copied))
	require.Equal(t, name, copied.Annotations[VPCEndpointSliceSourceNameAnnotation])
	require.Equal(t, liveSID, copied.Annotations["galactic.datum.net/srv6-sid"])

	// And the sweep can still find its way back to the source.
	require.NoError(t, r.writeBack.sweep(r.ctx))
	require.NoError(t, r.hub.Get(r.ctx, client.ObjectKeyFromObject(&copied), &copied))
}
