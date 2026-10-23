// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
)

// fakeWriter applies patches to a fakeReader's load balancers, so a preview
// and its apply run against the same state a real server would hold.
type fakeWriter struct {
	reader  *fakeReader
	writes  int
	dryRuns int
	err     error
}

func (w *fakeWriter) PatchProxy(_ context.Context, original, updated *networkingv1alpha.HTTPProxy, dryRun bool) error {
	if w.err != nil {
		return w.err
	}
	for i := range w.reader.proxies {
		p := &w.reader.proxies[i]
		if p.Name != original.Name {
			continue
		}
		if p.ResourceVersion != original.ResourceVersion {
			return apierrors.NewConflict(schema.GroupResource{Resource: "httpproxies"}, p.Name, errors.New("modified"))
		}
		if dryRun {
			w.dryRuns++
			return nil
		}
		w.writes++
		next := updated.DeepCopy()
		next.ResourceVersion = bumpVersion(p.ResourceVersion)
		*p = *next
		*updated = *next
		return nil
	}
	return apierrors.NewNotFound(schema.GroupResource{Resource: "httpproxies"}, original.Name)
}

var _ Writer = (*fakeWriter)(nil)

func bumpVersion(v string) string {
	n, _ := strconv.Atoi(v)
	return strconv.Itoa(n + 1)
}

func writeDeps(r *fakeReader, w Writer) DepsFor {
	return func(context.Context) (ToolDeps, error) {
		return ToolDeps{Reader: r, Writer: w, Namespace: "default"}, nil
	}
}

// pagesProxy is one origin behind Force HTTPS, the shape a load balancer
// fronting a static site usually has.
func pagesProxy() networkingv1alpha.HTTPProxy {
	p := healthyProxy()
	p.ResourceVersion = "7"
	p.Spec.Rules = []networkingv1alpha.HTTPProxyRule{{
		Backends: []networkingv1alpha.HTTPProxyRuleBackend{{Endpoint: "https://site.github.io"}},
	}}
	return *spec.SetForceHTTPS(&p, true)
}

func backendRuleOf(t *testing.T, p *networkingv1alpha.HTTPProxy) networkingv1alpha.HTTPProxyRule {
	t.Helper()
	for _, r := range p.Spec.Rules {
		if len(r.Backends) > 0 {
			return r
		}
	}
	t.Fatal("no backend rule")
	return networkingv1alpha.HTTPProxyRule{}
}

// TestHostOverrideIsPreviewedThenWrittenOnTheRoute is the change that went
// wrong when a model wrote the rules by hand: it put the Host override on the
// origin, where the console cannot edit it, and dropped Force HTTPS. Through
// the tool, the override lands on the route and every other rule survives.
func TestHostOverrideIsPreviewedThenWrittenOnTheRoute(t *testing.T) {
	pinClock(t)
	r := &fakeReader{proxies: []networkingv1alpha.HTTPProxy{pagesProxy()}}
	w := &fakeWriter{reader: r}
	header := albHeader(writeDeps(r, w))
	in := HeaderInput{Name: "my-app", Action: "set", Header: "Host", Value: "www.example.com"}

	_, _, err := header(context.Background(), nil, in)
	require.Error(t, err, "a change without a preview must be refused")
	assert.Contains(t, err.Error(), "dryRun")

	preview := in
	preview.DryRun = true
	_, planned, err := header(context.Background(), nil, preview)
	require.NoError(t, err)
	assert.True(t, planned.DryRun)
	assert.True(t, planned.Changed)
	assert.NotEmpty(t, planned.Confirm)
	assert.Equal(t, "www.example.com", planned.LoadBalancer.HostHeader)
	assert.Equal(t, 1, w.dryRuns, "the preview is validated by the platform")
	assert.Zero(t, w.writes, "a preview writes nothing")

	apply := in
	apply.Confirm = planned.Confirm
	_, applied, err := header(context.Background(), nil, apply)
	require.NoError(t, err)
	assert.False(t, applied.DryRun)
	assert.Empty(t, applied.Confirm)
	assert.Equal(t, 1, w.writes)

	got := &r.proxies[0]
	assert.True(t, spec.ForceHTTPS(got), "Force HTTPS must survive a header change")
	rule := backendRuleOf(t, got)
	require.Len(t, rule.Filters, 1, "the Host override belongs on the route")
	require.NotNil(t, rule.Filters[0].RequestHeaderModifier)
	assert.Equal(t, []gatewayv1.HTTPHeader{{Name: "Host", Value: "www.example.com"}}, rule.Filters[0].RequestHeaderModifier.Set)
	assert.Empty(t, rule.Backends[0].Filters, "nothing is written on the origin")
}

