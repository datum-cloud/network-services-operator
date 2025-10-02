// SPDX-License-Identifier: AGPL-3.0-only
//go:build conformance

package gatewayapi

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	envoygatewaye2e "github.com/envoyproxy/gateway/test/e2e"
	envoygatewaye2etests "github.com/envoyproxy/gateway/test/e2e/tests"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrl "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/gateway-api/conformance"
	"sigs.k8s.io/gateway-api/conformance/tests"
	"sigs.k8s.io/gateway-api/conformance/utils/flags"
	"sigs.k8s.io/gateway-api/conformance/utils/kubernetes"
	"sigs.k8s.io/gateway-api/conformance/utils/suite"
	"sigs.k8s.io/gateway-api/conformance/utils/tlog"
	"sigs.k8s.io/gateway-api/pkg/features"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

var (
	infraKubeconfig  = flag.String("infra-kubeconfig", "", "Path to the kubeconfig file for the infrastructure cluster used by the conformance suite")
	infraNodeAddress = flag.String("infra-node-address", "127.0.0.1", "Address that exposes the infra Gateway (for example, the Node IP for a NodePort)")
	infraHTTPPort    = flag.Int("infra-http-port", 30080, "Port exposing the infra Gateway HTTP port")
	infraHTTPSPort   = flag.Int("infra-https-port", 30443, "Port exposing the infra Gateway HTTP port")
)

const (
	managementInfraNamespace = "gateway-conformance-infra"
)

func TestGatewayAPIConformance(t *testing.T) {
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))

	opts := conformance.DefaultOptions(t)

	envoyManifests := NewTweakedFS(envoygatewaye2e.Manifests)

	opts.ManifestFS = append([]fs.FS{Manifests}, opts.ManifestFS...)
	opts.ManifestFS = append(opts.ManifestFS, envoyManifests)
	opts.SupportedFeatures = sets.New(
		features.SupportGateway,
		features.SupportHTTPRoute,
		features.SupportHTTPRouteBackendProtocolH2C,
		features.SupportHTTPRouteBackendProtocolWebSocket,
		features.SupportHTTPRouteMethodMatching,
		features.SupportHTTPRouteQueryParamMatching,
		features.SupportHTTPRoutePathRedirect,
		features.SupportHTTPRoutePortRedirect,
		features.SupportHTTPRouteSchemeRedirect,
		features.SupportHTTPRouteBackendRequestHeaderModification,
		features.SupportHTTPRouteResponseHeaderModification,
		features.SupportHTTPRouteHostRewrite,
		features.SupportHTTPRoutePathRewrite,
		features.SupportHTTPRouteRequestTimeout,
	)

	if flags.RunTest != nil {
		opts.RunTest = *flags.RunTest
	}

	nodePortDialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		var localAddr string
		if strings.HasSuffix(addr, ":80") {
			localAddr = fmt.Sprintf("%s:%d", *infraNodeAddress, *infraHTTPPort)
		} else {
			localAddr = fmt.Sprintf("%s:%d", *infraNodeAddress, *infraHTTPSPort)
		}
		return (&net.Dialer{}).DialContext(ctx, network, localAddr)
	}

	opts.RoundTripper = &NodePortRoundTripper{
		Debug:         opts.Debug,
		TimeoutConfig: opts.TimeoutConfig,
		DialContext:   nodePortDialer,
	}

	// Override default transport, as some Envoy Gateway tests leverage the
	// default HTTP client, and we're able to intercept those requests by doing this.
	http.DefaultTransport = &http.Transport{
		DialContext: nodePortDialer,
	}

	tests := collectTests()

	logSuiteConfiguration(t, opts, tests)

	cSuite, err := suite.NewConformanceTestSuite(opts)
	if err != nil {
		t.Fatalf("initialising conformance suite: %v", err)
	}

	cSuite.Setup(t, tests)

	utilruntime.Must(networkingv1alpha.AddToScheme(cSuite.Client.Scheme()))
	utilruntime.Must(envoygatewayv1alpha1.AddToScheme(cSuite.Client.Scheme()))
	envoyManifests.InitYAMLSerializer(cSuite.Client.Scheme())

	infraNamespaceName, err := deriveInfraNamespaceName(context.Background(), cSuite.Client, managementInfraNamespace)

	if err != nil {
		t.Fatalf("deriving infra namespace name: %v", err)
	}

	tweakEnvoyGatewayTests(envoyManifests, infraNamespaceName)

	// Set the test domain to be verified
	var domain networkingv1alpha.Domain
	domainObjectKey := types.NamespacedName{
		Namespace: "gateway-conformance-infra",
		Name:      "second-example.org",
	}
	if err := cSuite.Client.Get(context.Background(), domainObjectKey, &domain); err != nil {
		t.Fatalf("fetching test domain: %v", err)
	}

	apimeta.SetStatusCondition(&domain.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.DomainConditionVerifiedDNS,
		Status:             metav1.ConditionTrue,
		Reason:             networkingv1alpha.DomainReasonVerified,
		Message:            "The Domain has not been verified",
		LastTransitionTime: metav1.Now(),
	})
	if err := cSuite.Client.Status().Update(context.Background(), &domain); err != nil {
		t.Fatalf("updating test domain status: %v", err)
	}

	if err := ensureInfraBackends(t, cSuite, *infraKubeconfig, infraNamespaceName); err != nil {
		t.Fatalf("preparing infra cluster: %v", err)
	}

	if opts.RunTest != "" {
		tlog.Logf(t, "Running Gateway API conformance test %s", opts.RunTest)
	} else {
		tlog.Logf(t, "Running %d Gateway API conformance tests", len(tests))
	}

	if err := cSuite.Run(t, tests); err != nil {
		t.Fatalf("running Gateway API conformance suite: %v", err)
	}
}

