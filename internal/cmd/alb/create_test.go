// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

func TestCreateNoWait(t *testing.T) {
	scheme, err := util.NewScheme()
	require.NoError(t, err)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	restore := withTestClient(c)
	defer restore()

	root := Command()
	root.SetArgs([]string{
		"create", "my-app",
		"--endpoint", "https://origin.example.com",
		"--project", "demo",
		"--no-waf",
		"--no-wait",
	})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)

	require.NoError(t, root.Execute())
	assert.Contains(t, out.String(), `Application load balancer "my-app" created.`)

	got := &networkingv1alpha.HTTPProxy{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "my-app"}, got))
	assert.Equal(t, "https://origin.example.com", got.Spec.Rules[len(got.Spec.Rules)-1].Backends[0].Endpoint)
}

func TestCreateWithNetworkService(t *testing.T) {
	scheme, err := util.NewScheme()
	require.NoError(t, err)
	service := &networkingv1alpha.NetworkService{
		ObjectMeta: metav1.ObjectMeta{Name: "storefront", Namespace: "default"},
		Spec: networkingv1alpha.NetworkServiceSpec{
			Ports: []networkingv1alpha.NetworkServicePort{{Name: "http", Port: 8080}},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service).Build()

	restore := withTestClient(c)
	defer restore()

	root := Command()
	root.SetArgs([]string{
		"create", "my-app",
		"--network-service", "storefront", "--port", "http",
		"--endpoint", "https://fallback.example.com",
		"--project", "demo", "--no-waf", "--no-wait",
	})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	require.NoError(t, root.Execute())

	got := &networkingv1alpha.HTTPProxy{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "my-app"}, got))
	backends := got.Spec.Rules[len(got.Spec.Rules)-1].Backends
	require.Len(t, backends, 2)
	assert.Equal(t, "https://fallback.example.com", backends[0].Endpoint)
	require.NotNil(t, backends[1].NetworkService)
	assert.Equal(t, "storefront", backends[1].NetworkService.Name)
	assert.Equal(t, "http", backends[1].NetworkService.Port)
	assert.Nil(t, backends[1].TLS)

	services := &networkingv1alpha.NetworkServiceList{}
	require.NoError(t, c.List(context.Background(), services))
	assert.Len(t, services.Items, 1)
}

func TestCreateRefusesMissingNetworkService(t *testing.T) {
	scheme, err := util.NewScheme()
	require.NoError(t, err)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	restore := withTestClient(c)
	defer restore()

	root := Command()
	root.SetArgs([]string{
		"create", "my-app",
		"--network-service", "storefront", "--port", "http",
		"--project", "demo", "--no-waf", "--no-wait",
	})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	err = root.Execute()
	require.Error(t, err)
	var cliErr *util.CLIError
	require.ErrorAs(t, err, &cliErr)
	assert.Equal(t, util.ExitNotFound, cliErr.Code())
	assert.Contains(t, err.Error(), `network service "storefront" not found`)

	got := &networkingv1alpha.HTTPProxy{}
	require.Error(t, c.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "my-app"}, got))
	services := &networkingv1alpha.NetworkServiceList{}
	require.NoError(t, c.List(context.Background(), services))
	assert.Empty(t, services.Items)
}

func TestCreateRefusesUnknownNetworkServicePort(t *testing.T) {
	scheme, err := util.NewScheme()
	require.NoError(t, err)
	service := &networkingv1alpha.NetworkService{
		ObjectMeta: metav1.ObjectMeta{Name: "storefront", Namespace: "default"},
		Spec: networkingv1alpha.NetworkServiceSpec{
			Ports: []networkingv1alpha.NetworkServicePort{{Name: "http", Port: 8080}},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service).Build()

	restore := withTestClient(c)
	defer restore()

	root := Command()
	root.SetArgs([]string{
		"create", "my-app",
		"--network-service", "storefront", "--port", "grpc",
		"--project", "demo", "--no-waf", "--no-wait",
	})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	err = root.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no port named "grpc"`)
}

func TestUpdateRefusesBackendFlags(t *testing.T) {
	scheme, err := util.NewScheme()
	require.NoError(t, err)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	restore := withTestClient(c)
	defer restore()

	root := Command()
	root.SetArgs([]string{"update", "my-app", "--endpoint", "https://new.example.com", "--project", "demo"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	err = root.Execute()
	require.Error(t, err)
	var cliErr *util.CLIError
	require.ErrorAs(t, err, &cliErr)
	assert.Equal(t, util.ExitUsage, cliErr.Code())
	assert.Contains(t, err.Error(), "managed per route")
}

func TestWaitForCanonicalHostname(t *testing.T) {
	scheme, err := util.NewScheme()
	require.NoError(t, err)
	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: "default"},
		Status:     networkingv1alpha.HTTPProxyStatus{CanonicalHostname: "abc.datumproxy.net"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&networkingv1alpha.HTTPProxy{}).WithObjects(proxy).Build()

	oldInterval := waitInterval
	waitInterval = time.Millisecond
	defer func() { waitInterval = oldInterval }()

	hostname, err := waitForCanonicalHostname(context.Background(), c, "my-app", time.Second)
	require.NoError(t, err)
	assert.Equal(t, "abc.datumproxy.net", hostname)
}

func withTestClient(c client.Client) func() {
	oldClient := newClient
	oldEntitlement := ensureEntitlement
	newClient = func(string) (client.Client, error) { return c, nil }
	ensureEntitlement = func(context.Context, string, io.Reader, io.Writer) error { return nil }
	return func() {
		newClient = oldClient
		ensureEntitlement = oldEntitlement
	}
}
