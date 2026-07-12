// SPDX-License-Identifier: AGPL-3.0-only

// This file adds the TrafficProtectionPolicy arm of the edge re-translation
// controller. It is symmetric to the Connector Reconciler in retrigger.go:
// Envoy Gateway only re-runs translation — and so the extension hook — when a
// resource it natively watches changes. A TrafficProtectionPolicy is not such a
// resource, so a mode/spec flip lands promptly in the extension server's local
// cache but the data plane keeps serving the stale WAF program until some
// unrelated EG-watched resource incidentally forces a re-translation.
//
// This controller watches TrafficProtectionPolicies and, on a spec change (which
// is exactly what the cache reads: mode, targetRefs, ruleSets, sampling), touches
// a trigger annotation on each owning Gateway. Because the touch happens after
// the new spec is already in the shared cache, EG re-translates against fresh
// data and the mode change reaches the edge in seconds.

package retrigger

import (
	"context"
	"fmt"
	"strconv"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// tppTriggerAnnotationPrefix is the annotation key prefix patched onto a Gateway
// to make Envoy Gateway re-translate after a TPP spec change. The owning TPP's
// name is appended so each policy owns its own annotation slot: two TPPs
// targeting the same Gateway never clobber each other's value and flip-flop it
// on every resync.
const tppTriggerAnnotationPrefix = "networking.datumapis.com/tpp-generation-"

// TPPReconciler touches the trigger annotation on every Gateway a
// TrafficProtectionPolicy targets whenever the policy's spec changes, forcing EG
// to re-translate and re-run the extension hook against the fresh cache.
type TPPReconciler struct {
	Client client.Client
}

// tppTriggerAnnotationKey returns the per-TPP trigger annotation key.
func tppTriggerAnnotationKey(tppName string) string {
	return tppTriggerAnnotationPrefix + tppName
}

// Reconcile stamps the TPP's observed generation onto the trigger annotation of
// every Gateway it targets. A TPP, the Gateways it targets, and the HTTPProxies
// that generate them share a namespace, and the Gateway is named after the
// targetRef (Gateway targetRefs name the Gateway directly; HTTPRoute targetRefs
// share the HTTPProxy name, which is also the Gateway name), so the
// TPP→Gateway mapping is local and name-derived.
//
// The Gateway patch is a merge patch with no preceding Get: if the generation is
// unchanged the API server treats it as a no-op (no resourceVersion bump, no EG
// event), so it is naturally idempotent and never triggers a spurious
// re-translation.
func (r *TPPReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var tpp networkingv1alpha.TrafficProtectionPolicy
	if err := r.Client.Get(ctx, req.NamespacedName, &tpp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	value := strconv.FormatInt(tpp.Generation, 10)
	annotationKey := tppTriggerAnnotationKey(tpp.Name)

	seen := make(map[string]struct{}, len(tpp.Spec.TargetRefs))
	var firstErr error
	for _, ref := range tpp.Spec.TargetRefs {
		gwName := string(ref.Name)
		if gwName == "" {
			continue
		}
		if _, dup := seen[gwName]; dup {
			continue
		}
		seen[gwName] = struct{}{}

		gwKey := client.ObjectKey{Namespace: tpp.Namespace, Name: gwName}
		if err := r.touchGateway(ctx, gwKey, annotationKey, value); err != nil {
			logger.Error(err, "failed to touch gateway for TPP spec change",
				"gateway", gwKey, "tpp", tpp.Name)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		logger.Info("touched gateway to trigger EG re-translation",
			"gateway", gwKey, "tpp", tpp.Name, "generation", value)
	}

	return ctrl.Result{}, firstErr
}

// touchGateway applies a merge patch setting the trigger annotation. A
// non-existent Gateway is ignored: EG translates a Gateway when it is created,
// reading the (already fresh) extension cache, so there is nothing to nudge yet.
func (r *TPPReconciler) touchGateway(ctx context.Context, key client.ObjectKey, annotationKey, value string) error {
	gw := &gatewayv1.Gateway{}
	gw.Namespace = key.Namespace
	gw.Name = key.Name

	body := fmt.Appendf(nil, `{"metadata":{"annotations":{%q:%q}}}`, annotationKey, value)
	if err := r.Client.Patch(ctx, gw, client.RawPatch(types.MergePatchType, body)); err != nil {
		return client.IgnoreNotFound(err)
	}
	return nil
}

// SetupWithManager registers the controller. It reconciles a TPP only when its
// spec changes (generation bump) — the extension server reads spec fields only
// (mode, targetRefs, ruleSets, sampling), so status/metadata churn is ignored.
func (r *TPPReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1alpha.TrafficProtectionPolicy{}, builder.WithPredicates(tppSpecChangedPredicate())).
		Named("extension-server-gateway-retrigger-tpp").
		Complete(r)
}

// tppSpecChangedPredicate admits creates (so TPPs already present when the
// controller starts stamp their Gateways once) and updates that bump the
// generation (any spec change: mode flip, targetRef edit, ruleset/sampling
// change). Deletes are not handled here: a deleted TPP leaves no object to read
// targetRefs from, and its Gateways are not re-translated by this arm — see the
// known-limitation note in the PR.
func tppSpecChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(event.CreateEvent) bool { return true },
		DeleteFunc: func(event.DeleteEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldT, ok1 := e.ObjectOld.(*networkingv1alpha.TrafficProtectionPolicy)
			newT, ok2 := e.ObjectNew.(*networkingv1alpha.TrafficProtectionPolicy)
			if !ok1 || !ok2 {
				return true
			}
			return oldT.Generation != newT.Generation
		},
	}
}