var skipTests = sets.New(
	// NSO does not permit allowedRoutes from All namespaces today.
	"GatewayInvalidRouteKind",
	"GatewayObservedGenerationBump",

	// NSO does not support cross namespace references
	"HTTPRouteCrossNamespace",
	"HTTPRouteInvalidCrossNamespaceParentRef",
	"MultiReferenceGrantsSameNamespace",

	// NSO does not currently support certificateRefs on listeners.
	"GatewayInvalidTLSConfiguration",
	"GatewayModifyListeners",

	// Consider a configurable default listener name so tests that define listeners
	// without hostnames can run.
	// This test also expects to use certificateRefs, though.
	"GatewayWithAttachedRoutes",

	// No support for wildcard hostnames today.
	"HTTPRouteHostnameIntersection",
	"HTTPRouteListenerHostnameMatching",

	// No support for hostnames on routes today.
	"HTTPRouteHTTPSListener",
	"HTTPRouteMatchingAcrossRoutes",
	"HTTPRouteRewriteHost",

	// Validation webhook rejects invalid backend refs
	"HTTPRouteInvalidBackendRefUnknownKind",

	// Test sets the namespace on the backendRef
	"HTTPRouteInvalidNonExistentBackendRef",

	// NSO does not support Service backendRefs.
	"HTTPRouteServiceTypes",

	// Need to implement NoMatchingParent reason for Accepted condition
	// See: https://github.com/kubernetes-sigs/gateway-api/blob/40be951cebe5cb3f0365fe6951403de4ff8b9f33/conformance/tests/httproute-invalid-parentref-not-matching-section-name.go#L48
	"HTTPRouteInvalidParentRefNotMatchingSectionName",

	// Need to find a way to intercept websocket dialing.
	"HTTPRouteBackendProtocolWebSocket",

	// Test is flaky due to using the same HTTPRoute name used in HTTPRouteRequestHeaderModifier
	"HTTPRouteBackendRequestHeaderModifier",

	// Envoy Gateway tests

	// Not relevant to test from NSO
	"GatewayInfraResource",
	"EnvoyProxyHPA",
	"EnvoyProxyDaemonSet",
	"EnvoyProxyCustomName",
	"EnvoyPatchPolicy",
	"EnvoyPatchPolicyXDSNameSchemeV2",
	"ControlPlane",
	"OpenTelemetryAccessLogJSON",
	"OpenTelemetryAccessLogJSONAsDefault",
	"OpenTelemetryTextAccessLog",
	"FileAccessLog",
	"ALS",
	"BackendDualStack",
	"GatewayInvalidParameterTest",
	"GatewayWithEnvoyProxy",
	"EnvoyGatewayCustomSecurityContextUserid",
	"MetricWorkqueueAndRestclientTest",

	// Features in Envoy Gateway not supported via NSO either as a result of not
	// allowing ClientTrafficPolicy to be set, or usage of features like fault
	// injection that are not permitted.
	"AuthzWithClientIP",
	"AuthzWithClientIPTrustedCIDRs",
	"ClientMTLS",
	"ClientTimeout",
	"ConnectionLimit",
	"DatadogTracing",
	"DynamicResolverBackend",
	"DynamicResolverBackendWithTLS",
	"EnvoyGatewayRoutingType",
	"ExtProc",
	"Fault",
	"GRPCExtAuth",
	"HeaderSettings",
	"HTTP3",
	"HTTPBackendExtAuth",
	"HTTPExtAuth",
	"HTTPRouteDualStack",
	"ListenerHealthCheck",
	"LocalRateLimit",
	"LocalRateLimitDistinctCIDR",
	"LocalRateLimitDistinctHeader",
	"OpenTelemetryTracing",
	"PreserveCase",
	"PreserveRouteOrder",
	"RateLimitHeadersDisabled",
	"WasmOCIImageCodeSource",
	"ZipkinTracing",
	"ProxyMetrics",
	"WasmHTTPCodeSource",
	"MetricCompressor",
	"StatName",
	"ZoneAwareRouting",

	// Will add support when Gateway API 1.4.0 is released and BackendTLS moves to
	// v1.
	"BackendTLS",
	"BackendTLSSettings",
	"EnvoyGatewayBackend",
	"JWTBackendRemoteJWKS",

	// Active health checks are not permitted today
	"BackendHealthCheckActiveHTTP",
	"BackendPanicThresholdHTTPTest",

	// Can't get at HTTP client to forward traffic to the local service
	"BTPTimeout",
	"Compression",

	// No support for hostnames on routes today.
	"BackendTrafficPolicyMatchExpression",
	"FailedBackendTrafficPolicyDirectResponse",
	"LuaHTTP",
	"FailedSecurityPolicyDirectResponse",

	// Retries not permitted
	"BackendUpgrade",

	// Expects to use the `Name` field on rules, which does not exist in v1.3.0
	// of the Gateway API.
	"APIKeyAuth",

	// Expects to define Gateway and EnvoyProxy resources that wire up tracing.
	"BTPTracing",

	// No GRPC support yet, and the Envoy Gateway suite doesn't use or define
	// GRPC feature flags to disable these.
	"GRPCRouteBackendFQDNTest",
	"GRPCRouteBackendIPTest",

	// CORS filter is experimental, and not in the standard CRD.
	"CORSFromHTTPCORSFilter",
	// sessionPersistence is experimental
	"HeaderBasedSessionPersistence",
	"CookieBasedSessionPersistence",

	// Racy with CredentialInjectionBackendFilter
	"CredentialInjection",

	// Expects to create a Deployment, which would need to go into the infra
	// control plane
	"RoundRobinLoadBalancing",
	"SourceIPBasedConsistentHashLoadBalancing",
	"HeaderBasedConsistentHashLoadBalancing",
	"CookieBasedConsistentHashLoadBalancing",
	"OIDC with BackendCluster",
	"OIDC",
	"EndpointOverrideLoadBalancing",
	"MetricCompressor",

	// No global rate limiting
	"RateLimitCIDRMatch",
	"RateLimitHeaderMatch",
	"RateLimitBasedJwtClaims",
	"RateLimitHeadersAndCIDRMatch",
	"RateLimitGlobalSharedCidrMatch",
	"RateLimitGlobalSharedGatewayHeaderMatch",
	"RateLimitGlobalMergeTest",
	"GlobalRateLimitHeaderInvertMatch",
	"RateLimitMultipleListeners",
	"UsageRateLimit",

	// No retry support
	"Retry",

	// No TCP Routes
	"TCPRoute",
	"TCPRouteBackend",

	// No TLS Routes
	"TLSRouteBackendFQDNTest",
	"TLSRouteBackendIP",

	// No UDP Routes
	"UDPRoute",
	"UDPRouteBackendFQDNTest",
	"UDPRouteBackendIP",
)

