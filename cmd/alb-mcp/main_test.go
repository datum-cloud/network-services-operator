// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"

	"go.datum.net/network-services-operator/internal/mcpserver"
)

// TestNoToolTakesAProjectArgument pins why the project is a header. A tool
// argument is chosen by the model, and a model that could name its own project
// would be one prompt injection away from another tenant's load balancers.
func TestNoToolTakesAProjectArgument(t *testing.T) {
	base := &rest.Config{Host: "https://api.datum.net"}
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer abc")
	r.Header.Set(mcpserver.ProjectHeader, "from-the-header")

	deps, err := depsFromRequest(r, base)(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, deps.Reader)
	assert.Equal(t, "default", deps.Namespace,
		"objects live in the default namespace inside a project's control plane")
}
