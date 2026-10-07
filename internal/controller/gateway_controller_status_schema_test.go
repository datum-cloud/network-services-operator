// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
)

var installedGatewayAPICRDURL = regexp.MustCompile(`^https://raw\.githubusercontent\.com/kubernetes-sigs/gateway-api/refs/tags/[^/]+/(config/crd/[^/]+/[^/]+\.yaml)$`)

func installedGatewayAPICRDPaths(t *testing.T) []string {
	t.Helper()

	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "sigs.k8s.io/gateway-api").Output()
	require.NoError(t, err)
	moduleDir := strings.TrimSpace(string(out))
	require.NotEmpty(t, moduleDir)

	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "gateway", "kustomization.yaml"))
	require.NoError(t, err)
	var kustomization struct {
		Resources []string `json:"resources"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &kustomization))

	var paths []string
	for _, resource := range kustomization.Resources {
		if m := installedGatewayAPICRDURL.FindStringSubmatch(resource); m != nil {
			paths = append(paths, filepath.Join(moduleDir, m[1]))
		}
	}
	require.NotEmpty(t, paths, "no gateway-api CRDs found in the gateway kustomization")
	return paths
}

const testUpstreamGatewayControllerName = "gateway.networking.datumapis.com/external-global-proxy-controller"

func relaxRouteParentConditions(t *testing.T, crds []*apiextensionsv1.CustomResourceDefinition) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()

	for _, crd := range crds {
		if crd.Name != "httproutes.gateway.networking.k8s.io" {
			continue
		}
		relaxed := crd.DeepCopy()
		relaxed.ResourceVersion = ""
		for i := range relaxed.Spec.Versions {
			status := relaxed.Spec.Versions[i].Schema.OpenAPIV3Schema.Properties["status"]
			parents := status.Properties["parents"]
			parents.Items.Schema.Required = slices.DeleteFunc(parents.Items.Schema.Required, func(s string) bool { return s == "conditions" })
			status.Properties["parents"] = parents
			relaxed.Spec.Versions[i].Schema.OpenAPIV3Schema.Properties["status"] = status
		}
		return relaxed
	}
	t.Fatal("httproutes CRD is not installed")
	return nil
}

func acceptedRouteConditions() []metav1.Condition {
	return []metav1.Condition{
		{
			Type:               string(gatewayv1.RouteConditionAccepted),
			Status:             metav1.ConditionTrue,
			Reason:             string(gatewayv1.RouteReasonAccepted),
			Message:            "Route is accepted",
			LastTransitionTime: metav1.Now(),
		},
		{
			Type:               string(gatewayv1.RouteConditionResolvedRefs),
			Status:             metav1.ConditionTrue,
			Reason:             string(gatewayv1.RouteReasonResolvedRefs),
			Message:            "Resolved all the Object references for the Route",
			LastTransitionTime: metav1.Now(),
		},
	}
}

func TestEnsureDownstreamGatewayHTTPRoutesStatusFitsInstalledSchema(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}

	env := &envtest.Environment{
		CRDInstallOptions: envtest.CRDInstallOptions{
			Paths:              installedGatewayAPICRDPaths(t),
			ErrorIfPathMissing: true,
		},
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop() })

	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, gatewayv1.Install(testScheme))
	require.NoError(t, discoveryv1.AddToScheme(testScheme))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(testScheme))

	upstreamClient, err := client.New(cfg, client.Options{Scheme: testScheme})
	require.NoError(t, err)

	ctx := context.Background()

	testConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			DownstreamGatewayClassName:            "test-suite",
			DownstreamHostnameAccountingNamespace: "default",
			TargetDomain:                          "test-suite.com",
			ListenerTLSOptions: map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue{
				"gateway.networking.datumapis.com/certificate-issuer": "auto",
			},
		},
	}

	upstreamNamespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test"}}
	require.NoError(t, upstreamClient.Create(ctx, upstreamNamespace))
	downstreamNamespaceName := "ns-" + string(upstreamNamespace.UID)

	downstreamClient := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithStatusSubresource(&gatewayv1.Gateway{}, &gatewayv1.HTTPRoute{}).
		Build()

	reconciler := &GatewayReconciler{
		mgr:               &fakeMockManager{cl: upstreamClient},
		DownstreamCluster: &fakeCluster{cl: downstreamClient},
	}
	downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy("test", upstreamClient, downstreamClient)

	otherControllerParent := gatewayv1.RouteParentStatus{
		ControllerName: "example.com/other-controller",
		ParentRef:      gatewayv1.ParentReference{Name: "other"},
		Conditions:     acceptedRouteConditions(),
	}
	conditionlessParent := func(gateway string) gatewayv1.RouteParentStatus {
		return gatewayv1.RouteParentStatus{
			ControllerName: testUpstreamGatewayControllerName,
			ParentRef:      gatewayv1.ParentReference{Name: gatewayv1.ObjectName(gateway)},
		}
	}

	scenarios := []struct {
		gateway           string
		parentRefs        []string
		existingParents   []gatewayv1.RouteParentStatus
		downstreamReports bool
	}{
		{gateway: "unreported"},
		{gateway: "reported", downstreamReports: true},
		{gateway: "stale-unreported", existingParents: []gatewayv1.RouteParentStatus{conditionlessParent("stale-unreported")}},
		{gateway: "stale-reported", existingParents: []gatewayv1.RouteParentStatus{conditionlessParent("stale-reported")}, downstreamReports: true},
		{gateway: "stale-shared", parentRefs: []string{"other"}, existingParents: []gatewayv1.RouteParentStatus{otherControllerParent, conditionlessParent("stale-shared")}},
	}

	for _, s := range scenarios {
		upstreamGateway := newGateway(testConfig, upstreamNamespace.Name, s.gateway)
		upstreamGateway.UID = ""
		require.NoError(t, upstreamClient.Create(ctx, upstreamGateway))

		require.NoError(t, downstreamClient.Create(ctx, newGateway(testConfig, downstreamNamespaceName, s.gateway)))

		route := newHTTPRoute(upstreamNamespace.Name, s.gateway, func(route *gatewayv1.HTTPRoute) {
			route.UID = ""
			route.Spec.ParentRefs = []gatewayv1.ParentReference{{Name: gatewayv1.ObjectName(s.gateway)}}
			for _, ref := range s.parentRefs {
				route.Spec.ParentRefs = append(route.Spec.ParentRefs, gatewayv1.ParentReference{Name: gatewayv1.ObjectName(ref)})
			}
		})
		require.NoError(t, upstreamClient.Create(ctx, route))

		if s.downstreamReports {
			downstreamRoute := newHTTPRoute(downstreamNamespaceName, s.gateway)
			require.NoError(t, downstreamClient.Create(ctx, downstreamRoute))
			downstreamRoute.Status.Parents = []gatewayv1.RouteParentStatus{{
				ControllerName: "gateway.envoyproxy.io/gatewayclass-controller",
				ParentRef:      gatewayv1.ParentReference{Name: gatewayv1.ObjectName(s.gateway)},
				Conditions:     acceptedRouteConditions(),
			}}
			require.NoError(t, downstreamClient.Status().Update(ctx, downstreamRoute))
		}
	}

	_, err = envtest.InstallCRDs(cfg, envtest.CRDInstallOptions{CRDs: []*apiextensionsv1.CustomResourceDefinition{relaxRouteParentConditions(t, env.CRDs)}})
	require.NoError(t, err)
	for _, s := range scenarios {
		if len(s.existingParents) == 0 {
			continue
		}
		var route gatewayv1.HTTPRoute
		require.NoError(t, upstreamClient.Get(ctx, client.ObjectKey{Namespace: upstreamNamespace.Name, Name: s.gateway}, &route))
		route.Status.Parents = s.existingParents
		require.NoError(t, upstreamClient.Status().Update(ctx, &route))
	}
	_, err = envtest.InstallCRDs(cfg, envtest.CRDInstallOptions{Paths: installedGatewayAPICRDPaths(t)})
	require.NoError(t, err)

	reconcile := func(t *testing.T, gateway string) *gatewayv1.HTTPRoute {
		t.Helper()

		var upstreamGateway gatewayv1.Gateway
		require.NoError(t, upstreamClient.Get(ctx, client.ObjectKey{Namespace: upstreamNamespace.Name, Name: gateway}, &upstreamGateway))
		var downstreamGateway gatewayv1.Gateway
		require.NoError(t, downstreamClient.Get(ctx, client.ObjectKey{Namespace: downstreamNamespaceName, Name: gateway}, &downstreamGateway))

		result := reconciler.ensureDownstreamGatewayHTTPRoutes(
			ctx,
			upstreamClient,
			&upstreamGateway,
			testUpstreamGatewayControllerName,
			&downstreamGateway,
			downstreamStrategy,
			nil,
			nil,
			nil,
		)
		require.NoError(t, result.Err)
		_, err := result.Complete(ctx)
		require.NoError(t, err, "the upstream apiserver rejected a status write")

		var route gatewayv1.HTTPRoute
		require.NoError(t, upstreamClient.Get(ctx, client.ObjectKey{Namespace: upstreamNamespace.Name, Name: gateway}, &route))
		return &route
	}

	requireAccepted := func(t *testing.T, parent gatewayv1.RouteParentStatus) {
		t.Helper()
		require.True(t, apimeta.IsStatusConditionTrue(parent.Conditions, string(gatewayv1.RouteConditionAccepted)))
		require.True(t, apimeta.IsStatusConditionTrue(parent.Conditions, string(gatewayv1.RouteConditionResolvedRefs)))
	}

	t.Run("downstream has not reported route status", func(t *testing.T) {
		route := reconcile(t, "unreported")
		require.Empty(t, route.Status.Parents)
	})

	t.Run("downstream reports route status", func(t *testing.T) {
		route := reconcile(t, "reported")
		require.Len(t, route.Status.Parents, 1)
		requireAccepted(t, route.Status.Parents[0])
	})

	t.Run("conditionless parent is removed while downstream has not reported", func(t *testing.T) {
		route := reconcile(t, "stale-unreported")
		require.Empty(t, route.Status.Parents)
	})

	t.Run("conditionless parent is filled in once downstream reports", func(t *testing.T) {
		route := reconcile(t, "stale-reported")
		require.Len(t, route.Status.Parents, 1)
		require.EqualValues(t, "stale-reported", route.Status.Parents[0].ParentRef.Name)
		requireAccepted(t, route.Status.Parents[0])
	})

	t.Run("another controller's parent survives removal of a conditionless parent", func(t *testing.T) {
		route := reconcile(t, "stale-shared")
		require.Len(t, route.Status.Parents, 1)
		require.EqualValues(t, otherControllerParent.ControllerName, route.Status.Parents[0].ControllerName)
		require.EqualValues(t, "other", route.Status.Parents[0].ParentRef.Name)
		requireAccepted(t, route.Status.Parents[0])
	})
}
