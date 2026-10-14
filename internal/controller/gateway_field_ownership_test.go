// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	gatewayutil "go.datum.net/network-services-operator/internal/util/gateway"
)

func fieldOwnershipConfig() config.NetworkServicesOperator {
	cfg := config.NetworkServicesOperator{}
	config.SetObjectDefaults_NetworkServicesOperator(&cfg)
	cfg.Gateway.TargetDomain = "prism.example.test"
	return cfg
}

func proxyWithHostnames(name string, hostnames ...gatewayv1.Hostname) *networkingv1alpha.HTTPProxy {
	return newHTTPProxy(func(h *networkingv1alpha.HTTPProxy) {
		h.Namespace = metav1.NamespaceDefault
		h.Name = name
		h.Spec.Hostnames = hostnames
	})
}

func desiredGateway(t *testing.T, c client.Client, proxy *networkingv1alpha.HTTPProxy) *gatewayv1.Gateway {
	t.Helper()
	r := &HTTPProxyReconciler{Config: fieldOwnershipConfig()}
	desired, err := r.collectDesiredResources(context.Background(), c, proxy)
	require.NoError(t, err)
	return desired.gateway
}

// The HTTPProxy controller's apply must carry both default listeners, or the
// API server removes the one it leaves out and the webhook restores it without
// its hostname; and it must carry no hostname on them, or its force takes the
// hostname from the gateway controller.
func TestGatewayIntentCarriesOnlyTheHTTPProxyControllersFields(t *testing.T) {
	for _, hostnames := range [][]gatewayv1.Hostname{nil, {"a.example.test", "b.example.test"}} {
		proxy := proxyWithHostnames("proxy", hostnames...)
		want := desiredGateway(t, fake.NewClientBuilder().WithScheme(clientgoscheme.Scheme).Build(), proxy)
		require.NoError(t, controllerutil.SetControllerReference(proxy, want, testFieldOwnershipScheme(t)))

		intent, err := gatewayIntent(want, "gateway-uid")
		require.NoError(t, err)
		body, err := json.Marshal(intent)
		require.NoError(t, err)
		var sent map[string]any
		require.NoError(t, json.Unmarshal(body, &sent))
		metadata, _ := sent["metadata"].(map[string]any)
		spec, _ := sent["spec"].(map[string]any)
		sentGateway := &gatewayv1.Gateway{}
		require.NoError(t, roundTrip(spec, &sentGateway.Spec))

		assert.ElementsMatch(t, []string{"apiVersion", "kind", "metadata", "spec"}, slices.Collect(maps.Keys(sent)))
		assert.ElementsMatch(t, []string{"name", "namespace", "uid", "ownerReferences"}, slices.Collect(maps.Keys(metadata)))
		assert.ElementsMatch(t, []string{"gatewayClassName", "listeners"}, slices.Collect(maps.Keys(spec)))

		names := listenerNames(sentGateway)
		assert.Contains(t, names, gatewayv1.SectionName(gatewayutil.DefaultHTTPListenerName))
		assert.Contains(t, names, gatewayv1.SectionName(gatewayutil.DefaultHTTPSListenerName))
		assert.Len(t, names, 2+2*len(hostnames))
		for _, l := range sentGateway.Spec.Listeners {
			if gatewayutil.IsDefaultListener(l) {
				assert.Nil(t, l.Hostname, "listener %s", l.Name)
			}
		}
	}
}

func testFieldOwnershipScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, gatewayv1.Install(s))
	require.NoError(t, networkingv1alpha.AddToScheme(s))
	return s
}

func TestUpstreamGatewayIntent(t *testing.T) {
	r := &GatewayReconciler{Config: fieldOwnershipConfig()}
	base := func(change func(*gatewayv1.Gateway)) *gatewayv1.Gateway {
		gw := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "gw", UID: "11111111-1111-1111-1111-111111111111"}}
		gatewayutil.SetDefaultListeners(gw, r.Config.Gateway)
		canonical := gatewayv1.Hostname(r.gatewayCanonicalHostname(gw))
		for i := range gw.Spec.Listeners {
			gw.Spec.Listeners[i].Hostname = ptr.To(canonical)
		}
		gw.Finalizers = []string{gatewayControllerFinalizer}
		change(gw)
		return gw
	}
	tests := []struct {
		name          string
		gateway       *gatewayv1.Gateway
		wantMissing   bool
		wantListeners []gatewayv1.SectionName
	}{
		{"all present: nothing to save", base(func(*gatewayv1.Gateway) {}), false, []gatewayv1.SectionName{"default-http", "default-https"}},
		{"no finalizer", base(func(g *gatewayv1.Gateway) { g.Finalizers = nil }), true, []gatewayv1.SectionName{"default-http", "default-https"}},
		{"a default listener without hostname", base(func(g *gatewayv1.Gateway) { g.Spec.Listeners[1].Hostname = nil }), true, []gatewayv1.SectionName{"default-http", "default-https"}},
		{"a user's hostname stays the user's", base(func(g *gatewayv1.Gateway) {
			g.Spec.Listeners[0].Hostname = ptr.To(gatewayv1.Hostname("user.example.test"))
		}), false, []gatewayv1.SectionName{"default-https"}},
		{"no default listeners", base(func(g *gatewayv1.Gateway) { g.Spec.Listeners = nil }), false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intent, missing := r.upstreamGatewayIntent(tt.gateway)
			assert.Equal(t, tt.wantMissing, missing)
			assert.Equal(t, tt.gateway.UID, *intent.UID)
			assert.Equal(t, []string{gatewayControllerFinalizer}, intent.Finalizers)
			var names []gatewayv1.SectionName
			for _, l := range intent.Spec.Listeners {
				names = append(names, *l.Name)
				assert.Equal(t, r.gatewayCanonicalHostname(tt.gateway), string(*l.Hostname))
			}
			assert.Equal(t, tt.wantListeners, names)
		})
	}
}