func TestApplyRefusesWhenTheLoadBalancerMovedSincePreview(t *testing.T) {
	pinClock(t)
	r := &fakeReader{proxies: []networkingv1alpha.HTTPProxy{pagesProxy()}}
	w := &fakeWriter{reader: r}
	update := albUpdate(writeDeps(r, w))

	_, planned, err := update(context.Background(), nil, UpdateInput{Name: "my-app", ForceHTTPS: ptr.To(false), DryRun: true})
	require.NoError(t, err)

	// Someone else changes it between the preview and the apply.
	r.proxies[0].ResourceVersion = "8"

	_, _, err = update(context.Background(), nil, UpdateInput{Name: "my-app", ForceHTTPS: ptr.To(false), Confirm: planned.Confirm})
	require.ErrorIs(t, err, errMoved)
	assert.Zero(t, w.writes)
	assert.True(t, spec.ForceHTTPS(&r.proxies[0]))
}

func TestApplyRefusesAChangeOtherThanTheOnePreviewed(t *testing.T) {
	pinClock(t)
	r := &fakeReader{proxies: []networkingv1alpha.HTTPProxy{pagesProxy()}}
	w := &fakeWriter{reader: r}
	header := albHeader(writeDeps(r, w))

	_, planned, err := header(context.Background(), nil,
		HeaderInput{Name: "my-app", Action: "set", Header: "Host", Value: "a.example.com", DryRun: true})
	require.NoError(t, err)

	_, _, err = header(context.Background(), nil,
		HeaderInput{Name: "my-app", Action: "set", Header: "Host", Value: "b.example.com", Confirm: planned.Confirm})
	require.ErrorIs(t, err, errMoved)
	assert.Zero(t, w.writes)
}

func TestAChangeAlreadyInPlaceWritesNothing(t *testing.T) {
	pinClock(t)
	r := &fakeReader{proxies: []networkingv1alpha.HTTPProxy{pagesProxy()}}
	w := &fakeWriter{reader: r}

	_, out, err := albUpdate(writeDeps(r, w))(context.Background(), nil,
		UpdateInput{Name: "my-app", ForceHTTPS: ptr.To(true), DryRun: true})
	require.NoError(t, err)
	assert.False(t, out.Changed)
	assert.Empty(t, out.Confirm, "there is nothing to agree to")
	assert.Zero(t, w.dryRuns+w.writes)
}

func TestChangeToolsRefuseWithoutAWriter(t *testing.T) {
	r := &fakeReader{proxies: []networkingv1alpha.HTTPProxy{pagesProxy()}}
	_, _, err := albUpdate(writeDeps(r, nil))(context.Background(), nil,
		UpdateInput{Name: "my-app", ForceHTTPS: ptr.To(false), DryRun: true})
	require.ErrorIs(t, err, errNoWriter)
}

func TestOriginChangesKeepThePoolAndForceHTTPS(t *testing.T) {
	pinClock(t)
	r := &fakeReader{proxies: []networkingv1alpha.HTTPProxy{pagesProxy()}}
	w := &fakeWriter{reader: r}
	origin := albOrigin(writeDeps(r, w))
	in := OriginChangeInput{Name: "my-app", Action: "add", Origin: Origin{Endpoint: "https://b.example.com"}, DryRun: true}

	_, planned, err := origin(context.Background(), nil, in)
	require.NoError(t, err)
	in.DryRun, in.Confirm = false, planned.Confirm
	_, _, err = origin(context.Background(), nil, in)
	require.NoError(t, err)

	got := &r.proxies[0]
	assert.True(t, spec.ForceHTTPS(got))
	backends := backendRuleOf(t, got).Backends
	require.Len(t, backends, 2)
	assert.Equal(t, "https://site.github.io", backends[0].Endpoint)
	assert.Equal(t, "https://b.example.com", backends[1].Endpoint)
}

func TestAHostnameNeedsAVerifiedDomain(t *testing.T) {
	pinClock(t)
	r := &fakeReader{proxies: []networkingv1alpha.HTTPProxy{pagesProxy()}}
	w := &fakeWriter{reader: r}

	_, _, err := albHostname(writeDeps(r, w))(context.Background(), nil,
		HostnameInput{Name: "my-app", Action: "add", Hostname: "app.example.com", DryRun: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not on a verified domain")
	assert.Contains(t, err.Error(), "Fix:")
	assert.Zero(t, w.dryRuns)
}

func TestARouteToAMissingNetworkServiceIsRefused(t *testing.T) {
	pinClock(t)
	r := &fakeReader{proxies: []networkingv1alpha.HTTPProxy{pagesProxy()}}
	w := &fakeWriter{reader: r}

	_, _, err := albRoute(writeDeps(r, w))(context.Background(), nil, RouteInput{
		Name: "my-app", Action: "add", Path: "/api", DryRun: true,
		Origins: []Origin{{NetworkService: "storefront", Port: "http"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storefront")
	assert.Zero(t, w.dryRuns)
}
