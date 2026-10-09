//go:build prototype

// SPDX-License-Identifier: AGPL-3.0-only

package prototype

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayac "sigs.k8s.io/gateway-api/applyconfiguration/apis/v1"

	"go.datum.net/network-services-operator/internal/config"
	gatewayutil "go.datum.net/network-services-operator/internal/util/gateway"
)

// canonicalHost is the default listeners' hostname the gateway controller
// derives from the Gateway's UID.
const canonicalHost = "abc123.datumproxy.net"

func gatewayConfig() config.GatewayConfig {
	cfg := config.NetworkServicesOperator{}
	config.SetObjectDefaults_NetworkServicesOperator(&cfg)
	return cfg.Gateway
}

// proxyGateway builds the Gateway the HTTPProxy controller wants, the way
// httpproxy_controller.go builds it: the default listeners without hostnames,
// then one HTTP and one HTTPS listener per hostname, named by the hostname's
// position.
func proxyGateway(name string, uid types.UID, hostnames ...string) *gatewayv1.Gateway {
	gw := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name, UID: uid}}
	gw.Spec.GatewayClassName = "datum-external-global-proxy"
	cfg := gatewayConfig()
	gatewayutil.SetDefaultListeners(gw, cfg)
	same := &gatewayv1.AllowedRoutes{Namespaces: &gatewayv1.RouteNamespaces{From: ptr.To(gatewayv1.NamespacesFromSame)}}
	for i, h := range hostnames {
		gw.Spec.Listeners = append(gw.Spec.Listeners,
			gatewayv1.Listener{
				Name: gatewayv1.SectionName(fmt.Sprintf("http-hostname-%d", i)), Protocol: gatewayv1.HTTPProtocolType,
				Port: 80, Hostname: ptr.To(gatewayv1.Hostname(h)), AllowedRoutes: same,
			},
			gatewayv1.Listener{
				Name: gatewayv1.SectionName(fmt.Sprintf("https-hostname-%d", i)), Protocol: gatewayv1.HTTPSProtocolType,
				Port: 443, Hostname: ptr.To(gatewayv1.Hostname(h)), AllowedRoutes: same,
				TLS: &gatewayv1.ListenerTLSConfig{Mode: ptr.To(gatewayv1.TLSModeTerminate), Options: cfg.ListenerTLSOptions},
			})
	}
	return gw
}

