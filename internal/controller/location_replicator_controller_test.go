// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

const testPropagationClusterName = "datum-platform"

type replicationScenario struct {
	t   *testing.T
	ctx context.Context

	hub        client.Client
	platform   client.Client
	reconciler *LocationReplicator

	locationName string
}

// newReplicationScenario gives the platform control plane and the hub separate
// clients, because a Location is cluster-scoped and the copy carries the same
// name as its source.
func newReplicationScenario(t *testing.T, source *networkingv1alpha.Location) *replicationScenario {
	t.Helper()
	hub, _ := startNetworkInterfaceEnv(t)

	platformScheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(platformScheme))
	require.NoError(t, networkingv1alpha.AddToScheme(platformScheme))

	builder := fake.NewClientBuilder().WithScheme(platformScheme)
	if source != nil {
		builder = builder.WithObjects(source)
	}
	platform := builder.Build()

	name := "loc-" + sanitizeName(strings.ToLower(t.Name()))
	if source != nil {
		name = source.Name
	}

	return &replicationScenario{
		t:            t,
		ctx:          context.Background(),
		hub:          hub,
		platform:     platform,
		locationName: name,
		reconciler: &LocationReplicator{
			PropagationClusterName: testPropagationClusterName,
			platform:               platform,
			hub:                    hub,
		},
	}
}

func readyLocation(t *testing.T, ready bool) *networkingv1alpha.Location {
	t.Helper()

	location := &networkingv1alpha.Location{}
	location.Name = "loc-" + sanitizeName(strings.ToLower(t.Name()))
	location.Spec.LocationClassName = "datum-managed"
	location.Spec.Topology = map[string]string{
		"topology.datum.net/city-code": "DFW",
	}
	location.Spec.Provider.GCP = &networkingv1alpha.GCPLocationProvider{
		ProjectID: "datum-dfw",
		Region:    "us-south1",
		Zone:      "us-south1-a",
	}
	location.Spec.Coordinates = &networkingv1alpha.Coordinates{
		Latitude:  "32.7767",
		Longitude: "-96.797",
	}

	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	location.Status.Conditions = []metav1.Condition{{
		Type:               networkingv1alpha.LocationReady,
		Status:             status,
		Reason:             "Test",
		Message:            "test",
		LastTransitionTime: metav1.Now(),
	}}

	return location
}

func (s *replicationScenario) reconcile() {
	s.t.Helper()
	_, err := s.reconciler.Reconcile(s.ctx, ctrl.Request{
		NamespacedName: client.ObjectKey{Name: s.locationName},
	})
	require.NoError(s.t, err)
}

func (s *replicationScenario) copy() (*networkingv1alpha.Location, bool) {
	s.t.Helper()
	var copied networkingv1alpha.Location
	err := s.hub.Get(s.ctx, client.ObjectKey{Name: s.locationName}, &copied)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	require.NoError(s.t, err)
	return &copied, true
}

func (s *replicationScenario) source() *networkingv1alpha.Location {
	s.t.Helper()
	var source networkingv1alpha.Location
	require.NoError(s.t, s.platform.Get(s.ctx, client.ObjectKey{Name: s.locationName}, &source))
	return &source
}

func TestLocationReplicatorCopiesAReadyLocation(t *testing.T) {
	s := newReplicationScenario(t, readyLocation(t, true))
	s.reconcile()

	copied, ok := s.copy()
	require.True(t, ok, "a ready location must reach the hub")

	require.Equal(t, "datum-managed", copied.Spec.LocationClassName)
	require.Equal(t, map[string]string{"topology.datum.net/city-code": "DFW"}, copied.Spec.Topology)
	require.NotNil(t, copied.Spec.Provider.GCP)
	require.Equal(t, "datum-dfw", copied.Spec.Provider.GCP.ProjectID)
	require.Equal(t, "us-south1", copied.Spec.Provider.GCP.Region)
	require.Equal(t, "us-south1-a", copied.Spec.Provider.GCP.Zone)
	require.NotNil(t, copied.Spec.Coordinates)
	require.Equal(t, "32.7767", copied.Spec.Coordinates.Latitude)
	require.Equal(t, "-96.797", copied.Spec.Coordinates.Longitude)

	require.Equal(t, testPropagationClusterName,
		copied.Labels[downstreamclient.UpstreamOwnerClusterNameLabel],
		"the copy carries the label the propagation policy selects on")
}

// Status does not survive propagation, so reconstructing it on the hub would
// state something no location reader can act on.
func TestLocationReplicatorDoesNotCopyStatus(t *testing.T) {
	s := newReplicationScenario(t, readyLocation(t, true))
	s.reconcile()

	copied, ok := s.copy()
	require.True(t, ok)
	require.Empty(t, copied.Status.Conditions)
}

func TestLocationReplicatorSkipsALocationThatIsNotReady(t *testing.T) {
	s := newReplicationScenario(t, readyLocation(t, false))
	s.reconcile()

	_, ok := s.copy()
	require.False(t, ok, "a location that is not ready must not be copied")
}

func TestLocationReplicatorRemovesALocationThatStopsBeingReady(t *testing.T) {
	s := newReplicationScenario(t, readyLocation(t, true))
	s.reconcile()
	require.True(t, mustExistLocation(s.copy()))

	source := s.source()
	source.Status.Conditions[0].Status = metav1.ConditionFalse
	require.NoError(t, s.platform.Update(s.ctx, source))

	s.reconcile()

	_, ok := s.copy()
	require.False(t, ok, "a location that stops being ready is removed")
}

func TestLocationReplicatorRemovesACopyOfADeletedLocation(t *testing.T) {
	s := newReplicationScenario(t, readyLocation(t, true))
	s.reconcile()
	require.True(t, mustExistLocation(s.copy()))

	require.NoError(t, s.platform.Delete(s.ctx, s.source()))
	s.reconcile()

	_, ok := s.copy()
	require.False(t, ok)
}

func TestLocationReplicatorConvergesOnAnEdit(t *testing.T) {
	s := newReplicationScenario(t, readyLocation(t, true))
	s.reconcile()

	source := s.source()
	source.Spec.Topology["topology.datum.net/city-code"] = "IAD"
	source.Spec.Coordinates = &networkingv1alpha.Coordinates{
		Latitude:  "38.9445",
		Longitude: "-77.4558",
	}
	source.Spec.Provider.GCP.Zone = "us-east4-a"
	require.NoError(t, s.platform.Update(s.ctx, source))

	s.reconcile()

	copied, ok := s.copy()
	require.True(t, ok)
	require.Equal(t, "IAD", copied.Spec.Topology["topology.datum.net/city-code"])
	require.Equal(t, "38.9445", copied.Spec.Coordinates.Latitude)
	require.Equal(t, "us-east4-a", copied.Spec.Provider.GCP.Zone)
}

func mustExistLocation(_ *networkingv1alpha.Location, ok bool) bool { return ok }
