// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// Writer changes a load balancer. It is the only way a tool here reaches the
// cluster to write, and it is kept apart from Reader so that what can write
// stays one small, reviewable interface.
//
// Like Reader, the identity a write runs under is decided by whoever builds the
// Writer: the server builds one per request from the caller's own credentials,
// so a tool can never change more than the caller could change themselves.
type Writer interface {
	// PatchProxy merge-patches original into updated, refusing if original has
	// moved since it was read. With dryRun the platform validates the patch and
	// keeps nothing. On success updated holds what the platform returned.
	PatchProxy(ctx context.Context, original, updated *networkingv1alpha.HTTPProxy, dryRun bool) error
}

// ClientWriter implements Writer against a controller-runtime client.
type ClientWriter struct {
	Client client.Client
	// FieldManager names this writer in the object's managed fields, so a
	// change can be traced back to the tool that made it.
	FieldManager string
}

var _ Writer = (*ClientWriter)(nil)

// NewClientWriter returns a Writer backed by c that records its changes under
// fieldManager. Every write is performed with whatever credentials c carries.
func NewClientWriter(c client.Client, fieldManager string) *ClientWriter {
	return &ClientWriter{Client: c, FieldManager: fieldManager}
}

func (w *ClientWriter) PatchProxy(ctx context.Context, original, updated *networkingv1alpha.HTTPProxy, dryRun bool) error {
	opts := []client.PatchOption{client.FieldOwner(w.FieldManager)}
	if dryRun {
		opts = append(opts, client.DryRunAll)
	}
	return w.Client.Patch(ctx, updated, lockedPatch(original), opts...)
}

// lockedPatch is the patch every write sends: a merge patch carrying the
// resourceVersion it was computed against, so a change made in between fails
// rather than being silently overwritten.
func lockedPatch(original *networkingv1alpha.HTTPProxy) client.Patch {
	return client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})
}
