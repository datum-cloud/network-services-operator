// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// +kubebuilder:webhook:path=/validate-networking-datumapis-com-v1alpha-networkloadbalancerroute,mutating=false,failurePolicy=fail,sideEffects=None,groups=networking.datumapis.com,resources=networkloadbalancerroutes,verbs=create;update,versions=v1alpha,name=vnetworkloadbalancerroute-v1alpha.kb.io,admissionReviewVersions=v1

type NetworkLoadBalancerRouteValidator struct{}

var _ admission.Validator[*networkingv1alpha.NetworkLoadBalancerRoute] = &NetworkLoadBalancerRouteValidator{}

func SetupNetworkLoadBalancerRouteWebhookWithManager(mgr mcmanager.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager(), &networkingv1alpha.NetworkLoadBalancerRoute{}).
		WithValidator(&NetworkLoadBalancerRouteValidator{}).Complete()
}

func (v *NetworkLoadBalancerRouteValidator) ValidateCreate(_ context.Context, route *networkingv1alpha.NetworkLoadBalancerRoute) (admission.Warnings, error) {
	return nil, validateNetworkLoadBalancerRouteCapabilities(route)
}

func (v *NetworkLoadBalancerRouteValidator) ValidateUpdate(_ context.Context, _, route *networkingv1alpha.NetworkLoadBalancerRoute) (admission.Warnings, error) {
	return nil, validateNetworkLoadBalancerRouteCapabilities(route)
}

func (v *NetworkLoadBalancerRouteValidator) ValidateDelete(context.Context, *networkingv1alpha.NetworkLoadBalancerRoute) (admission.Warnings, error) {
	return nil, nil
}

func validateNetworkLoadBalancerRouteCapabilities(route *networkingv1alpha.NetworkLoadBalancerRoute) error {
	var errs field.ErrorList
	seenParents := make(map[string]struct{}, len(route.Spec.ParentRefs))
	if len(route.Spec.Backends) > 1 {
		errs = append(errs, field.Forbidden(field.NewPath("spec", "backends"),
			"UnsupportedCapability: multiple backends require complete endpoint selection in serving cells"))
	}
	if len(route.Spec.SNIHostnames) > 0 {
		errs = append(errs, field.Forbidden(field.NewPath("spec", "sniHostnames"),
			"UnsupportedCapability: SNI routing requires a proxy-capable edge path"))
	}
	for i, parent := range route.Spec.ParentRefs {
		namespace := parent.Namespace
		if namespace == "" {
			namespace = route.Namespace
		}
		selector := "listener:" + parent.Listener
		if parent.Port != nil {
			selector = fmt.Sprintf("port:%d", *parent.Port)
		}
		key := namespace + "/" + parent.Name + "/" + selector
		if _, exists := seenParents[key]; exists {
			errs = append(errs, field.Duplicate(field.NewPath("spec", "parentRefs").Index(i), parent))
		}
		seenParents[key] = struct{}{}
		if parent.Namespace != "" && parent.Namespace != route.Namespace {
			errs = append(errs, field.Forbidden(field.NewPath("spec", "parentRefs").Index(i).Child("namespace"),
				"UnsupportedCapability: cross-namespace parent references require grants"))
		}
	}
	for i, backend := range route.Spec.Backends {
		path := field.NewPath("spec", "backends").Index(i)
		if check := backend.HealthCheck; check != nil && (check.IntervalSeconds != nil || check.TimeoutSeconds != nil ||
			check.HealthyThreshold != nil || check.UnhealthyThreshold != nil) {
			errs = append(errs, field.Forbidden(path.Child("healthCheck"),
				"UnsupportedCapability: custom health timing and thresholds are not supported by serving cells"))
		}
		if backend.Transport != nil {
			errs = append(errs, field.Forbidden(path.Child("transport"),
				"UnsupportedCapability: backend TLS transport requires a proxy-capable edge path"))
		}
		if backend.NetworkService.Namespace != "" && backend.NetworkService.Namespace != route.Namespace {
			errs = append(errs, field.Forbidden(path.Child("networkService", "namespace"),
				"UnsupportedCapability: cross-namespace backends require grants"))
		}
	}
	if len(errs) != 0 {
		return apierrors.NewInvalid(schema.GroupKind{Group: networkingv1alpha.GroupVersion.Group, Kind: "NetworkLoadBalancerRoute"}, route.Name, errs)
	}
	return nil
}
