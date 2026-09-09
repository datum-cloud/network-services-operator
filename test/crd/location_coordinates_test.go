// SPDX-License-Identifier: AGPL-3.0-only

package crd

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func baseLocationSpec() networkingv1alpha.LocationSpec {
	return networkingv1alpha.LocationSpec{
		LocationClassName: "datum-managed",
		Topology: map[string]string{
			"topology.datum.net/city-code": "DFW",
		},
		Provider: networkingv1alpha.LocationProvider{
			GCP: &networkingv1alpha.GCPLocationProvider{
				ProjectID: "datum-cloud-poc-1",
				Region:    "us-south1",
				Zone:      "us-south1-a",
			},
		},
	}
}

// TestLocationCoordinatesOptional asserts a Location can still be created
// without coordinates, so the new field doesn't break existing callers.
func TestLocationCoordinatesOptional(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	loc := &networkingv1alpha.Location{
		ObjectMeta: metav1.ObjectMeta{Name: "no-coordinates"},
		Spec:       baseLocationSpec(),
	}
	require.NoError(t, cl.Create(ctx, loc))
	t.Cleanup(func() { _ = cl.Delete(ctx, loc) })

	var got networkingv1alpha.Location
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(loc), &got))
	assert.Nil(t, got.Spec.Coordinates)
}

// TestLocationCoordinatesValid asserts a Location with well-formed
// coordinates is accepted and round-trips.
func TestLocationCoordinatesValid(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	spec := baseLocationSpec()
	spec.Coordinates = &networkingv1alpha.Coordinates{
		Latitude:  "32.8968",
		Longitude: "-97.0380",
	}
	loc := &networkingv1alpha.Location{
		ObjectMeta: metav1.ObjectMeta{Name: "valid-coordinates"},
		Spec:       spec,
	}
	require.NoError(t, cl.Create(ctx, loc))
	t.Cleanup(func() { _ = cl.Delete(ctx, loc) })

	var got networkingv1alpha.Location
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(loc), &got))
	require.NotNil(t, got.Spec.Coordinates)
	assert.Equal(t, "32.8968", got.Spec.Coordinates.Latitude)
	assert.Equal(t, "-97.0380", got.Spec.Coordinates.Longitude)
}

// TestLocationRejectsOutOfRangeCoordinates asserts the CEL range rules reject
// a latitude/longitude outside valid Earth coordinates, even though the
// pattern alone would accept the string shape.
func TestLocationRejectsOutOfRangeCoordinates(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	spec := baseLocationSpec()
	spec.Coordinates = &networkingv1alpha.Coordinates{
		Latitude:  "95.0",
		Longitude: "-97.0380",
	}
	loc := &networkingv1alpha.Location{
		ObjectMeta: metav1.ObjectMeta{Name: "out-of-range-latitude"},
		Spec:       spec,
	}
	err := cl.Create(ctx, loc)
	require.Error(t, err, "latitude outside [-90, 90] must be rejected")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
}

// TestLocationRejectsMalformedCoordinate asserts the pattern rule rejects a
// non-numeric coordinate string.
func TestLocationRejectsMalformedCoordinate(t *testing.T) {
	cl := requireEnv(t)
	ctx := context.Background()

	spec := baseLocationSpec()
	spec.Coordinates = &networkingv1alpha.Coordinates{
		Latitude:  "not-a-number",
		Longitude: "-97.0380",
	}
	loc := &networkingv1alpha.Location{
		ObjectMeta: metav1.ObjectMeta{Name: "malformed-latitude"},
		Spec:       spec,
	}
	err := cl.Create(ctx, loc)
	require.Error(t, err, "non-numeric latitude must be rejected")
	assert.Truef(t, apierrors.IsInvalid(err), "expected an Invalid error, got %v", err)
}
