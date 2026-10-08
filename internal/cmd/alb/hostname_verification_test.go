// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

func newDomain(name string, verified bool) *networkingv1alpha.Domain {
	status := metav1.ConditionFalse
	if verified {
		status = metav1.ConditionTrue
	}
	return &networkingv1alpha.Domain{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: util.ResourceNamespace},
		Spec:       networkingv1alpha.DomainSpec{DomainName: name},
		Status: networkingv1alpha.DomainStatus{Conditions: []metav1.Condition{{
			Type:   networkingv1alpha.DomainConditionVerified,
			Status: status,
		}}},
	}
}

func newHostnameTestClient(t *testing.T, funcs *interceptor.Funcs) client.Client {
	t.Helper()
	scheme, err := util.NewScheme()
	require.NoError(t, err)
	proxy, err := spec.BuildHTTPProxy(spec.CreateInput{
		Name:       "my-app",
		Backends:   []spec.BackendInput{{Endpoint: "https://origin.example.com"}},
		ForceHTTPS: true,
	})
	require.NoError(t, err)
	b := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(proxy, newDomain("example.com", true), newDomain("pending.dev", false))
	if funcs != nil {
		b = b.WithInterceptorFuncs(*funcs)
	}
	return b.Build()
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	var ce *util.CLIError
	require.True(t, errors.As(err, &ce), "expected a CLIError, got %v", err)
	return ce.Code()
}

func TestHostnameAddRequiresVerifiedDomain(t *testing.T) {
	c := newHostnameTestClient(t, nil)

	_, err := run(t, c, "hostname", "add", "my-app", "app.pending.dev")
	require.Error(t, err)
	assert.Equal(t, util.ExitInvalid, exitCode(t, err))
	assert.Contains(t, err.Error(), "domain pending.dev is not verified yet")
	assert.Empty(t, loadTestProxy(t, c).Spec.Hostnames, "nothing is written when the domain is unverified")

	_, err = run(t, c, "hostname", "add", "my-app", "app.unknown.io")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no domain in this project covers it")

	out, err := run(t, c, "hostname", "add", "my-app", "app.example.com")
	require.NoError(t, err)
	assert.Contains(t, out, `Hostname "app.example.com" attached`)
	assert.Len(t, loadTestProxy(t, c).Spec.Hostnames, 1)
}

func TestHostnameAddReportsDuplicateBeforeVerification(t *testing.T) {
	c := newHostnameTestClient(t, nil)
	proxy := loadTestProxy(t, c)
	proxy.Spec.Hostnames = append(proxy.Spec.Hostnames, "app.pending.dev")
	require.NoError(t, c.Update(context.Background(), proxy))

	_, err := run(t, c, "hostname", "add", "my-app", "app.pending.dev")
	require.Error(t, err)
	assert.Equal(t, util.ExitConflict, exitCode(t, err))
}

func TestHostnameAddSkipsCheckWhenDomainsAreForbidden(t *testing.T) {
	c := newHostnameTestClient(t, &interceptor.Funcs{
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, isDomains := list.(*networkingv1alpha.DomainList); isDomains {
				return apierrors.NewForbidden(schema.GroupResource{Resource: "domains"}, "", errors.New("denied"))
			}
			return cl.List(ctx, list, opts...)
		},
	})

	_, err := run(t, c, "hostname", "add", "my-app", "app.unknown.io")
	require.NoError(t, err)
	assert.Len(t, loadTestProxy(t, c).Spec.Hostnames, 1)
}

func TestCreateRequiresVerifiedHostnames(t *testing.T) {
	scheme, err := util.NewScheme()
	require.NoError(t, err)
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(newDomain("example.com", true), newDomain("pending.dev", false)).Build()

	_, err = run(t, c, "create", "new-app", "--endpoint", "https://origin.example.com",
		"--hostname", "app.example.com", "--hostname", "app.pending.dev", "--no-waf", "--no-wait")
	require.Error(t, err)
	assert.Equal(t, util.ExitInvalid, exitCode(t, err))
	assert.Contains(t, err.Error(), "app.pending.dev")
	assert.NotContains(t, err.Error(), "app.example.com:")

	err = c.Get(context.Background(), client.ObjectKey{Namespace: util.ResourceNamespace, Name: "new-app"}, &networkingv1alpha.HTTPProxy{})
	assert.True(t, apierrors.IsNotFound(err), "the load balancer is not created")

	_, err = run(t, c, "create", "new-app", "--endpoint", "https://origin.example.com",
		"--hostname", "app.example.com", "--no-waf", "--no-wait")
	require.NoError(t, err)
}
