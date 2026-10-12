// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
)

// resolverManager answers GetCluster with err and hands back provider; the
// resolver uses nothing else of the manager.
type resolverManager struct {
	mcmanager.Manager
	err      error
	provider multicluster.Provider
}

func (m *resolverManager) GetCluster(context.Context, multicluster.ClusterName) (cluster.Cluster, error) {
	return nil, m.err
}

func (m *resolverManager) GetProvider() multicluster.Provider { return m.provider }

type readinessProvider struct {
	multicluster.Provider
	ready bool
	err   error
}

func (p readinessProvider) ProjectReady(context.Context, string) (bool, error) { return p.ready, p.err }

func TestClientForProjectTellsAGoneProjectFromOneNotRegisteredYet(t *testing.T) {
	notFound := fmt.Errorf("cluster p: %w", multicluster.ErrClusterNotFound)

	for _, tc := range []struct {
		name     string
		err      error
		provider multicluster.Provider
		gone     bool
	}{
		{"a Ready project the provider has not registered yet", notFound, readinessProvider{ready: true}, false},
		{"a project that is gone or no longer Ready", notFound, readinessProvider{ready: false}, true},
		{"a project whose state cannot be read", notFound, readinessProvider{err: errors.New("cache not started")}, false},
		{"a provider that cannot tell", notFound, nil, true},
		{"another failure", errors.New("connection refused"), readinessProvider{ready: false}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := NewProjectClusterResolver(&resolverManager{err: tc.err, provider: tc.provider})
			_, err := resolver.ClientForProject(context.Background(), "p")
			require.Error(t, err)
			assert.Equal(t, tc.gone, errors.Is(err, multicluster.ErrClusterNotFound))
		})
	}
}
