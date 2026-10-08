package cache

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
)

// NewManager creates a read-only single-cluster controller-runtime manager
// that primes informer caches for the resource types consumed by
// PostTranslateModify: TrafficProtectionPolicy, HTTPProxy, Connector,
// Namespace, and EndpointSlice.
//
// The extension server runs at the edge, co-located with Envoy Gateway. NSO
// replicates TPP/HTTPProxy/Connector resources into the local edge cluster's
// downstream namespaces (ns-<uid>), so all policy reads are LOCAL — no
// upstream control-plane connectivity is required.
//
// The manager has no controllers and no leader election; all replicas serve
// reads from the same warm local cache. The caller must Start the returned
// manager and await WaitForCacheSync before serving gRPC requests.
func NewManager(scheme *runtime.Scheme) (ctrl.Manager, error) {
	// Use the pod's in-cluster config. No QPS/Burst override is needed here;
	// the cache drives list-watch calls at startup only.
	restCfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("get in-cluster config: %w", err)
	}

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme:         scheme,
		LeaderElection: false,
		// Disable metrics and health servers — the extension server exposes its
		// own /healthz on --health-addr; a second listener would conflict.
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		// Disable the default webhook server — extension server has no webhooks.
		WebhookServer: webhook.NewServer(webhook.Options{Port: 0}),
	})
	if err != nil {
		return nil, fmt.Errorf("create cache manager: %w", err)
	}

	// Prime informers before Start so they are started and synced as part of
	// the manager's startup sequence. Calling GetInformer on an unstarted cache
	// registers the type; the informer starts and syncs when mgr.Start(ctx) is
	// called. By the time WaitForCacheSync returns true all four informers are
	// fully populated and subsequent List/Get calls are served from memory.
	bgCtx := context.Background()
	for _, obj := range primeObjects() {
		if _, err := mgr.GetCache().GetInformer(bgCtx, obj); err != nil {
			return nil, fmt.Errorf("register informer for %T: %w", obj, err)
		}
	}

	return mgr, nil
}

// primeObjects returns the resource types that BuildPolicyIndexFromClient
// reads. Defined once here so builder.go and any future wiring have a single
// source of truth for the informer set.
//
// EndpointSlice was added for the vpcPod HTTPProxy backend (#856): resolving
// the tenant-id label a vpcPod backend's referenced EndpointSlice carries
// needs a local Get, same as the Connector lookup already does.
func primeObjects() []client.Object {
	return []client.Object{
		&networkingv1alpha.TrafficProtectionPolicy{},
		&networkingv1alpha.HTTPProxy{},
		&networkingv1alpha1.Connector{},
		&corev1.Namespace{},
		&discoveryv1.EndpointSlice{},
	}
}