// TestGatewaySavesOnAnAPIServer runs the two controllers' Gateway saves
// against a kube-apiserver.
func TestGatewaySavesOnAnAPIServer(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}
	env := &envtest.Environment{CRDInstallOptions: envtest.CRDInstallOptions{Paths: installedGatewayAPICRDPaths(t), ErrorIfPathMissing: true}}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, env.Stop()) })
	c, err := client.New(cfg, client.Options{Scheme: testFieldOwnershipScheme(t)})
	require.NoError(t, err)
	cl := &fakeCluster{cl: c}
	ctx := context.Background()
	proxies := &HTTPProxyReconciler{Config: fieldOwnershipConfig()}
	gateways := &GatewayReconciler{Config: fieldOwnershipConfig()}

	read := func(t *testing.T, name string) *gatewayv1.Gateway {
		t.Helper()
		gw := &gatewayv1.Gateway{}
		require.NoError(t, c.Get(ctx, types.NamespacedName{Namespace: metav1.NamespaceDefault, Name: name}, gw))
		return gw
	}
	saveAsGatewayController := func(t *testing.T, name string) {
		t.Helper()
		if intent, missing := gateways.upstreamGatewayIntent(read(t, name)); missing {
			require.NoError(t, c.Apply(ctx, intent, client.FieldOwner(fieldOwner(gatewayControllerName)), client.ForceOwnership))
		}
	}
	requireDefaultHostnames := func(t *testing.T, gw *gatewayv1.Gateway) {
		t.Helper()
		canonical := gatewayv1.Hostname(gateways.gatewayCanonicalHostname(gw))
		for _, name := range []gatewayv1.SectionName{gatewayutil.DefaultHTTPListenerName, gatewayutil.DefaultHTTPSListenerName} {
			l := gatewayutil.GetListenerByName(gw.Spec.Listeners, name)
			require.NotNil(t, l, "listener %s", name)
			require.Equal(t, canonical, ptr.Deref(l.Hostname, ""), "listener %s", name)
		}
	}
	// legacyGateway saves a Gateway the way the operator saved one before
	// apply: a create, then an Update with the finalizer and default hostnames,
	// both under the binary's field manager.
	legacyGateway := func(t *testing.T, proxy *networkingv1alpha.HTTPProxy) {
		t.Helper()
		gw := desiredGateway(t, c, proxy)
		require.NoError(t, controllerutil.SetControllerReference(proxy, gw, c.Scheme()))
		require.NoError(t, c.Create(ctx, gw, client.FieldOwner("network-services")))
		prepareUpstreamGateway(gateways, gw)
		require.NoError(t, c.Update(ctx, gw, client.FieldOwner("network-services")))
	}

	t.Run("a new Gateway is created, prepared, then left alone", func(t *testing.T) {
		proxy := proxyWithHostnames("new", "a.example.test")
		_, result, err := proxies.saveGateway(ctx, cl, proxy, desiredGateway(t, c, proxy))
		require.NoError(t, err)
		require.Equal(t, controllerutil.OperationResultCreated, result)
		saveAsGatewayController(t, "new")
		before := read(t, "new")

		_, result, err = proxies.saveGateway(ctx, cl, proxy, desiredGateway(t, c, proxy))
		require.NoError(t, err)
		require.Equal(t, controllerutil.OperationResultNone, result)
		after := read(t, "new")
		require.Equal(t, before.ResourceVersion, after.ResourceVersion)
		requireDefaultHostnames(t, after)
		require.Contains(t, after.Finalizers, gatewayControllerFinalizer)
	})

	t.Run("a Gateway saved before apply keeps its default hostnames", func(t *testing.T) {
		proxy := proxyWithHostnames("legacy", "a.example.test")
		legacyGateway(t, proxy)
		grown := proxyWithHostnames("legacy", "a.example.test", "b.example.test")
		grown.UID = proxy.UID
		_, result, err := proxies.saveGateway(ctx, cl, grown, desiredGateway(t, c, grown))
		require.NoError(t, err)
		require.Equal(t, controllerutil.OperationResultUpdated, result)
		gw := read(t, "legacy")
		require.Len(t, gw.Spec.Listeners, 6)
		requireDefaultHostnames(t, gw)
		require.Contains(t, gw.Finalizers, gatewayControllerFinalizer)
	})

	t.Run("a removed hostname renumbers the listeners", func(t *testing.T) {
		proxy := proxyWithHostnames("renumber", "a.example.test", "b.example.test")
		legacyGateway(t, proxy)
		shrunk := proxyWithHostnames("renumber", "b.example.test")
		shrunk.UID = proxy.UID
		_, _, err := proxies.saveGateway(ctx, cl, shrunk, desiredGateway(t, c, shrunk))
		require.NoError(t, err)
		gw := read(t, "renumber")
		require.ElementsMatch(t, []gatewayv1.SectionName{"default-http", "default-https", "http-hostname-0", "https-hostname-0"}, listenerNames(gw))
		require.Equal(t, gatewayv1.Hostname("b.example.test"), *gatewayutil.GetListenerByName(gw.Spec.Listeners, "http-hostname-0").Hostname)
		requireDefaultHostnames(t, gw)
	})

	t.Run("a user's hostname on a default listener stays the user's", func(t *testing.T) {
		proxy := proxyWithHostnames("user")
		gw := desiredGateway(t, c, proxy)
		gw.Spec.Listeners[0].Hostname = ptr.To(gatewayv1.Hostname("user.example.test"))
		require.NoError(t, c.Create(ctx, gw, client.FieldOwner("datum-cloud-portal")))
		saveAsGatewayController(t, "user")
		saveAsGatewayController(t, "user")
		live := read(t, "user")
		require.Equal(t, gatewayv1.Hostname("user.example.test"), *gatewayutil.GetListenerByName(live.Spec.Listeners, "default-http").Hostname)
		require.Equal(t, gatewayv1.Hostname(gateways.gatewayCanonicalHostname(live)), *gatewayutil.GetListenerByName(live.Spec.Listeners, "default-https").Hostname)
	})

	t.Run("a stale intent does not touch a Gateway created again under the same name", func(t *testing.T) {
		proxy := proxyWithHostnames("again")
		_, _, err := proxies.saveGateway(ctx, cl, proxy, desiredGateway(t, c, proxy))
		require.NoError(t, err)
		stale := read(t, "again")
		require.NoError(t, c.Delete(ctx, stale.DeepCopy()))
		_, _, err = proxies.saveGateway(ctx, cl, proxy, desiredGateway(t, c, proxy))
		require.NoError(t, err)

		intent, missing := gateways.upstreamGatewayIntent(stale)
		require.True(t, missing)
		require.Error(t, c.Apply(ctx, intent, client.FieldOwner(fieldOwner(gatewayControllerName)), client.ForceOwnership))
		again := read(t, "again")
		require.NotEqual(t, stale.UID, again.UID)
		require.Empty(t, again.Finalizers)
		require.Nil(t, gatewayutil.GetListenerByName(again.Spec.Listeners, gatewayutil.DefaultHTTPListenerName).Hostname)
	})

	t.Run("a removal that lost a race is retried, not reported invalid", func(t *testing.T) {
		proxy := proxyWithHostnames("moved", "a.example.test", "b.example.test")
		legacyGateway(t, proxy)
		stale := read(t, "moved")
		live := stale.DeepCopy()
		live.Spec.Listeners = slices.DeleteFunc(live.Spec.Listeners, func(l gatewayv1.Listener) bool {
			return l.Name == "http-hostname-0" || l.Name == "https-hostname-0"
		})
		require.NoError(t, c.Update(ctx, live, client.FieldOwner("network-services")))
		want := stale.DeepCopy()
		want.Spec.Listeners = slices.DeleteFunc(want.Spec.Listeners, func(l gatewayv1.Listener) bool {
			return l.Name == "http-hostname-1" || l.Name == "https-hostname-1"
		})

		err := removeListeners(ctx, c, c, stale, want)
		require.Error(t, err)
		require.False(t, apierrors.IsInvalid(err), "got %v", err)
	})

	t.Run("a removal the API server rejects is reported invalid", func(t *testing.T) {
		proxy := proxyWithHostnames("rejected")
		legacyGateway(t, proxy)
		live := read(t, "rejected")
		err := removeListeners(ctx, c, c, live, &gatewayv1.Gateway{})
		require.True(t, apierrors.IsInvalid(err), "got %v", err)
	})
}
