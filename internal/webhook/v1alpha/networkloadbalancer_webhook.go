// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"context"
	"slices"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// +kubebuilder:webhook:path=/validate-networking-datumapis-com-v1alpha-networkloadbalancer,mutating=false,failurePolicy=fail,sideEffects=None,groups=networking.datumapis.com,resources=networkloadbalancers,verbs=create;update,versions=v1alpha,name=vnetworkloadbalancer-v1alpha.kb.io,admissionReviewVersions=v1

type NetworkLoadBalancerValidator struct {
	IPv4Enabled bool
}

var _ admission.Validator[*networkingv1alpha.NetworkLoadBalancer] = &NetworkLoadBalancerValidator{}

func SetupNetworkLoadBalancerWebhookWithManager(mgr mcmanager.Manager, ipv4Enabled bool) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager(), &networkingv1alpha.NetworkLoadBalancer{}).
		WithValidator(&NetworkLoadBalancerValidator{IPv4Enabled: ipv4Enabled}).Complete()
}

func (v *NetworkLoadBalancerValidator) ValidateCreate(_ context.Context, lb *networkingv1alpha.NetworkLoadBalancer) (admission.Warnings, error) {
	return nil, v.validateCapabilities(lb)
}

func (v *NetworkLoadBalancerValidator) ValidateUpdate(_ context.Context, _, lb *networkingv1alpha.NetworkLoadBalancer) (admission.Warnings, error) {
	return nil, v.validateCapabilities(lb)
}

func (v *NetworkLoadBalancerValidator) ValidateDelete(context.Context, *networkingv1alpha.NetworkLoadBalancer) (admission.Warnings, error) {
	return nil, nil
}

func (v *NetworkLoadBalancerValidator) validateCapabilities(lb *networkingv1alpha.NetworkLoadBalancer) error {
	var errs field.ErrorList
	for i, request := range lb.Spec.Exposure.Addresses {
		if !v.IPv4Enabled && slices.Contains(request.Families, networkingv1alpha.IPv4Protocol) {
			errs = append(errs, field.Forbidden(field.NewPath("spec", "exposure", "addresses").Index(i).Child("families"),
				"UnsupportedCapability: public IPv4 NLB ingress is unavailable"))
		}
	}
	for i, listener := range lb.Spec.Listeners {
		path := field.NewPath("spec", "listeners").Index(i)
		if len(listener.AddressRefs) > 1 {
			errs = append(errs, field.Forbidden(path.Child("addressRefs"),
				"UnsupportedCapability: a listener can use only one address request until multi-VIP serving is supported"))
		}
		if listener.Protocol == networkingv1alpha.NetworkLoadBalancerProtocolTLS || listener.TLS != nil || listener.Hostname != "" {
			errs = append(errs, field.Forbidden(path, "UnsupportedCapability: TLS listeners require a proxy-capable edge path"))
		}
		for _, addressName := range listener.AddressRefs {
			if slices.ContainsFunc(lb.Spec.Listeners[:i], func(other networkingv1alpha.NetworkLoadBalancerListener) bool {
				return slices.Contains(other.AddressRefs, addressName)
			}) {
				errs = append(errs, field.Forbidden(path.Child("addressRefs"),
					"UnsupportedCapability: one address request cannot be shared by multiple listeners yet"))
			}
		}
	}
	if len(errs) != 0 {
		return apierrors.NewInvalid(schema.GroupKind{Group: networkingv1alpha.GroupVersion.Group, Kind: "NetworkLoadBalancer"}, lb.Name, errs)
	}
	return nil
}
