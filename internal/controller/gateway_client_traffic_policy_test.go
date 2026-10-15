package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	cmv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
)

// The downstream cluster is a real API server here, with Envoy Gateway's
// ClientTrafficPolicy schema, so a policy the operator could not write fails.
func TestDownstreamClientTrafficPolicy(t *testing.T) {
	cl := startDownstreamEnv(t, append([]string{envoyGatewayCRDDir}, installedGatewayAPICRDPaths(t)...))
	ctx := context.Background()
	windows := config.DownstreamClientTrafficConfig{HTTP2: &envoygatewayv1alpha1.HTTP2Settings{
		InitialStreamWindowSize:     ptr.To(resource.MustParse("1Mi")),
		InitialConnectionWindowSize: ptr.To(resource.MustParse("16Mi")),
	}}
	withHTTP3 := windows
	withHTTP3.HTTP3 = &envoygatewayv1alpha1.HTTP3Settings{}

	t.Run("NotConfiguredWritesNothing", func(t *testing.T) {
		gw := createDownstreamGateway(t, ctx, cl, "a", httpListener(), httpsListener("https"))
		require.NoError(t, ensureDownstreamClientTrafficPolicy(ctx, cl, config.DownstreamClientTrafficConfig{}, gw))
		assertNoClientTrafficPolicy(t, ctx, cl, gw)
	})

	t.Run("TargetsEachHTTPSListenerOnly", func(t *testing.T) {
		gw := createDownstreamGateway(t, ctx, cl, "b",
			httpListener(), httpsListener("https"), httpsListener("custom-https"))
		require.NoError(t, ensureDownstreamClientTrafficPolicy(ctx, cl, windows, gw))
		policy := getClientTrafficPolicy(t, ctx, cl, gw)
		assert.Equal(t, []string{"https", "custom-https"}, sectionNames(policy, gw.Name))
		assert.Len(t, policy.Spec.TargetRefs, 2, "the HTTP listener on port 80 is never a target")
	})

	t.Run("CarriesTheConfiguredFieldsAndNoOthers", func(t *testing.T) {
		gw := createDownstreamGateway(t, ctx, cl, "c", httpsListener("https"))
		require.NoError(t, ensureDownstreamClientTrafficPolicy(ctx, cl, windows, gw))
		policy := getClientTrafficPolicy(t, ctx, cl, gw)
		require.NotNil(t, policy.Spec.HTTP2)
		assert.True(t, policy.Spec.HTTP2.InitialStreamWindowSize.Equal(resource.MustParse("1Mi")))
		assert.True(t, policy.Spec.HTTP2.InitialConnectionWindowSize.Equal(resource.MustParse("16Mi")))
		assert.Nil(t, policy.Spec.HTTP3, "http3 is not configured, so it is not written")

		require.NoError(t, ensureDownstreamClientTrafficPolicy(ctx, cl, withHTTP3, gw))
		assert.NotNil(t, getClientTrafficPolicy(t, ctx, cl, gw).Spec.HTTP3, "staging's http3 is carried")
	})

	t.Run("BelongsToTheGatewayAndCarriesItsUpstreamLabels", func(t *testing.T) {
		gw := createDownstreamGateway(t, ctx, cl, "d", httpsListener("https"))
		require.NoError(t, ensureDownstreamClientTrafficPolicy(ctx, cl, windows, gw))
		policy := getClientTrafficPolicy(t, ctx, cl, gw)
		assert.True(t, metav1.IsControlledBy(policy, gw), "the Gateway is the controller, so the policy goes with it")
		for _, key := range upstreamOwnerLabelKeys {
			assert.Equal(t, gw.Labels[key], policy.Labels[key], "label %s selects the policy for propagation", key)
		}
	})

	t.Run("FollowsTheGatewaysListeners", func(t *testing.T) {
		gw := createDownstreamGateway(t, ctx, cl, "e", httpsListener("https"), httpsListener("old-https"))
		require.NoError(t, ensureDownstreamClientTrafficPolicy(ctx, cl, windows, gw))
		gw.Spec.Listeners = []gatewayv1.Listener{httpsListener("https"), httpsListener("new-https")}
		require.NoError(t, cl.Update(ctx, gw))
		require.NoError(t, ensureDownstreamClientTrafficPolicy(ctx, cl, windows, gw))
		assert.Equal(t, []string{"https", "new-https"}, sectionNames(getClientTrafficPolicy(t, ctx, cl, gw), gw.Name))
	})

	t.Run("SecondPassWritesNothing", func(t *testing.T) {
		gw := createDownstreamGateway(t, ctx, cl, "f", httpsListener("https"))
		require.NoError(t, ensureDownstreamClientTrafficPolicy(ctx, cl, withHTTP3, gw))
		before := getClientTrafficPolicy(t, ctx, cl, gw).ResourceVersion
		require.NoError(t, ensureDownstreamClientTrafficPolicy(ctx, cl, withHTTP3, gw))
		assert.Equal(t, before, getClientTrafficPolicy(t, ctx, cl, gw).ResourceVersion,
			"an unchanged policy is not rewritten on every reconcile")
	})

	t.Run("EmptiedConfigDeletes", func(t *testing.T) {
		gw := createDownstreamGateway(t, ctx, cl, "g", httpsListener("https"))
		require.NoError(t, ensureDownstreamClientTrafficPolicy(ctx, cl, windows, gw))
		require.NoError(t, ensureDownstreamClientTrafficPolicy(ctx, cl, config.DownstreamClientTrafficConfig{}, gw))
		assertNoClientTrafficPolicy(t, ctx, cl, gw)
	})

	t.Run("NoHTTPSListenerDeletes", func(t *testing.T) {
		gw := createDownstreamGateway(t, ctx, cl, "h", httpListener(), httpsListener("https"))
		require.NoError(t, ensureDownstreamClientTrafficPolicy(ctx, cl, windows, gw))
		gw.Spec.Listeners = []gatewayv1.Listener{httpListener()}
		require.NoError(t, cl.Update(ctx, gw))
		require.NoError(t, ensureDownstreamClientTrafficPolicy(ctx, cl, windows, gw))
		assertNoClientTrafficPolicy(t, ctx, cl, gw)
	})

	t.Run("NeverDeletesAPolicyItDoesNotControl", func(t *testing.T) {
		gw := createDownstreamGateway(t, ctx, cl, "i", httpsListener("https"))
		foreign := &envoygatewayv1alpha1.ClientTrafficPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: gw.Name, Namespace: gw.Namespace},
			Spec: envoygatewayv1alpha1.ClientTrafficPolicySpec{
				PolicyTargetReferences: envoygatewayv1alpha1.PolicyTargetReferences{
					TargetRefs: []gatewayv1.LocalPolicyTargetReferenceWithSectionName{{
						LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
							Group: gatewayv1.GroupName, Kind: KindGateway, Name: gatewayv1.ObjectName(gw.Name),
						},
					}},
				},
				HTTP3: &envoygatewayv1alpha1.HTTP3Settings{},
			},
		}
		require.NoError(t, cl.Create(ctx, foreign))
		require.NoError(t, ensureDownstreamClientTrafficPolicy(ctx, cl, config.DownstreamClientTrafficConfig{}, gw))
		getClientTrafficPolicy(t, ctx, cl, gw)
	})

	t.Run("UnwrittenGatewayWritesNothing", func(t *testing.T) {
		gw := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "never-written", Namespace: "default"}}
		require.NoError(t, ensureDownstreamClientTrafficPolicy(ctx, cl, windows, gw))
		assertNoClientTrafficPolicy(t, ctx, cl, gw)
	})
}

