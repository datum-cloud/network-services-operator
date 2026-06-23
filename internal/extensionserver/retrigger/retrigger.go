// SPDX-License-Identifier: AGPL-3.0-only

// Package retrigger contains the edge controller that makes Envoy Gateway
// re-translate when a Connector's liveness changes.
//
// A Connector's online/offline state reaches the edge in the
// networking.datumapis.com/upstream-status annotation (Karmada propagates
// metadata, not status), and the extension server reads it to program tunnels.
// Envoy Gateway, however, only re-runs translation — and so the extension hook —
// when a resource it watches changes through an annotation-aware predicate; it
// does not re-translate on Connector changes. So an online connector's fresh
// liveness sits in the cache while the data plane keeps serving the stale
// (often offline) program.
//
// This controller, co-located with the extension server and sharing its cache,
// watches Connectors and touches the owning Gateway when liveness changes. Envoy
// Gateway does re-translate on Gateway annotation changes, and because the touch
// happens after the new liveness is already in the shared cache, the hook
// re-runs against fresh data.
package retrigger

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	extcache "go.datum.net/network-services-operator/internal/extensionserver/cache"
)

// ConnectorReadyAnnotationKey is the annotation patched onto a Gateway to make
// Envoy Gateway re-translate. Its value encodes the connector's liveness, so EG
// re-translates only when the liveness actually changes.
const ConnectorReadyAnnotationKey = "networking.datumapis.com/connector-ready-generation"

// Reconciler touches the owning Gateway annotation whenever an edge Connector's
// liveness changes, forcing EG to re-translate and re-run the extension hook.
type Reconciler struct {
	Client client.Client
}

// Reconcile resolves the connector's current liveness and stamps it onto every
// Gateway backed by an HTTPProxy that references the connector. The Gateway
// patch is a merge patch with no preceding Get: if the value is unchanged the
// API server treats it as a no-op (no resourceVersion bump, no EG event), so it
// is naturally idempotent and never triggers a spurious re-translation.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var connector networkingv1alpha1.Connector
	if err := r.Client.Get(ctx, req.NamespacedName, &connector); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	online, nodeID := extcache.ConnectorLiveness(&connector)
	value := livenessValue(online, nodeID)

	// The Connector, HTTPProxy, and Gateway share a namespace, and the Gateway is
	// named after the HTTPProxy, so the connector→Gateway mapping is local.
	var proxies networkingv1alpha.HTTPProxyList
	if err := r.Client.List(ctx, &proxies, client.InNamespace(connector.Namespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("list HTTPProxies in %s: %w", connector.Namespace, err)
	}

	var firstErr error
	for i := range proxies.Items {
		proxy := &proxies.Items[i]
		if !httpProxyReferencesConnector(proxy, connector.Name) {
			continue
		}

		gwKey := client.ObjectKey{Namespace: connector.Namespace, Name: proxy.Name}
		if err := r.touchGateway(ctx, gwKey, value); err != nil {
			logger.Error(err, "failed to touch gateway for connector liveness change",
				"gateway", gwKey, "connector", connector.Name)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		logger.Info("touched gateway to trigger EG re-translation",
			"gateway", gwKey, "connector", connector.Name, "liveness", value)
	}

	return ctrl.Result{}, firstErr
}

// touchGateway applies a merge patch setting the trigger annotation. A
// non-existent Gateway is ignored: EG translates a Gateway when it is created,
// reading the (already fresh) extension cache, so there is nothing to nudge yet.
func (r *Reconciler) touchGateway(ctx context.Context, key client.ObjectKey, value string) error {
	gw := &gatewayv1.Gateway{}
	gw.Namespace = key.Namespace
	gw.Name = key.Name

	body := fmt.Appendf(nil, `{"metadata":{"annotations":{%q:%q}}}`, ConnectorReadyAnnotationKey, value)
	if err := r.Client.Patch(ctx, gw, client.RawPatch(types.MergePatchType, body)); err != nil {
		return client.IgnoreNotFound(err)
	}
	return nil
}

// livenessValue is the trigger annotation value. It includes the node id so a
// change in connectionDetails (e.g. the tunnel endpoint moves) re-translates
// too, not only Ready flips.
func livenessValue(online bool, nodeID string) string {
	return fmt.Sprintf("%t/%s", online, nodeID)
}

// SetupWithManager registers the controller. It reconciles a Connector only when
// its liveness — the (online, nodeID) the extension server keys on — actually
// changes, so heartbeat status churn that does not affect routing is ignored.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1alpha1.Connector{}, builder.WithPredicates(livenessChangedPredicate())).
		Named("extension-server-gateway-retrigger").
		Complete(r)
}

// livenessChangedPredicate admits creates (so connectors already online when the
// controller starts get their Gateways stamped) and updates that change the
// (online, nodeID) classification. Deletes are ignored: removing a Connector
// tears down its HTTPProxy/Gateway/HTTPRoute, which EG re-translates on its own.
func livenessChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(event.CreateEvent) bool { return true },
		DeleteFunc: func(event.DeleteEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldC, ok1 := e.ObjectOld.(*networkingv1alpha1.Connector)
			newC, ok2 := e.ObjectNew.(*networkingv1alpha1.Connector)
			if !ok1 || !ok2 {
				return true
			}
			oOnline, oNode := extcache.ConnectorLiveness(oldC)
			nOnline, nNode := extcache.ConnectorLiveness(newC)
			return oOnline != nOnline || oNode != nNode
		},
	}
}

// httpProxyReferencesConnector reports whether any of the HTTPProxy's backends
// reference the named Connector. (Kept local to avoid importing the controller
// package's multicluster dependencies into the edge extension server.)
func httpProxyReferencesConnector(httpProxy *networkingv1alpha.HTTPProxy, connectorName string) bool {
	for _, rule := range httpProxy.Spec.Rules {
		for _, backend := range rule.Backends {
			if backend.Connector != nil && backend.Connector.Name == connectorName {
				return true
			}
		}
	}
	return false
}
