// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

// Identity sources reported by nso_cell_location_identity_source.
const (
	LocationIdentitySourceDelivered  = "Delivered"
	LocationIdentitySourceConfigured = "Configured"
)

// Reasons a cell cannot name the location it serves.
const (
	LocationUnresolvedNoIdentity = "NoLocationIdentity"
	LocationUnresolvedAmbiguous  = "AmbiguousLocationIdentity"
)

// LocationIdentity is the location a cell serves and where that answer came
// from. Mismatch reports that a delivered copy and a configured location
// disagree; the delivered copy wins.
type LocationIdentity struct {
	Reference networkingv1alpha.LocationReference
	Source    string
	Mismatch  bool
}

// LocationUnresolved reports that a cell cannot yet name the location it
// serves. Callers report it on the objects that are stuck rather than failing.
type LocationUnresolved struct {
	Reason  string
	Message string
}

func (e *LocationUnresolved) Error() string { return e.Message }

// ResolveLocationIdentity returns the location a cell serves. A single
// delivered ServingLocation wins over the configured location. More than one
// delivered copy falls back to the configured location, and returns
// LocationUnresolved when there is none.
func ResolveLocationIdentity(
	ctx context.Context,
	reader client.Reader,
	configured config.LocationConfig,
) (LocationIdentity, error) {
	var delivered networkingv1alpha.ServingLocationList
	if err := reader.List(ctx, &delivered); err != nil {
		return LocationIdentity{}, fmt.Errorf("failed listing delivered serving locations: %w", err)
	}

	configuredIdentity := LocationIdentity{
		Reference: networkingv1alpha.LocationReference{
			Name: configured.Name,
		},
		Source: LocationIdentitySourceConfigured,
	}

	switch {
	case len(delivered.Items) == 1:
		identity := LocationIdentity{
			Reference: networkingv1alpha.LocationReference{Name: delivered.Items[0].Name},
			Source:    LocationIdentitySourceDelivered,
			Mismatch:  configured.Name != "" && configured.Name != delivered.Items[0].Name,
		}
		return identity, nil

	case len(delivered.Items) > 1:
		if configured.Name != "" {
			return configuredIdentity, nil
		}
		return LocationIdentity{}, &LocationUnresolved{
			Reason: LocationUnresolvedAmbiguous,
			Message: fmt.Sprintf(
				"%d ServingLocations have been delivered to this cell; exactly one cluster label %s is expected and no location is configured to fall back to",
				len(delivered.Items), networkingv1alpha.ServingLocationTopologyLabel),
		}

	case configured.Name != "":
		return configuredIdentity, nil

	default:
		return LocationIdentity{}, &LocationUnresolved{
			Reason: LocationUnresolvedNoIdentity,
			Message: fmt.Sprintf(
				"no ServingLocation has been delivered to this cell and location.name is not configured; label the cluster with %s or configure a location",
				networkingv1alpha.ServingLocationTopologyLabel),
		}
	}
}

func reportLocationIdentity(identity LocationIdentity, err error) {
	cellLocationIdentitySource.Reset()
	cellLocationIdentityMismatch.Reset()
	cellLocationIdentityWaiting.Reset()

	var unresolved *LocationUnresolved
	if errors.As(err, &unresolved) {
		cellLocationIdentityWaiting.WithLabelValues(unresolved.Reason).Set(1)
		return
	}
	if err != nil {
		return
	}

	cellLocationIdentitySource.WithLabelValues(identity.Source, identity.Reference.Name).Set(1)
	if identity.Mismatch {
		cellLocationIdentityMismatch.WithLabelValues(identity.Reference.Name).Set(1)
	}
}