// A downstream cluster without the ClientTrafficPolicy kind holds no such
// policy, so an estate that sets nothing reconciles there without an error.
func TestDownstreamClientTrafficPolicyWithoutTheKind(t *testing.T) {
	cl := startDownstreamEnv(t, installedGatewayAPICRDPaths(t))
	ctx := context.Background()
	gw := createDownstreamGateway(t, ctx, cl, "a", httpsListener("https"))
	assert.NoError(t, ensureDownstreamClientTrafficPolicy(ctx, cl, config.DownstreamClientTrafficConfig{}, gw))
}

// The reconcile writes the policy for the Gateway it has just written.
func TestEnsureDownstreamGatewayWritesClientTrafficPolicy(t *testing.T) {
	traffic := config.DownstreamClientTrafficConfig{HTTP3: &envoygatewayv1alpha1.HTTP3Settings{}}
	downstream, gw := reconcileOneGatewayWithClientTraffic(t, traffic)

	var policy envoygatewayv1alpha1.ClientTrafficPolicy
	require.NoError(t, downstream.Get(context.Background(), client.ObjectKeyFromObject(gw), &policy))
	assert.True(t, metav1.IsControlledBy(&policy, gw))
	var https []string
	for _, l := range gw.Spec.Listeners {
		if l.Protocol == gatewayv1.HTTPSProtocolType {
			https = append(https, string(l.Name))
		}
	}
	require.NotEmpty(t, https, "the fixture must program an HTTPS listener")
	assert.Equal(t, https, sectionNames(&policy, gw.Name))
}

