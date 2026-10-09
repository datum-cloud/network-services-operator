//go:build prototype

// SPDX-License-Identifier: AGPL-3.0-only

package prototype

// Run against a kube-apiserver (envtest):
//
//	KUBEBUILDER_ASSETS="$(bin/setup-envtest use 1.31.0 --bin-dir "$PWD/bin" -p path)" \
//	  go test -tags prototype ./docs/enhancements/controller-field-ownership/prototype -v
//
// Every scenario logs lines that start with RESULT. Where a control exists, it
// runs the save it replaces beside the proposed one.

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
)

const (
	repoRoot = "../../../.."

	finProxy      = "networking.datumapis.com/httpproxy-cleanup"
	finReplicator = "gateway.networking.datumapis.com/gateway-resource-replicator"
	finGateway    = "gateway.networking.datumapis.com/gateway-controller"

	// The field managers production records the operator's saves under: the
	// binary's old and present names, shared by every controller.
	legacyOld     = "manager"
	legacyPresent = "network-services"
)

var gatewayAPICRDURL = regexp.MustCompile(
	`^https://raw\.githubusercontent\.com/kubernetes-sigs/gateway-api/refs/tags/[^/]+/(config/crd/[^/]+/[^/]+\.yaml)$`)

// env holds one API server. c reads and writes without a cache, so it also
// serves as the uncached reader a controller takes from its cluster.
type env struct {
	c      client.Client
	scheme *runtime.Scheme
	test   *envtest.Environment
}

func startEnv(t *testing.T) *env {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset")
	}
	te := &envtest.Environment{
		CRDDirectoryPaths:     append([]string{filepath.Join(repoRoot, "config", "crd", "bases")}, gatewayAPICRDPaths(t)...),
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := te.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = te.Stop() })
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, gatewayv1.Install(s))
	require.NoError(t, networkingv1alpha.AddToScheme(s))
	require.NoError(t, networkingv1alpha1.AddToScheme(s))
	c, err := client.New(cfg, client.Options{Scheme: s})
	require.NoError(t, err)
	return &env{c: c, scheme: s, test: te}
}

// gatewayAPICRDPaths returns the Gateway API CRDs at the version the operator
// installs, read from config/crd/gateway/kustomization.yaml.
func gatewayAPICRDPaths(t *testing.T) []string {
	t.Helper()
	dir, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "sigs.k8s.io/gateway-api").Output()
	require.NoError(t, err)
	raw, err := os.ReadFile(filepath.Join(repoRoot, "config", "crd", "gateway", "kustomization.yaml"))
	require.NoError(t, err)
	var k struct {
		Resources []string `json:"resources"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &k))
	var paths []string
	for _, r := range k.Resources {
		if m := gatewayAPICRDURL.FindStringSubmatch(r); m != nil {
			paths = append(paths, filepath.Join(string(bytes.TrimSpace(dir)), m[1]))
		}
	}
	require.NotEmpty(t, paths)
	return paths
}

// patcher is one controller's Patcher on the test cluster.
func (e *env) patcher(controller string, testFailures *atomic.Int64) Patcher {
	return Patcher{Client: e.c, Reader: e.c, Manager: FieldManager(controller), TestFailures: testFailures}
}

func newProxy(name string) *networkingv1alpha.HTTPProxy {
	return &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{{
				Backends: []networkingv1alpha.HTTPProxyRuleBackend{{Endpoint: "http://www.example.com"}},
			}},
		},
	}
}

func requireGone(t *testing.T, c client.Client, obj client.Object) {
	t.Helper()
	require.Eventually(t, func() bool {
		live, ok := obj.DeepCopyObject().(client.Object)
		require.True(t, ok)
		return apierrors.IsNotFound(c.Get(context.Background(), client.ObjectKeyFromObject(obj), live))
	}, 10*time.Second, 50*time.Millisecond, "%s still exists: a finalizer was left behind", obj.GetName())
}

func TestFieldOwnership(t *testing.T) {
	e := startEnv(t)
	t.Run("P1 finalizers: two controllers, churn", func(t *testing.T) { finalizerChurn(t, e) })
	t.Run("P1 finalizers: beside status and spec saves", func(t *testing.T) { finalizersBesideOtherSaves(t, e) })
	t.Run("P1 finalizers: legacy entries", func(t *testing.T) { legacyFinalizers(t, e) })
	t.Run("P1 finalizers: mixed rollout", func(t *testing.T) { mixedRollout(t, e) })
	t.Run("P1 finalizers: duplicates", func(t *testing.T) { duplicateFinalizer(t, e) })
	t.Run("P1 finalizers: rollback", func(t *testing.T) { rollback(t, e) })
	t.Run("P1 finalizers: edges", func(t *testing.T) { finalizerEdges(t, e) })
	t.Run("P2 status, one writer: from a cache", func(t *testing.T) { statusOneWriter(t, e) })
	t.Run("P2 status, one writer: a list shrinks", func(t *testing.T) { statusListShrinks(t, e) })
	t.Run("P2 status, one writer: old code at once", func(t *testing.T) { statusOldCode(t, e) })
	t.Run("P3 conditions, two writers", func(t *testing.T) { conditionsTwoWriters(t, e) })
	t.Run("P3 conditions, two writers: old code at once", func(t *testing.T) { conditionsOldCode(t, e) })
	t.Run("P4 Gateway: a new Gateway", func(t *testing.T) { gatewayNew(t, e) })
	t.Run("P4 Gateway: a legacy Gateway", func(t *testing.T) { gatewayLegacy(t, e) })
	t.Run("P4 Gateway: a hostname removed", func(t *testing.T) { gatewayHostnameRemoved(t, e) })
	t.Run("P4 Gateway: a user's hostname", func(t *testing.T) { gatewayUserHostname(t, e) })
	t.Run("P4 Gateway: a deleted object", func(t *testing.T) { gatewayGone(t, e) })
	t.Run("P4 Gateway: old code at once", func(t *testing.T) { gatewayOldCode(t, e) })
}
