package server

import (
	"context"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
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

// markTPPsProgrammed writes Programmed=True on each applied TrafficProtectionPolicy's
// edge-local status with observedGeneration matching the applied generation.
// Idempotent. Errors are logged and do not fail the xDS hook — the config is
// already in the snapshot.
func (s *Server) markTPPsProgrammed(ctx context.Context, applied map[string]int64) {
	if len(applied) == 0 {
		return
	}
	for key, gen := range applied {
		ns, name, ok := splitNamespaceName(key)
		if !ok {
			continue
		}
		if err := s.markTPPProgrammed(ctx, ns, name, gen); err != nil {
			s.log.Error("mark tpp programmed", "namespace", ns, "name", name, "generation", gen, "err", err)
		}
	}
}

func (s *Server) markTPPProgrammed(ctx context.Context, namespace, name string, generation int64) error {
	tpp := &networkingv1alpha.TrafficProtectionPolicy{}
	if err := s.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, tpp); err != nil {
		return err
	}

	original := tpp.Status.DeepCopy()
	for _, targetRef := range tpp.Spec.TargetRefs {
		ancestorRef := ancestorRefForTarget(tpp.Namespace, targetRef)
		gatewaystatus.SetConditionForPolicyAncestor(
			&tpp.Status.PolicyStatus,
			ancestorRef,
			edgeProgrammedControllerName,
			gatewayv1.PolicyConditionType(conditionTypeProgrammed),
			metav1.ConditionTrue,
			conditionReasonProgrammed,
			"Policy has been programmed on this edge.",
			generation,
		)
	}

	if equality.Semantic.DeepEqual(original, &tpp.Status) {
		return nil
	}
	return s.client.Status().Update(ctx, tpp)
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
