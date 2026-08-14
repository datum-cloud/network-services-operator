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

// LocationIdentity is the answer to "which location is this cell".
type LocationIdentity struct {
	Reference networkingv1alpha.LocationReference
	Source    string

	// Mismatch is set when a delivered copy and a configured location disagree.
	// The delivered copy wins, because it is reconciled and centrally
	// correctable, but the disagreement is exactly the failure this design
	// exists to kill and must not pass silently.
	Mismatch bool
}

// LocationUnresolved means the cell cannot name itself yet. It is a waiting
// state to report on the objects that are stuck, never a boot failure: a cell
// must not crash-loop because delivery is merely late.
type LocationUnresolved struct {
	Reason  string
	Message string
}

func (e *LocationUnresolved) Error() string { return e.Message }

// ResolveLocationIdentity picks the location a cell serves. Delivery wins over
// configuration, and neither guesses: more than one delivered copy is a
// labelling error, so an explicit configured answer is used if there is one and
// otherwise the cell waits visibly.
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
			Name:      configured.Name,
			Namespace: configured.Namespace,
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

// reportLocationIdentity exports which source a cell's identity came from, so a
// permanent fallback cannot become a hiding place.
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
