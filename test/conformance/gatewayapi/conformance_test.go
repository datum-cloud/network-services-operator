// SPDX-License-Identifier: AGPL-3.0-only
//go:build conformance

package gatewayapi

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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

	opts.ManifestFS = append([]fs.FS{Manifests}, opts.ManifestFS...)
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

	opts.RoundTripper = &NodePortRoundTripper{
		Debug:         opts.Debug,
		TimeoutConfig: opts.TimeoutConfig,
		NodeAddress:   *infraNodeAddress,
		HTTPPort:      *infraHTTPPort,
		HTTPSPort:     *infraHTTPSPort,
	}

	selectedTests, err := selectTests(opts.RunTest)
	if err != nil {
		t.Fatalf("selecting conformance tests: %v", err)
	}

	logSuiteConfiguration(t, opts, selectedTests)

	cSuite, err := suite.NewConformanceTestSuite(opts)
	if err != nil {
		t.Fatalf("initialising conformance suite: %v", err)
	}

	cSuite.Setup(t, selectedTests)
	networkingv1alpha.AddToScheme(cSuite.Client.Scheme())

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

	if err := ensureInfraBackends(t, cSuite, *infraKubeconfig); err != nil {
		t.Fatalf("preparing infra cluster: %v", err)
	}

	if opts.RunTest != "" {
		tlog.Logf(t, "Running Gateway API conformance test %s", opts.RunTest)
	} else {
		tlog.Logf(t, "Running %d Gateway API conformance tests", len(selectedTests))
	}

	if err := cSuite.Run(t, selectedTests); err != nil {
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
)

func selectTests(runTest string) ([]suite.ConformanceTest, error) {
	testsByName := make(map[string]suite.ConformanceTest, len(tests.ConformanceTests))
	for _, test := range tests.ConformanceTests {
		testsByName[test.ShortName] = test
	}

	if runTest != "" {
		test, ok := testsByName[runTest]
		if !ok {
			return nil, fmt.Errorf("run-test %q is not registered in the upstream suite", runTest)
		}

		return []suite.ConformanceTest{test}, nil
	}

	t := slices.Clone(tests.ConformanceTests)

	return slices.DeleteFunc(t, func(test suite.ConformanceTest) bool {
		return skipTests.Has(test.ShortName)
	}), nil
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

func ensureInfraBackends(t *testing.T, suite *suite.ConformanceTestSuite, kubeconfigPath string) error {

	infraNamespaceName, err := deriveInfraNamespaceName(context.Background(), suite.Client, managementInfraNamespace)

	if err != nil {
		t.Fatalf("deriving infra namespace name: %v", err)
	}

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
		client.NewNamespacedClient(infraClient, infraNamespaceName),
		suite.TimeoutConfig,
		"base/infra-manifests.yaml",
		suite.Cleanup,
	)

	secret := kubernetes.MustCreateSelfSignedCertSecret(t, infraNamespaceName, "tls-passthrough-checks-certificate", []string{"abc.example.com"})
	suite.Applier.MustApplyObjectsWithCleanup(t, infraClient, suite.TimeoutConfig, []client.Object{secret}, suite.Cleanup)

	kubernetes.NamespacesMustBeReady(t, infraClient, suite.TimeoutConfig, []string{infraNamespaceName})

	return nil
}

func deriveInfraNamespaceName(ctx context.Context, c client.Client, managementNamespace string) (string, error) {
	var ns corev1.Namespace
	if err := c.Get(ctx, types.NamespacedName{Name: managementNamespace}, &ns); err != nil {
		return "", err
	}

	return fmt.Sprintf("ns-%s", ns.GetUID()), nil
}
