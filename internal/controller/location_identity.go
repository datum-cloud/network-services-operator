// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"go.miloapis.com/locations/pkg/locationidentity"

	"go.datum.net/network-services-operator/internal/config"
)

// locationsNotServed reports that locations.miloapis.com is absent from the
// control plane being read. A plane that has not been migrated yet reads as
// empty rather than as a failure, so a reconcile degrades instead of erroring.
func locationsNotServed(err error) bool {
	return apimeta.IsNoMatchError(err) || apierrors.IsNotFound(err)
}

// noServingLocations answers every list as empty, which is how a control plane
// that does not serve the kind is treated.
type noServingLocations struct {
	client.Reader
}

func (noServingLocations) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return nil
}

func resolveLocationIdentity(
	ctx context.Context,
	reader client.Reader,
	configured config.LocationConfig,
) (locationidentity.LocationIdentity, error) {
	wanted := locationidentity.LocationConfig{Name: configured.Name}
	identity, err := locationidentity.Resolve(ctx, reader, wanted)
	if err != nil && locationsNotServed(err) {
		return locationidentity.Resolve(ctx, noServingLocations{reader}, wanted)
	}
	return identity, err
}

func reportLocationIdentity(identity locationidentity.LocationIdentity, err error) {
	cellLocationIdentitySource.Reset()
	cellLocationIdentityMismatch.Reset()
	cellLocationIdentityWaiting.Reset()

	var unresolved *locationidentity.LocationUnresolved
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
