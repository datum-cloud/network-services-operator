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
