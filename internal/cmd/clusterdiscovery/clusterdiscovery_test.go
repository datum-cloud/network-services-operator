// SPDX-License-Identifier: AGPL-3.0-only

package clusterdiscovery

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

type fakeSyncedProvider struct{ synced bool }

func (p *fakeSyncedProvider) HasSynced() bool { return p.synced }

func TestProjectsRegisteredFollowsTheProvider(t *testing.T) {
	provider := &fakeSyncedProvider{}
	check := projectsRegistered(provider)
	request := httptest.NewRequest("GET", "/readyz/projects", nil)

	assert.Error(t, check(request), "not ready while projects are still registering")

	provider.synced = true
	assert.NoError(t, check(request), "ready once every project found at start is registered")
}
