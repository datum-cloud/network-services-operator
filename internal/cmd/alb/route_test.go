// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

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
	return fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&networkingv1alpha.HTTPProxy{}).
		WithObjects(proxy, service).Build()
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

	before := loadTestProxy(t, c)
	_, err = run(t, c, "route", "update", "my-app", "--path", "/api", "--network-service", "missing", "--port", "http")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `network service "missing" not found`)
	assert.Equal(t, before.Spec.Rules, loadTestProxy(t, c).Spec.Rules)
	assert.Equal(t, before.ResourceVersion, loadTestProxy(t, c).ResourceVersion)

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

func TestRoutePathRequiredIsUsageError(t *testing.T) {
	c := newRouteTestClient(t)
	for _, args := range [][]string{
		{"route", "remove", "my-app"},
		{"route", "update", "my-app", "--endpoint", "https://b.example.com"},
		{"route", "backend", "add", "my-app", "--endpoint", "https://b.example.com"},
		{"route", "backend", "remove", "my-app", "--endpoint", "https://b.example.com"},
		{"route", "add", "my-app", "--path", "//", "--endpoint", "https://b.example.com"},
	} {
		_, err := run(t, c, args...)
		require.Error(t, err, args)
		var cliErr *util.CLIError
		require.ErrorAs(t, err, &cliErr, args)
		assert.Equal(t, util.ExitUsage, cliErr.Code(), args)
		assert.NotEmpty(t, cliErr.Fix(), args)
	}
}

func TestRouteRemoveChecksExistenceBeforePrompt(t *testing.T) {
	c := newRouteTestClient(t)
	restore := withTestClient(c)
	defer restore()

	root := Command()
	root.SetArgs([]string{"route", "remove", "my-app", "--path", "/nope", "--project", "demo"})
	root.SetIn(strings.NewReader("y\n"))
	var errOut bytes.Buffer
	root.SetOut(io.Discard)
	root.SetErr(&errOut)

	err := root.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `route "/nope" not found`)
	assert.NotContains(t, errOut.String(), "Remove route")
}

func TestRouteRemoveSkipsPromptOnDryRun(t *testing.T) {
	c := newRouteTestClient(t)
	_, err := run(t, c, "route", "add", "my-app", "--path", "/api", "--endpoint", "https://api.example.com")
	require.NoError(t, err)

	restore := withTestClient(c)
	defer restore()
	root := Command()
	root.SetArgs([]string{"route", "remove", "my-app", "--path", "/api", "--dry-run", "--project", "demo"})
	root.SetIn(strings.NewReader("n\n"))
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)

	require.NoError(t, root.Execute())
	assert.NotContains(t, errOut.String(), "Remove route")
	assert.Contains(t, out.String(), "validated")
	assert.Len(t, spec.UserRoutes(loadTestProxy(t, c)), 2)
}

func TestRouteListMarksAdvancedRules(t *testing.T) {
	c := newRouteTestClient(t)
	proxy := loadTestProxy(t, c)
	proxy.Spec.Rules = append(proxy.Spec.Rules, networkingv1alpha.HTTPProxyRule{
		Matches: []gatewayv1.HTTPRouteMatch{{
			Path: &gatewayv1.HTTPPathMatch{Type: ptr.To(gatewayv1.PathMatchExact), Value: ptr.To("/exact")},
		}},
		Backends: []networkingv1alpha.HTTPProxyRuleBackend{{Endpoint: "https://exact.example.com"}},
	})
	require.NoError(t, c.Update(context.Background(), proxy))

	out, err := run(t, c, "route", "list", "my-app")
	require.NoError(t, err)
	assert.Contains(t, out, "advanced")
	assert.Contains(t, out, "exact /exact")
}

func TestPatchRetriesOnceOnConflict(t *testing.T) {
	c := newRouteTestClient(t)
	conflicting := &conflictOnce{Client: c}

	restore := withTestClient(conflicting)
	defer restore()
	root := Command()
	root.SetArgs([]string{"route", "add", "my-app", "--path", "/api", "--endpoint", "https://api.example.com", "--project", "demo"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	require.NoError(t, root.Execute())
	assert.Equal(t, 2, conflicting.patches)
	assert.Len(t, spec.UserRoutes(loadTestProxy(t, c)), 2)
}

type conflictOnce struct {
	client.Client
	patches int
}

func (c *conflictOnce) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	c.patches++
	if c.patches == 1 {
		return apierrors.NewConflict(schema.GroupResource{Group: "networking.datumapis.com", Resource: "httpproxies"}, obj.GetName(), errors.New("stale"))
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func TestListStatusFilter(t *testing.T) {
	c := newRouteTestClient(t)
	proxy := loadTestProxy(t, c)
	proxy.Status.Conditions = []metav1.Condition{{
		Type:   networkingv1alpha.HTTPProxyConditionProgrammed,
		Status: metav1.ConditionTrue,
		Reason: "Programmed",
	}}
	require.NoError(t, c.Status().Update(context.Background(), proxy))

	out, err := run(t, c, "list", "--status", "active", "-o", "name")
	require.NoError(t, err)
	assert.Contains(t, out, "my-app")

	out, err = run(t, c, "list", "--status", "error", "-o", "name")
	require.NoError(t, err)
	assert.NotContains(t, out, "my-app")

	_, err = run(t, c, "list", "--status", "broken")
	require.Error(t, err)
	var cliErr *util.CLIError
	require.ErrorAs(t, err, &cliErr)
	assert.Equal(t, util.ExitUsage, cliErr.Code())
}