func collectTests() []suite.ConformanceTest {
	t := append(slices.Clone(tests.ConformanceTests), envoygatewaye2etests.ConformanceTests...)

	return slices.DeleteFunc(t, func(test suite.ConformanceTest) bool {
		return skipTests.Has(test.ShortName)
	})
}

func logSuiteConfiguration(t *testing.T, opts suite.ConformanceOptions, selected []suite.ConformanceTest) {
	selectedNames := make([]string, len(selected))
	for i, test := range selected {
		selectedNames[i] = test.ShortName
	}
	slices.Sort(selectedNames)

	featuresList := opts.SupportedFeatures.UnsortedList()
	slices.Sort(featuresList)
	tlog.Logf(t, "GatewayClass: %s", opts.GatewayClassName)
	tlog.Logf(t, "Cleanup base resources: %t", opts.CleanupBaseResources)
	tlog.Logf(t, "Debug output: %t", opts.Debug)
	tlog.Logf(t, "Supported features: %v", featuresList)
	tlog.Logf(t, "Selected tests: %v", selectedNames)
}

func ensureInfraBackends(t *testing.T, suite *suite.ConformanceTestSuite, kubeconfigPath, infraNamespaceName string) error {

	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("building infra kubeconfig: %w", err)
	}

	infraClient, err := client.New(cfg, client.Options{})
	if err != nil {
		return fmt.Errorf("creating infra client: %w", err)
	}

	suite.Applier.MustApplyWithCleanup(
		t,
		infraClient,
		suite.TimeoutConfig,
		"base/infra-manifests-static-ns.yaml",
		suite.Cleanup,
	)

	suite.Applier.MustApplyWithCleanup(
		t,
		client.NewNamespacedClient(infraClient, infraNamespaceName),
		suite.TimeoutConfig,
		"base/infra-manifests.yaml",
		suite.Cleanup,
	)

	secret := kubernetes.MustCreateSelfSignedCertSecret(t, infraNamespaceName, "tls-passthrough-checks-certificate", []string{"abc.example.com"})
	suite.Applier.MustApplyObjectsWithCleanup(t, infraClient, suite.TimeoutConfig, []client.Object{secret}, suite.Cleanup)

	kubernetes.NamespacesMustBeReady(t, infraClient, suite.TimeoutConfig, []string{infraNamespaceName, managementInfraNamespace})

	return nil
}

