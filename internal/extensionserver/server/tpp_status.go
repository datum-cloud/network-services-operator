package server

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/extensionserver/mutate"
	gatewaystatus "go.datum.net/network-services-operator/internal/gatewayapi/status"
)

const (
	// edgeProgrammedControllerName identifies the extension server as the
	// writer of edge-local Programmed conditions. Upstream NSO mirrors the
	// Karmada-aggregated value onto the project-CP TPP.
	edgeProgrammedControllerName = "networking.datumapis.com/envoy-gateway-extension-server"

	conditionTypeProgrammed                                   = "Programmed"
	conditionReasonProgrammed gatewayv1.PolicyConditionReason = "Programmed"
)

// reportProgrammed writes the edge's Programmed report, after a build the hook
// returned to Envoy Gateway (built) or after a fact it depends on changed
// (built is empty). Programmed means Gateway API's "translated, assumed ready
// soon": a replica of Envoy Gateway on this edge built it and the hook returned
// it. An Envoy NACK, or a snapshot EG drops after the hook, is not seen here.
//
// Every replica of the extension server writes, without a leader, so the
// report follows GEP-713's rules for several writers, and replicas converge:
//   - it changes only the ancestors it owns (edgeProgrammedControllerName);
//   - a claim only advances: an ancestor is never written for an older
//     generation than it already reports, so a replica behind cannot undo one
//     ahead of it;
//   - a claim is removed only on facts every replica sees alike: the target left
//     the policy's spec, the policy is invalid (inverted paranoia levels), the
//     target or its rule no longer exists, or the claim is for a generation the
//     policy never had (it was recreated with its status, as a restore does). A build that lacks a target
//     removes nothing: two replicas of Envoy Gateway, or a partial build, may
//     lack one for a moment.
//
// Errors are logged; the next build or fact change reports again.
func (s *Server) reportProgrammed(ctx context.Context, built mutate.BuiltTPPs) {
	targets, err := buildTargetIndex(ctx, s.client)
	if err != nil {
		s.log.Error("index policy targets for programmed report", "err", err)
		return
	}
	var tpps networkingv1alpha.TrafficProtectionPolicyList
	if err := s.client.List(ctx, &tpps); err != nil {
		s.log.Error("list trafficprotectionpolicies for programmed report", "err", err)
		return
	}
	for i := range tpps.Items {
		tpp := &tpps.Items[i]
		if err := s.reportTPP(ctx, client.ObjectKeyFromObject(tpp), built[tpp.Namespace+"/"+tpp.Name], targets); err != nil {
			s.log.Error("report tpp programmed", "namespace", tpp.Namespace, "name", tpp.Name, "err", err)
		}
	}
}

// reportTPP rewrites the ancestors the extension server owns on one policy,
// reading the policy again on a conflict with the other replica.
func (s *Server) reportTPP(ctx context.Context, key client.ObjectKey, built *mutate.BuiltTPP, targets targetIndex) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		tpp := &networkingv1alpha.TrafficProtectionPolicy{}
		if err := s.client.Get(ctx, key, tpp); err != nil {
			return client.IgnoreNotFound(err)
		}
		ancestors := ancestorsAfterReport(tpp, built, targets)
		if equality.Semantic.DeepEqual(tpp.Status.Ancestors, ancestors) {
			return nil
		}
		tpp.Status.Ancestors = ancestors
		return s.client.Status().Update(ctx, tpp)
	})
}

// ancestorsAfterReport returns the policy's ancestors after one report, by the
// rules on reportProgrammed. Other controllers' ancestors and the order of all
// ancestors are kept, so an unchanged report compares equal and writes nothing.
func ancestorsAfterReport(tpp *networkingv1alpha.TrafficProtectionPolicy, built *mutate.BuiltTPP, targets targetIndex) []gatewayv1.PolicyAncestorStatus {
	wanted := map[string]gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{}
	var order []string
	if tpp.Spec.InvertedParanoiaLevels() == nil {
		for _, ref := range tpp.Spec.TargetRefs {
			key := refKey(ancestorRefForTarget(tpp.Namespace, ref))
			if _, dup := wanted[key]; !dup && targets.exists(tpp.Namespace, ref) {
				wanted[key] = ref
				order = append(order, key)
			}
		}
	}

	next := gatewayv1alpha2.PolicyStatus{Ancestors: []gatewayv1.PolicyAncestorStatus{}}
	kept := map[string]bool{}
	for _, ancestor := range tpp.Status.Ancestors {
		key := refKey(&ancestor.AncestorRef)
		if string(ancestor.ControllerName) == edgeProgrammedControllerName {
			if _, ok := wanted[key]; !ok {
				continue // the target left the spec or no longer exists, or the policy is invalid
			}
			if programmedGeneration([]gatewayv1.PolicyAncestorStatus{ancestor}, key) > tpp.Generation {
				continue // newer than the object itself: left from an earlier incarnation, as after a restore
			}
			kept[key] = true
		}
		next.Ancestors = append(next.Ancestors, *ancestor.DeepCopy())
	}
	for _, key := range order {
		ref := wanted[key]
		if !built.Has(ref) {
			continue // keep what is reported; a build without the target removes nothing
		}
		if kept[key] && programmedGeneration(next.Ancestors, key) > built.Generation {
			continue // never write an older generation over a newer one
		}
		gatewaystatus.SetConditionForPolicyAncestor(
			&next,
			ancestorRefForTarget(tpp.Namespace, ref),
			edgeProgrammedControllerName,
			gatewayv1.PolicyConditionType(conditionTypeProgrammed),
			metav1.ConditionTrue,
			conditionReasonProgrammed,
			"Policy has been programmed on this edge.",
			built.Generation,
		)
	}
	return next.Ancestors
}

// programmedGeneration returns the observedGeneration of the extension
// server's Programmed condition on the ancestor with key, or 0.
func programmedGeneration(ancestors []gatewayv1.PolicyAncestorStatus, key string) int64 {
	for _, ancestor := range ancestors {
		if string(ancestor.ControllerName) != edgeProgrammedControllerName || refKey(&ancestor.AncestorRef) != key {
			continue
		}
		for _, cond := range ancestor.Conditions {
			if cond.Type == conditionTypeProgrammed {
				return cond.ObservedGeneration
			}
		}
	}
	return 0
}

// refKey identifies an ancestor by its reference, absent fields as empty.
func refKey(ref *gatewayv1alpha2.ParentReference) string {
	return fmt.Sprintf("%s/%s/%s/%s/%s", deref(ref.Group), deref(ref.Kind), deref(ref.Namespace), ref.Name, deref(ref.SectionName))
}

func deref[T ~string](p *T) string {
	if p == nil {
		return ""
	}
	return string(*p)
}

func ancestorRefForTarget(namespace string, targetRef gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName) *gatewayv1alpha2.ParentReference {
	return &gatewayv1alpha2.ParentReference{
		Group:       ptr.To(targetRef.Group),
		Kind:        ptr.To(targetRef.Kind),
		Name:        targetRef.Name,
		Namespace:   ptr.To(gatewayv1.Namespace(namespace)),
		SectionName: targetRef.SectionName,
	}
}

func splitNamespaceName(key string) (namespace, name string, ok bool) {
	ns, name, found := strings.Cut(key, "/")
	if !found || ns == "" || name == "" || strings.Contains(name, "/") {
		return "", "", false
	}
	return ns, name, true
}