// applyProxyGateway saves the HTTPProxy controller's intent: it first removes,
// by tested patches, the listeners its intent no longer has, then applies. An
// empty UID creates the Gateway.
func applyProxyGateway(t *testing.T, e *env, want *gatewayv1.Gateway) error {
	ctx := context.Background()
	live := &gatewayv1.Gateway{}
	err := e.c.Get(ctx, client.ObjectKeyFromObject(want), live)
	if err == nil {
		keep := func(name string) bool {
			return slices.ContainsFunc(want.Spec.Listeners, func(l gatewayv1.Listener) bool { return string(l.Name) == name })
		}
		if err := e.patcher("httpproxy", nil).RemoveListeners(ctx, live, keep); err != nil {
			return err
		}
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	return applyProxyIntent(t, e, want)
}

// applyProxyIntent applies the HTTPProxy controller's intent alone, without
// removing listeners first. The controls use it.
func applyProxyIntent(t *testing.T, e *env, want *gatewayv1.Gateway) error {
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(want)
	require.NoError(t, err)
	u := &unstructured.Unstructured{Object: obj}
	u.SetAPIVersion(gatewayv1.GroupName + "/v1")
	u.SetKind("Gateway")
	unstructured.RemoveNestedField(u.Object, "status")
	unstructured.RemoveNestedField(u.Object, "metadata", "creationTimestamp")
	if want.UID == "" {
		unstructured.RemoveNestedField(u.Object, "metadata", "uid")
	}
	return e.c.Apply(context.Background(), client.ApplyConfigurationFromUnstructured(u),
		client.FieldOwner(FieldManager("httpproxy")), client.ForceOwnership)
}

// applyGatewayHostnames is the gateway controller's intent: the canonical
// hostname on each default listener whose hostname is empty or already the
// canonical one. A hostname someone else set stays theirs.
func applyGatewayHostnames(e *env, live *gatewayv1.Gateway) error {
	spec := gatewayac.GatewaySpec()
	defaults := []gatewayv1.SectionName{gatewayutil.DefaultHTTPListenerName, gatewayutil.DefaultHTTPSListenerName}
	for _, name := range defaults {
		l := gatewayutil.GetListenerByName(live.Spec.Listeners, name)
		if l == nil || (l.Hostname != nil && *l.Hostname != canonicalHost) {
			continue
		}
		spec.WithListeners(gatewayac.Listener().WithName(name).WithHostname(canonicalHost))
	}
	ac := gatewayac.Gateway(live.Name, live.Namespace).WithUID(live.UID).WithSpec(spec)
	return e.c.Apply(context.Background(), ac, client.FieldOwner(FieldManager("gateway")), client.ForceOwnership)
}

func gatewayReconcile(e *env, key types.NamespacedName) error {
	live := &gatewayv1.Gateway{}
	if err := e.c.Get(context.Background(), key, live); err != nil {
		return err
	}
	return applyGatewayHostnames(e, live)
}

func listenersOf(gw *gatewayv1.Gateway) (names []string, defaultHostnames bool) {
	names = make([]string, 0, len(gw.Spec.Listeners))
	defaultHostnames = true
	for _, l := range gw.Spec.Listeners {
		names = append(names, string(l.Name))
		isDefault := l.Name == gatewayutil.DefaultHTTPListenerName || l.Name == gatewayutil.DefaultHTTPSListenerName
		if isDefault && ptr.Deref(l.Hostname, "") != canonicalHost {
			defaultHostnames = false
		}
	}
	return names, defaultHostnames
}

func gatewayNew(t *testing.T, e *env) {
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "default", Name: "new"}
	require.NoError(t, applyProxyGateway(t, e, proxyGateway(key.Name, "")), "the first apply creates the Gateway")
	gw := &gatewayv1.Gateway{}
	require.NoError(t, e.c.Get(ctx, key, gw))

	// The gateway controller cannot add a hostname to a listener that does not
	// exist yet: the entry would have no port or protocol.
	early := gatewayac.Gateway(key.Name, key.Namespace).WithUID(gw.UID).WithSpec(gatewayac.GatewaySpec().WithListeners(
		gatewayac.Listener().WithName("not-yet").WithHostname(canonicalHost)))
	err := e.c.Apply(ctx, early, client.FieldOwner(FieldManager("gateway")), client.ForceOwnership)
	require.Error(t, err)
	t.Logf("RESULT P4 new: a hostname for a listener that does not exist -> %v", err)

	var failures atomic.Int64
	for range 10 {
		if gatewayReconcile(e, key) != nil {
			failures.Add(1)
		}
		if applyProxyGateway(t, e, proxyGateway(key.Name, gw.UID, "app.example.test")) != nil {
			failures.Add(1)
		}
	}
	require.NoError(t, e.c.Get(ctx, key, gw))
	names, ok := listenersOf(gw)
	require.True(t, ok)
	t.Logf("RESULT P4 new: 20 saves by two controllers, failures=%d; listeners=%v; default hostnames kept=%t",
		failures.Load(), names, ok)

	// Control: the HTTPProxy controller must never leave out a listener whose
	// hostname the gateway controller owns (kubernetes/kubernetes#128102).
	only := proxyGateway(key.Name, gw.UID)
	only.Spec.Listeners = only.Spec.Listeners[:1]
	err = applyProxyIntent(t, e, only)
	require.Error(t, err)
	t.Logf("RESULT P4 control leave-out: the HTTPProxy controller drops a co-owned default listener -> %v", err)
}

// legacyGateway saves a Gateway the way the operator saves one before the
// change: the HTTPProxy controller's create, then the gateway controller's
// Update with its finalizer and the default hostnames, all under the legacy
// managers. Another writer's annotation is recorded under the legacy manager
// too.
func legacyGateway(t *testing.T, e *env, name string, hostnames ...string) *gatewayv1.Gateway {
	ctx := context.Background()
	gw := proxyGateway(name, "", hostnames...)
	require.NoError(t, e.c.Create(ctx, gw, client.FieldOwner(legacyOld)))
	controllerutil.AddFinalizer(gw, finGateway)
	gw.Annotations = map[string]string{"example.com/other-writer": "kept"}
	for i := range gw.Spec.Listeners[:2] {
		gw.Spec.Listeners[i].Hostname = ptr.To(gatewayv1.Hostname(canonicalHost))
	}
	require.NoError(t, e.c.Update(ctx, gw, client.FieldOwner(legacyPresent)))
	return gw
}