func deriveInfraNamespaceName(ctx context.Context, c client.Client, managementNamespace string) (string, error) {
	var ns corev1.Namespace
	if err := c.Get(ctx, types.NamespacedName{Name: managementNamespace}, &ns); err != nil {
		return "", err
	}

	return fmt.Sprintf("ns-%s", ns.GetUID()), nil
}

func tweakEnvoyGatewayTests(manifests *TweakedFS, infraNamespaceName string) {
	manifests.AddTweak(
		"*",
		"v1",
		"ConfigMap",
		wildcard,
		wildcard,
		setGatewaySyncLabels,
	)

	manifests.AddTweak(
		"*",
		"v1",
		"Secret",
		wildcard,
		wildcard,
		setGatewaySyncLabels,
	)

	manifests.AddTweak(
		"testdata/jwt.yaml",
		"gateway.envoyproxy.io/v1alpha1",
		"SecurityPolicy",
		"gateway-conformance-infra",
		"jwt-example",
		rewriteTargetRef,
	)

	manifests.AddTweak(
		"testdata/compression.yaml",
		"gateway.envoyproxy.io/v1alpha1",
		"BackendTrafficPolicy",
		"gateway-conformance-infra",
		"compression",
		rewriteTargetRef,
	)

	manifests.AddTweak(
		"testdata/response-override.yaml",
		"gateway.envoyproxy.io/v1alpha1",
		"BackendTrafficPolicy",
		"gateway-conformance-infra",
		"response-override",
		rewriteTargetRef,
	)

	// This test is covering the ability to rewrite the upstream host header
	// based on a request header. We tweak it to set a specific hostname in the case
	// that a header isn't provided by the test suite. This is likely now not
	// testing what's intended by the upstream suite, but we consider that ok
	// given this operator builds upon it and we'd like to use as much of the test
	// as possible.
	manifests.AddTweak(
		"testdata/httproute-rewrite-host.yaml",
		"gateway.networking.k8s.io/v1",
		"HTTPRoute",
		"gateway-conformance-infra",
		"rewrite-host",
		func(uObj *unstructured.Unstructured) {
			rules, _, _ := unstructured.NestedSlice(uObj.Object, "spec", "rules")

			backendRule := rules[1].(map[string]any)

			filter := map[string]any{
				"type": "URLRewrite",
				"urlRewrite": map[string]any{
					"hostname": "infra-backend-v1.gateway-conformance-infra.svc.cluster.local",
				},
			}
			unstructured.SetNestedSlice(backendRule, []any{filter}, "filters")

			rules[1] = backendRule
			unstructured.SetNestedSlice(uObj.Object, rules, "spec", "rules")
		},
	)

	manifests.AddTweak(
		"testdata/httproute-rewrite-host.yaml",
		"gateway.envoyproxy.io/v1alpha1",
		"Backend",
		"gateway-conformance-infra",
		"backend-fqdn",
		func(uObj *unstructured.Unstructured) {
			endpoints, _, _ := unstructured.NestedSlice(uObj.Object, "spec", "endpoints")

			hostname := fmt.Sprintf("infra-backend-v1.%s.svc.cluster.local", infraNamespaceName)
			unstructured.SetNestedField(endpoints[0].(map[string]any), hostname, "fqdn", "hostname")
			unstructured.SetNestedSlice(uObj.Object, endpoints, "spec", "endpoints")
		},
	)
}

func setGatewaySyncLabels(uObj *unstructured.Unstructured) {
	labels := uObj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels["networking.datumapis.com/gateway-sync"] = ""
	uObj.SetLabels(labels)
}

func rewriteTargetRef(uObj *unstructured.Unstructured) {
	target, _, _ := unstructured.NestedMap(uObj.Object, "spec", "targetRef")
	unstructured.RemoveNestedField(uObj.Object, "spec", "targetRef")
	unstructured.SetNestedSlice(uObj.Object, []any{target}, "spec", "targetRefs")
}
