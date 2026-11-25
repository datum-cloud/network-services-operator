// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"go.datum.net/network-services-operator/internal/config"
)

func TestReplicatorReleasesSecretAfterItsPolicyIsDeleted(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, s := startSecretReleaseEnv(t)
	c, err := client.New(cfg, client.Options{Scheme: s})
	require.NoError(t, err)

	const ns = "default"
	startReplicatorManager(t, ctx, cfg, s)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "users"},
		StringData: map[string]string{".htpasswd": "alice:{SHA}placeholder\n"},
	}
	require.NoError(t, c.Create(ctx, secret))
	policy := &envoygatewayv1alpha1.SecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: "auth"},
		Spec: envoygatewayv1alpha1.SecurityPolicySpec{
			PolicyTargetReferences: envoygatewayv1alpha1.PolicyTargetReferences{
				TargetRefs: []gatewayv1.LocalPolicyTargetReferenceWithSectionName{{
					LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
						Group: gatewayv1.GroupName, Kind: "Gateway", Name: "example",
					},
				}},
			},
			BasicAuth: &envoygatewayv1alpha1.BasicAuth{
				Users: gatewayv1.SecretObjectReference{Name: "users"},
			},
		},
	}
	require.NoError(t, c.Create(ctx, policy))

	eventually(t, "the secret to be labeled and finalized", func() bool {
		var got corev1.Secret
		if err := c.Get(ctx, client.ObjectKeyFromObject(secret), &got); err != nil {
			return false
		}
		_, labeled := got.Labels[gatewaySyncLabel]
		return labeled && controllerutil.ContainsFinalizer(&got, gatewayResourceReplicatorFinalizer)
	})

	require.NoError(t, c.Delete(ctx, policy))
	eventually(t, "the policy to be gone and the secret unlabeled", func() bool {
		if !apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(policy), &envoygatewayv1alpha1.SecurityPolicy{})) {
			return false
		}
		var got corev1.Secret
		if err := c.Get(ctx, client.ObjectKeyFromObject(secret), &got); err != nil {
			return false
		}
		_, labeled := got.Labels[gatewaySyncLabel]
		return !labeled
	})

	require.NoError(t, c.Delete(ctx, secret))
	eventually(t, "the secret to be deleted", func() bool {
		return apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(secret), &corev1.Secret{}))
	})
}

func TestReplicatorReleasesASecretStrandedBeforeItStarted(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS unset; run via `make test` to exercise envtest")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, s := startSecretReleaseEnv(t)
	c, err := client.New(cfg, client.Options{Scheme: s})
	require.NoError(t, err)

	const ns = "default"
	stranded := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  ns,
			Name:       "stranded-users",
			Finalizers: []string{gatewayResourceReplicatorFinalizer},
		},
		StringData: map[string]string{".htpasswd": "alice:{SHA}placeholder\n"},
	}
	require.NoError(t, c.Create(ctx, stranded))
	require.NoError(t, c.Delete(ctx, stranded))
	kept := stranded.DeepCopy()
	kept.ObjectMeta = metav1.ObjectMeta{
		Namespace:  ns,
		Name:       "kept-users",
		Finalizers: []string{gatewayResourceReplicatorFinalizer},
	}
	require.NoError(t, c.Create(ctx, kept))

	startReplicatorManager(t, ctx, cfg, s)

	eventually(t, "the stranded terminating secret to be released", func() bool {
		return apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(stranded), &corev1.Secret{}))
	})
	eventually(t, "the finalizer on the stranded secret to be removed", func() bool {
		var got corev1.Secret
		return c.Get(ctx, client.ObjectKeyFromObject(kept), &got) == nil &&
			!controllerutil.ContainsFinalizer(&got, gatewayResourceReplicatorFinalizer)
	})
}

func startSecretReleaseEnv(t *testing.T) (*rest.Config, *runtime.Scheme) {
	t.Helper()
	env := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "tools", "envoy-gateway", "charts",
				"gateway-helm-v0.0.0-latest", "gateway-helm", "crds", "generated"),
		},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop() })

	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, envoygatewayv1alpha1.AddToScheme(s))
	return cfg, s
}

func startReplicatorManager(t *testing.T, ctx context.Context, cfg *rest.Config, s *runtime.Scheme) {
	t.Helper()
	provider := &staticProvider{clusters: map[string]cluster.Cluster{}}
	mgr, err := mcmanager.New(cfg, provider, ctrl.Options{
		Scheme:     s,
		Metrics:    metricsserver.Options{BindAddress: "0"},
		Controller: ctrlconfig.Controller{SkipNameValidation: ptr.To(true)},
	})
	require.NoError(t, err)
	const clusterName = "project"
	provider.clusters[clusterName] = mgr.GetLocalManager()

	downstream, err := cluster.New(cfg, func(o *cluster.Options) {
		o.Scheme = s
		o.Client = client.Options{Cache: &client.CacheOptions{Unstructured: true}}
	})
	require.NoError(t, err)

	operatorConfig := config.NetworkServicesOperator{}
	config.SetObjectDefaults_NetworkServicesOperator(&operatorConfig)
	var resources []config.ReplicatedResourceConfig
	for _, rc := range operatorConfig.Gateway.ResourceReplicator.Resources {
		if rc.Kind == "Secret" || rc.Kind == "SecurityPolicy" {
			resources = append(resources, rc)
		}
	}
	require.Len(t, resources, 2)
	operatorConfig.Gateway.ResourceReplicator.Resources = resources

	r := &GatewayResourceReplicatorReconciler{Config: operatorConfig, DownstreamCluster: downstream}
	require.NoError(t, r.SetupWithManager(mgr))
	require.NoError(t, mgr.Engage(ctx, clusterName, mgr.GetLocalManager()))
	go func() { _ = downstream.Start(ctx) }()
	go func() { _ = mgr.Start(ctx) }()
}
