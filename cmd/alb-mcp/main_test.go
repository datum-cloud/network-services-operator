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
)

// TestRefusesTheInClusterFallback pins the boot guard. Without it, a deployment
// missing its KUBECONFIG hangs a project control-plane path off the local API
// server, and every tool call fails with a 401 that reads like the caller's
// token is bad when the deployment is what is wrong.
func TestRefusesTheInClusterFallback(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.96.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")

	err := checkControlPlaneEndpoint(&rest.Config{Host: "https://10.96.0.1:443"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "never correct")
	assert.Contains(t, err.Error(), "KUBECONFIG", "the message has to say how to fix the deployment")

	// A trailing slash is the same endpoint.
	require.Error(t, checkControlPlaneEndpoint(&rest.Config{Host: "https://10.96.0.1:443/"}))

	// A real control plane is fine.
	require.NoError(t, checkControlPlaneEndpoint(&rest.Config{Host: "https://api.datum.net"}))
}

// TestOutsideAPodTheGuardDoesNotFire keeps local runs working: with no
// KUBERNETES_SERVICE_* in the environment there is no in-cluster fallback to
// catch.
func TestOutsideAPodTheGuardDoesNotFire(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	require.NoError(t, checkControlPlaneEndpoint(&rest.Config{Host: "https://10.96.0.1:443"}))
}

// TestTheServersOwnCredentialsNeverReachACallersRead is the central security
// property. The base config is what the deployment was given; a caller's read
// must carry only the caller's token.
func TestTheServersOwnCredentialsNeverReachACallersRead(t *testing.T) {
	base := &rest.Config{
		Host:            "https://api.datum.net",
		BearerToken:     "the-servers-own-token",
		BearerTokenFile: "/var/run/secrets/token",
		Username:        "server",
		Password:        "hunter2",
		TLSClientConfig: rest.TLSClientConfig{CertFile: "/tls.crt", KeyFile: "/tls.key"},
	}

	cfg, err := clientConfig(base, "the-callers-token", "my-project")
	require.NoError(t, err)

	assert.Equal(t, "the-callers-token", cfg.BearerToken)
	assert.Empty(t, cfg.BearerTokenFile)
	assert.Empty(t, cfg.Username)
	assert.Empty(t, cfg.Password)
	assert.Empty(t, cfg.CertFile)
	assert.Empty(t, cfg.KeyFile)

	assert.Equal(t, base.BearerToken, "the-servers-own-token", "the base config must not be mutated")
}

// TestTheProjectIsInterpolatedOnlyAfterValidation pins the reason the project
// is validated at all: it arrives in a header and goes into a URL path, so an
// unvalidated value could reshape that path into another API route.
func TestTheProjectIsInterpolatedOnlyAfterValidation(t *testing.T) {
	base := &rest.Config{Host: "https://api.datum.net"}

	cfg, err := clientConfig(base, "token", "my-project")
	require.NoError(t, err)
	assert.Equal(t,
		"https://api.datum.net/apis/resourcemanager.miloapis.com/v1alpha1/projects/my-project/control-plane",
		cfg.Host)

	for _, bad := range []string{"../../etc", "Has-Capitals", "with space", "a/b", ""} {
		_, err := clientConfig(base, "token", bad)
		assert.Error(t, err, "project %q must be refused", bad)
	}
}

// TestAToolCallWithoutCredentialsBlamesTheClient pins the wording as much as
// the behaviour. The first assistant to relay a vaguer message told the user to
// re-authenticate, which they cannot act on and which is not the problem.
func TestAToolCallWithoutCredentialsBlamesTheClient(t *testing.T) {
	base := &rest.Config{Host: "https://api.datum.net"}

	noAuth := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	_, err := depsFromRequest(noAuth, base)(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not forward the user's identity")
	assert.Contains(t, err.Error(), "did nothing wrong")

	noProject := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	noProject.Header.Set("Authorization", "Bearer abc")
	_, err = depsFromRequest(noProject, base)(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), projectHeader)
}

func TestBearerTokenParsing(t *testing.T) {
	for _, tc := range []struct{ header, want string }{
		{"Bearer abc", "abc"},
		{"bearer abc", "abc"},
		{"Bearer  abc  ", "abc"},
		{"Basic abc", ""},
		{"abc", ""},
		{"", ""},
	} {
		r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		if tc.header != "" {
			r.Header.Set("Authorization", tc.header)
		}
		assert.Equal(t, tc.want, bearerToken(r), "Authorization: %q", tc.header)
	}
}

// TestNoToolTakesAProjectArgument pins why the project is a header. A tool
// argument is chosen by the model, and a model that could name its own project
// would be one prompt injection away from another tenant's load balancers.
func TestNoToolTakesAProjectArgument(t *testing.T) {
	base := &rest.Config{Host: "https://api.datum.net"}
	r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	r.Header.Set("Authorization", "Bearer abc")
	r.Header.Set(projectHeader, "from-the-header")

	deps, err := depsFromRequest(r, base)(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, deps.Reader)
	assert.Equal(t, "default", deps.Namespace,
		"objects live in the default namespace inside a project's control plane")
}
