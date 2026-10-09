// SPDX-License-Identifier: AGPL-3.0-only

package webhook

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
)

// ProjectReader returns the reader a webhook reads a project through.
//
// It reads the project's API server, not the cache. The webhook Service selects every replica, so a
// replica admits objects of projects it does not own, and a cached read there starts an informer
// whose watch stays open for the life of the process.
func ProjectReader(ctx context.Context, mgr mcmanager.Manager, project multicluster.ClusterName) (client.Reader, error) {
	cl, err := mgr.GetCluster(ctx, project)
	if err != nil {
		return nil, err
	}
	return cl.GetAPIReader(), nil
}