// envoyGatewayCRDDir holds Envoy Gateway's CRDs, ClientTrafficPolicy's among them.
var envoyGatewayCRDDir = filepath.Join("..", "..", "config", "tools", "envoy-gateway", "charts",
	"gateway-helm-v0.0.0-latest", "gateway-helm", "crds", "generated")

// startDownstreamEnv starts an API server with the given CRDs and returns a
// client that knows the Gateway API and Envoy Gateway types.
func startDownstreamEnv(t *testing.T, crdPaths []string) client.Client {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}
	env := &envtest.Environment{CRDDirectoryPaths: crdPaths, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop() })

	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, gatewayv1.Install(s))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(s))
	cl, err := client.New(cfg, client.Options{Scheme: s})
	require.NoError(t, err)
	return cl
}

// createDownstreamGateway writes a Gateway as the operator writes one
// downstream: in its own namespace, with the upstream owner labels.
func createDownstreamGateway(t *testing.T, ctx context.Context, cl client.Client, name string, listeners ...gatewayv1.Listener) *gatewayv1.Gateway {
	t.Helper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-" + name}}
	require.NoError(t, cl.Create(ctx, ns))
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns.Name, Labels: map[string]string{
			downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-" + name,
			downstreamclient.UpstreamOwnerGroupLabel:       gatewayv1.GroupName,
			downstreamclient.UpstreamOwnerKindLabel:        "Gateway",
			downstreamclient.UpstreamOwnerNameLabel:        name,
			downstreamclient.UpstreamOwnerNamespaceLabel:   "upstream-" + name,
		}},
		Spec: gatewayv1.GatewaySpec{GatewayClassName: "datum-downstream-gateway", Listeners: listeners},
	}
	require.NoError(t, cl.Create(ctx, gw))
	return gw
}

// httpListener is the port-80 listener every downstream gateway carries.
func httpListener() gatewayv1.Listener {
	return gatewayv1.Listener{Name: "http", Port: 80, Protocol: gatewayv1.HTTPProtocolType,
		Hostname: ptr.To(gatewayv1.Hostname("http.example.test"))}
}

func httpsListener(name string) gatewayv1.Listener {
	return gatewayv1.Listener{Name: gatewayv1.SectionName(name), Port: 443, Protocol: gatewayv1.HTTPSProtocolType,
		Hostname: ptr.To(gatewayv1.Hostname(name + ".example.test")),
		TLS: &gatewayv1.ListenerTLSConfig{
			Mode:            ptr.To(gatewayv1.TLSModeTerminate),
			CertificateRefs: []gatewayv1.SecretObjectReference{{Name: "tls"}},
		}}
}