// watchHostnames counts every version of the Gateway in which a default
// listener lacks its hostname.
func watchHostnames(t *testing.T, e *env, ctx context.Context, name string) *atomic.Int64 {
	wc, err := client.NewWithWatch(e.test.Config, client.Options{Scheme: e.scheme})
	require.NoError(t, err)
	w, err := wc.Watch(ctx, &gatewayv1.GatewayList{}, client.InNamespace("default"),
		client.MatchingFields{"metadata.name": name})
	require.NoError(t, err)
	var gaps atomic.Int64
	go func() {
		for ev := range w.ResultChan() {
			gw, ok := ev.Object.(*gatewayv1.Gateway)
			if !ok || ev.Type != watch.Modified {
				continue
			}
			if _, set := listenersOf(gw); !set {
				gaps.Add(1)
			}
		}
	}()
	return &gaps
}

func gatewayLegacy(t *testing.T, e *env) {
	for _, order := range []string{"httpproxy-first", "gateway-first"} {
		ctx, cancel := context.WithCancel(context.Background())
		key := types.NamespacedName{Namespace: "default", Name: "legacy-" + order}
		gaps := watchHostnames(t, e, ctx, key.Name)
		gw := legacyGateway(t, e, key.Name, "app.example.test")
		steps := []func() error{
			func() error { return applyProxyGateway(t, e, proxyGateway(key.Name, gw.UID)) },
			func() error { return gatewayReconcile(e, key) },
		}
		if order == "gateway-first" {
			slices.Reverse(steps)
		}
		for _, step := range steps {
			require.NoError(t, step())
		}
		require.NoError(t, e.c.Get(ctx, key, gw))
		names, set := listenersOf(gw)
		time.Sleep(300 * time.Millisecond) // let the watch see the last version
		require.True(t, set)
		require.Zero(t, gaps.Load(), "a version lacked a default hostname")
		require.ElementsMatch(t, []string{gatewayutil.DefaultHTTPListenerName, gatewayutil.DefaultHTTPSListenerName}, names)
		require.Contains(t, gw.Finalizers, finGateway, "the gateway controller's finalizer is gone")
		require.Equal(t, "kept", gw.Annotations["example.com/other-writer"], "the other writer's annotation is gone")
		t.Logf("RESULT P4 legacy order=%s: listeners=%v; versions without a default hostname=%d; "+
			"the legacy-recorded finalizer and annotation kept", order, names, gaps.Load())
		cancel()
	}
}

// gatewayHostnameRemoved: an HTTPProxy with hostnames [a, b] loses a. Listeners
// are named by position, so the intent renames b's listeners to position 0.
func gatewayHostnameRemoved(t *testing.T, e *env) {
	ctx := context.Background()
	for _, mode := range []string{"apply only", "remove then apply"} {
		objName := map[string]string{"apply only": "renumber-control", "remove then apply": "renumber"}[mode]
		key := types.NamespacedName{Namespace: "default", Name: objName}
		gw := legacyGateway(t, e, key.Name, "a.example.test", "b.example.test")
		want := proxyGateway(key.Name, gw.UID, "b.example.test")
		var err error
		if mode == "apply only" {
			err = applyProxyIntent(t, e, want)
		} else {
			err = applyProxyGateway(t, e, want)
		}
		require.NoError(t, e.c.Get(ctx, key, gw))
		names, _ := listenersOf(gw)
		t.Logf("RESULT P4 hostname removed, %s: err=%v; listeners=%v", mode, err, names)
		if mode == "apply only" {
			require.Error(t, err, "the control should show the uniqueness rejection")
			continue
		}
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"default-http", "default-https", "http-hostname-0", "https-hostname-0"}, names)
		i := slices.IndexFunc(gw.Spec.Listeners, func(l gatewayv1.Listener) bool { return l.Name == "http-hostname-0" })
		require.Equal(t, "b.example.test", string(ptr.Deref(gw.Spec.Listeners[i].Hostname, "")))
	}
}

