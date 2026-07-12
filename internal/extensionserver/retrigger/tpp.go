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
//
// Deletion and targetRef removal are the mirror image: when a TPP is deleted or
// a Gateway is dropped from spec.targetRefs, the cache no longer programs any WAF
// for that Gateway, but nothing forces EG to re-translate — so the edge keeps
// serving the last (possibly Enforce, still-blocking) program. Standard
// Reconcile receives only the object key on delete, not the last-known object,
// so the controller records the owning Gateway set of each TPP in memory and, on
// delete or a targetRef edit, clears the trigger annotation on every Gateway that
// dropped out of the set. Removing the annotation is itself a Gateway change, so
// EG re-translates against the now-empty cache and drops the orphaned WAF config.

package retrigger

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/go-logr/logr"
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
// to re-translate and re-run the extension hook against the fresh cache. On
// delete or targetRef removal it clears the annotation on Gateways that dropped
// out of the policy's target set so EG re-translates and drops the orphaned WAF.
type TPPReconciler struct {
	Client client.Client

	// mu guards lastTargets. Reconciles for distinct TPPs run concurrently, so
	// the previous-target map needs its own lock (controller-runtime only
	// serialises reconciles that share a key).
	mu sync.Mutex
	// lastTargets records the owning Gateway set last stamped for each TPP,
	// keyed by namespaced name. It is the source for "which Gateways did this
	// TPP used to govern?" on a delete (where the object is gone) or a targetRef
	// edit (where only the current targets are otherwise visible). Populated by
	// the create event every live TPP fires on startup, so a restart re-learns
	// the set before any subsequent edit or delete.
	lastTargets map[types.NamespacedName][]string
}

// tppTriggerAnnotationKey returns the per-TPP trigger annotation key.
func tppTriggerAnnotationKey(tppName string) string {
	return tppTriggerAnnotationPrefix + tppName
}

// Reconcile stamps the TPP's observed generation onto the trigger annotation of
// every Gateway it targets, and clears the annotation on every Gateway that
// dropped out of its target set since the last reconcile (a removed targetRef,
// or — when the TPP itself is gone — all of them). A TPP, the Gateways it
// targets, and the HTTPProxies that generate them share a namespace, and the
// Gateway is named after the targetRef (Gateway targetRefs name the Gateway
// directly; HTTPRoute targetRefs share the HTTPProxy name, which is also the
// Gateway name), so the TPP→Gateway mapping is local and name-derived.
//
// Each Gateway patch is a merge patch with no preceding Get: an unchanged
// generation (or clearing an already-absent annotation) is a server-side no-op
// (no resourceVersion bump, no EG event), so it is naturally idempotent and
// never triggers a spurious re-translation.
func (r *TPPReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	annotationKey := tppTriggerAnnotationKey(req.Name)

	var tpp networkingv1alpha.TrafficProtectionPolicy
	if err := r.Client.Get(ctx, req.NamespacedName, &tpp); err != nil {
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
		// Deleted: nudge every Gateway the policy used to govern so EG
		// re-translates against the now-empty cache and drops the orphaned WAF.
		return ctrl.Result{}, r.clearGateways(ctx, logger, req.NamespacedName, annotationKey, r.takeTargets(req.NamespacedName), "delete")
	}

	value := strconv.FormatInt(tpp.Generation, 10)

	seen := make(map[string]struct{}, len(tpp.Spec.TargetRefs))
	current := make([]string, 0, len(tpp.Spec.TargetRefs))
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
		current = append(current, gwName)

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

	// Clear Gateways that were targeted last time but are not now. On any error
	// touching a current target, keep the previous set so the dropped Gateways
	// are retried on requeue rather than forgotten.
	prev := r.swapTargets(req.NamespacedName, current, firstErr == nil)
	if err := r.clearGateways(ctx, logger, req.NamespacedName, annotationKey, removedTargets(prev, seen), "targetRef removal"); err != nil && firstErr == nil {
		firstErr = err
	}

	return ctrl.Result{}, firstErr
}

// clearGateways removes the trigger annotation from each named Gateway in the
// policy's namespace, returning the first error encountered.
func (r *TPPReconciler) clearGateways(ctx context.Context, logger logr.Logger, tpp types.NamespacedName, annotationKey string, gwNames []string, reason string) error {
	var firstErr error
	for _, gwName := range gwNames {
		gwKey := client.ObjectKey{Namespace: tpp.Namespace, Name: gwName}
		if err := r.clearGateway(ctx, gwKey, annotationKey); err != nil {
			logger.Error(err, "failed to clear gateway trigger for orphaned TPP target",
				"gateway", gwKey, "tpp", tpp.Name, "reason", reason)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		logger.Info("cleared gateway trigger to drop orphaned WAF config",
			"gateway", gwKey, "tpp", tpp.Name, "reason", reason)
	}
	return firstErr
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

// clearGateway removes the trigger annotation via a merge patch (JSON null
// deletes the key). Removing the annotation is a Gateway change, so EG
// re-translates; clearing an already-absent key is a server-side no-op.
func (r *TPPReconciler) clearGateway(ctx context.Context, key client.ObjectKey, annotationKey string) error {
	gw := &gatewayv1.Gateway{}
	gw.Namespace = key.Namespace
	gw.Name = key.Name

	body := fmt.Appendf(nil, `{"metadata":{"annotations":{%q:null}}}`, annotationKey)
	if err := r.Client.Patch(ctx, gw, client.RawPatch(types.MergePatchType, body)); err != nil {
		return client.IgnoreNotFound(err)
	}
	return nil
}

// swapTargets records the TPP's current owning Gateway set and returns the set
// from the previous reconcile. When commit is false the previous set is left in
// place (a touch failed, so the dropped Gateways must stay pending for retry)
// but still returned for diffing.
func (r *TPPReconciler) swapTargets(key types.NamespacedName, current []string, commit bool) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	prev := r.lastTargets[key]
	if !commit {
		return prev
	}
	if r.lastTargets == nil {
		r.lastTargets = make(map[types.NamespacedName][]string)
	}
	if len(current) == 0 {
		delete(r.lastTargets, key)
	} else {
		r.lastTargets[key] = current
	}
	return prev
}

// takeTargets returns and forgets the TPP's recorded owning Gateway set (used on
// delete, where there is no current set to keep).
func (r *TPPReconciler) takeTargets(key types.NamespacedName) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	prev := r.lastTargets[key]
	delete(r.lastTargets, key)
	return prev
}

// removedTargets returns the members of prev not present in current.
func removedTargets(prev []string, current map[string]struct{}) []string {
	var removed []string
	for _, name := range prev {
		if _, ok := current[name]; !ok {
			removed = append(removed, name)
		}
	}
	return removed
}

// SetupWithManager registers the controller. It reconciles a TPP only when its
// spec changes (generation bump) or it is deleted — the extension server reads
// spec fields only (mode, targetRefs, ruleSets, sampling), so status/metadata
// churn is ignored.
func (r *TPPReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1alpha.TrafficProtectionPolicy{}, builder.WithPredicates(tppSpecChangedPredicate())).
		Named("extension-server-gateway-retrigger-tpp").
		Complete(r)
}

// tppSpecChangedPredicate admits creates (so TPPs already present when the
// controller starts stamp their Gateways, and their target set is learned before
// any later edit or delete), updates that bump the generation (any spec change:
// mode flip, targetRef edit, ruleset/sampling change), and deletes (so a removed
// policy's Gateways are re-translated against the now-empty cache).
func tppSpecChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(event.CreateEvent) bool { return true },
		DeleteFunc: func(event.DeleteEvent) bool { return true },
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