func getClientTrafficPolicy(t *testing.T, ctx context.Context, cl client.Client, gw *gatewayv1.Gateway) *envoygatewayv1alpha1.ClientTrafficPolicy {
	t.Helper()
	var policy envoygatewayv1alpha1.ClientTrafficPolicy
	require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(gw), &policy))
	return &policy
}

func assertNoClientTrafficPolicy(t *testing.T, ctx context.Context, cl client.Client, gw *gatewayv1.Gateway) {
	t.Helper()
	var policy envoygatewayv1alpha1.ClientTrafficPolicy
	err := cl.Get(ctx, client.ObjectKeyFromObject(gw), &policy)
	assert.True(t, apierrors.IsNotFound(err), "want no ClientTrafficPolicy, got %v", err)
}

// sectionNames returns the listener names a policy targets on one Gateway, in order.
func sectionNames(policy *envoygatewayv1alpha1.ClientTrafficPolicy, gateway string) []string {
	var names []string
	for _, ref := range policy.Spec.TargetRefs {
		if string(ref.Name) == gateway && string(ref.Kind) == "Gateway" && ref.SectionName != nil {
			names = append(names, string(*ref.SectionName))
		}
	}
	return names
}

// reconcileOneGatewayWithClientTraffic runs ensureDownstreamGateway once for a
// gateway with an HTTP and an HTTPS listener, as TestEnsureDownstreamGateway
// does, and returns the downstream client and the downstream Gateway written.
func reconcileOneGatewayWithClientTraffic(t *testing.T, traffic config.DownstreamClientTrafficConfig) (client.Client, *gatewayv1.Gateway) {
	t.Helper()
	testScheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(testScheme))
	require.NoError(t, gatewayv1.Install(testScheme))
	require.NoError(t, discoveryv1.AddToScheme(testScheme))
	require.NoError(t, networkingv1alpha.AddToScheme(testScheme))
	require.NoError(t, cmv1.AddToScheme(testScheme))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(testScheme))

	upstreamNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test", UID: uuid.NewUUID()}}
	testConfig := config.NetworkServicesOperator{Gateway: config.GatewayConfig{
		DownstreamGatewayClassName:            "test-suite",
		DownstreamHostnameAccountingNamespace: "default",
		TargetDomain:                          "test-suite.com",
		IPFamilies:                            []networkingv1alpha.IPFamily{networkingv1alpha.IPv4Protocol},
		DownstreamClientTraffic:               traffic,
	}}
	upstreamGateway := newGateway(testConfig, upstreamNamespace.Name, "test", func(*gatewayv1.Gateway) {})
	gatewayClass := &gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "test", UID: uuid.NewUUID()},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: "test"},
	}
	upstream := fake.NewClientBuilder().WithScheme(testScheme).
		WithObjects(upstreamGateway, upstreamNamespace, gatewayClass).
		WithStatusSubresource(upstreamGateway).Build()
	downstream := fake.NewClientBuilder().WithScheme(testScheme).
		WithStatusSubresource(&gatewayv1.Gateway{}).Build()

	ctx := log.IntoContext(context.Background(), zap.New(zap.UseFlagOptions(&zap.Options{Development: true})))
	reconciler := &GatewayReconciler{
		mgr:               &fakeMockManager{cl: upstream},
		Config:            testConfig,
		DownstreamCluster: &fakeCluster{cl: downstream},
	}
	strategy := downstreamclient.NewMappedNamespaceResourceStrategy("test", upstream, downstream)
	prepareUpstreamGateway(reconciler, upstreamGateway)
	result, gw := reconciler.ensureDownstreamGateway(ctx, "test-suite", upstream, upstreamGateway, strategy)
	require.NoError(t, result.Err)
	require.NotNil(t, gw)
	return downstream, gw
}
