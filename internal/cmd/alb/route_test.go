// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

func newRouteTestClient(t *testing.T) client.Client {
	t.Helper()
	scheme, err := util.NewScheme()
	require.NoError(t, err)

	proxy, err := spec.BuildHTTPProxy(spec.CreateInput{
		Name:       "my-app",
		Backends:   []spec.BackendInput{{Endpoint: "https://origin.example.com"}},
		ForceHTTPS: true,
	})
	require.NoError(t, err)
	service := &networkingv1alpha.NetworkService{
		ObjectMeta: metav1.ObjectMeta{Name: "storefront", Namespace: "default"},
		Spec: networkingv1alpha.NetworkServiceSpec{
			Ports: []networkingv1alpha.NetworkServicePort{{Name: "http", Port: 8080}},
		},
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(proxy, service).Build()
}

func run(t *testing.T, c client.Client, args ...string) (string, error) {
	t.Helper()
	restore := withTestClient(c)
	defer restore()

	root := Command()
	root.SetArgs(append(args, "--project", "demo", "--yes"))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)
	err := root.Execute()
	return out.String(), err
}

func loadTestProxy(t *testing.T, c client.Client) *networkingv1alpha.HTTPProxy {
	t.Helper()
	got := &networkingv1alpha.HTTPProxy{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "my-app"}, got))
	return got
}

func TestRouteAddUpdateRemove(t *testing.T) {
	c := newRouteTestClient(t)

	out, err := run(t, c, "route", "add", "my-app", "--path", "/api", "--endpoint", "https://api.example.com")
	require.NoError(t, err)
	assert.Contains(t, out, "Route /api added")

	proxy := loadTestProxy(t, c)
	routes := spec.UserRoutes(proxy)
	require.Len(t, routes, 2)
	assert.True(t, spec.ForceHTTPS(proxy))

	_, err = run(t, c, "route", "update", "my-app", "--path", "/api", "--network-service", "storefront", "--port", "http")
	require.NoError(t, err)
	routes = spec.UserRoutes(loadTestProxy(t, c))
	assert.Equal(t, "https://origin.example.com", spec.FormatBackend(routes[0].Backends[0]))
	assert.Equal(t, "storefront:http", spec.FormatBackend(routes[1].Backends[0]))

	_, err = run(t, c, "route", "update", "my-app", "--path", "/api", "--network-service", "missing", "--port", "http")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `network service "missing" not found`)

	_, err = run(t, c, "route", "remove", "my-app", "--path", "/")
	require.Error(t, err)

	_, err = run(t, c, "route", "remove", "my-app", "--path", "/api")
	require.NoError(t, err)
	assert.Len(t, spec.UserRoutes(loadTestProxy(t, c)), 1)

	out, err = run(t, c, "route", "list", "my-app")
	require.NoError(t, err)
	assert.Contains(t, out, "system")
	assert.Contains(t, out, "https://origin.example.com")
}

func TestRouteBackendAddRemove(t *testing.T) {
	c := newRouteTestClient(t)

	_, err := run(t, c, "route", "backend", "add", "my-app", "--path", "/", "--endpoint", "https://b.example.com")
	require.NoError(t, err)
	_, err = run(t, c, "route", "backend", "add", "my-app", "--path", "/", "--network-service", "storefront", "--port", "http")
	require.NoError(t, err)

	proxy := loadTestProxy(t, c)
	require.Len(t, spec.UserRoutes(proxy)[0].Backends, 3)
	assert.True(t, spec.ForceHTTPS(proxy))
	assert.Equal(t, "https://origin.example.com +2", spec.OriginSummary(proxy))

	_, err = run(t, c, "route", "backend", "add", "my-app", "--path", "/",
		"--endpoint", "https://c.example.com", "--endpoint", "https://d.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exactly one backend")

	out, err := run(t, c, "route", "backend", "list", "my-app", "--path", "/", "-o", "name")
	require.NoError(t, err)
	assert.Contains(t, out, "storefront:http")
	assert.Contains(t, out, "https://b.example.com")

	_, err = run(t, c, "route", "backend", "remove", "my-app", "--path", "/", "--endpoint", "https://b.example.com")
	require.NoError(t, err)
	_, err = run(t, c, "route", "backend", "remove", "my-app", "--path", "/", "--network-service", "storefront", "--port", "http")
	require.NoError(t, err)
	require.Len(t, spec.UserRoutes(loadTestProxy(t, c))[0].Backends, 1)

	_, err = run(t, c, "route", "backend", "remove", "my-app", "--path", "/", "--endpoint", "https://origin.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "last backend")
}

func TestRouteBackendAddRequiresPath(t *testing.T) {
	c := newRouteTestClient(t)
	_, err := run(t, c, "route", "backend", "add", "my-app", "--endpoint", "https://b.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path")
}