// gatewayUserHostname: a user's own Gateway sets a hostname on a default
// listener. The gateway controller keeps setting the canonical hostname only
// where it is empty or already canonical, so the user's hostname stays, and the
// canonical one does not flap.
func gatewayUserHostname(t *testing.T, e *env) {
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "default", Name: "user-gateway"}
	gw := proxyGateway(key.Name, "")
	gw.Spec.Listeners[0].Hostname = ptr.To(gatewayv1.Hostname("user.example.test"))
	require.NoError(t, e.c.Create(ctx, gw, client.FieldOwner("datum-cloud-portal")))
	for range 10 {
		require.NoError(t, gatewayReconcile(e, key))
	}
	require.NoError(t, e.c.Get(ctx, key, gw))
	http := gatewayutil.GetListenerByName(gw.Spec.Listeners, gatewayutil.DefaultHTTPListenerName)
	https := gatewayutil.GetListenerByName(gw.Spec.Listeners, gatewayutil.DefaultHTTPSListenerName)
	require.Equal(t, "user.example.test", string(ptr.Deref(http.Hostname, "")))
	require.Equal(t, canonicalHost, string(ptr.Deref(https.Hostname, "")))
	t.Logf("RESULT P4 user hostname: after 10 passes default-http=%s (the user's), default-https=%s (canonical, stable)",
		ptr.Deref(http.Hostname, ""), ptr.Deref(https.Hostname, ""))
}

// gatewayGone: an apply that carries the UID of a deleted Gateway, or of one
// deleted and created again, creates nothing, and Gone tells it from a conflict.
func gatewayGone(t *testing.T, e *env) {
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "default", Name: "gone"}
	require.NoError(t, applyProxyGateway(t, e, proxyGateway(key.Name, "")))
	stale := &gatewayv1.Gateway{}
	require.NoError(t, e.c.Get(ctx, key, stale))
	require.NoError(t, e.c.Delete(ctx, stale.DeepCopy()))
	require.Eventually(t, func() bool { return apierrors.IsNotFound(e.c.Get(ctx, key, &gatewayv1.Gateway{})) },
		10*time.Second, 50*time.Millisecond)
	err := applyGatewayHostnames(e, stale)
	require.True(t, Gone(ctx, e.c, stale, err), "want gone, got %v", err)
	require.True(t, apierrors.IsNotFound(e.c.Get(ctx, key, &gatewayv1.Gateway{})), "the apply created the Gateway")
	t.Logf("RESULT P4 deleted: apply with the old UID -> %v; nothing created", err)

	require.NoError(t, applyProxyGateway(t, e, proxyGateway(key.Name, "")))
	err = applyGatewayHostnames(e, stale)
	require.True(t, Gone(ctx, e.c, stale, err), "want gone, got %v", err)
	t.Logf("RESULT P4 created again: apply with the old UID -> %v", err)
}

// gatewayOldCode: during a rollout the old HTTPProxy controller code saves the
// Gateway with Update while the new code applies.
func gatewayOldCode(t *testing.T, e *env) {
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "default", Name: "old-code"}
	gw := legacyGateway(t, e, key.Name, "app.example.test")
	var failures, rejected, landed atomic.Int64
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if applyProxyGateway(t, e, proxyGateway(key.Name, gw.UID, "app.example.test")) != nil {
				failures.Add(1)
			}
		})
		wg.Go(func() {
			if gatewayReconcile(e, key) != nil {
				failures.Add(1)
			}
		})
		wg.Go(func() {
			old := &gatewayv1.Gateway{}
			if e.c.Get(ctx, key, old) != nil {
				return
			}
			old.Spec.Listeners[2].Port = 8080 // the old code's intent differs
			switch err := e.c.Update(ctx, old, client.FieldOwner(legacyPresent)); {
			case apierrors.IsConflict(err):
				rejected.Add(1)
			case err == nil:
				landed.Add(1)
			}
		})
	}
	wg.Wait()
	require.NoError(t, applyProxyGateway(t, e, proxyGateway(key.Name, gw.UID, "app.example.test")))
	require.NoError(t, e.c.Get(ctx, key, gw))
	_, set := listenersOf(gw)
	require.True(t, set)
	require.EqualValues(t, 80, gw.Spec.Listeners[2].Port, "the new code's intent did not win on its next apply")
	t.Logf("RESULT P4 old code at once: 60 concurrent saves; new-code failures=%d; old-code Updates landed=%d "+
		"rejected=%d; the next apply restored the new intent; default hostnames kept",
		failures.Load(), landed.Load(), rejected.Load())
}
