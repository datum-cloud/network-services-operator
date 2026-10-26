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
	"go.datum.net/network-services-operator/internal/config"
)

// +kubebuilder:webhook:path=/validate-networking-datumapis-com-v1alpha-networkloadbalancer,mutating=false,failurePolicy=fail,sideEffects=None,groups=networking.datumapis.com,resources=networkloadbalancers,verbs=create;update,versions=v1alpha,name=vnetworkloadbalancer-v1alpha.kb.io,admissionReviewVersions=v1

type NetworkLoadBalancerValidator struct {
	Config config.NetworkLoadBalancerConfig
}

var _ admission.Validator[*networkingv1alpha.NetworkLoadBalancer] = &NetworkLoadBalancerValidator{}

func SetupNetworkLoadBalancerWebhookWithManager(mgr mcmanager.Manager, settings ...config.NetworkLoadBalancerConfig) error {
	var cfg config.NetworkLoadBalancerConfig
	if len(settings) > 0 {
		cfg = settings[0]
	}
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager(), &networkingv1alpha.NetworkLoadBalancer{}).
		WithValidator(&NetworkLoadBalancerValidator{Config: cfg}).Complete()
}

func (v *NetworkLoadBalancerValidator) ValidateCreate(_ context.Context, lb *networkingv1alpha.NetworkLoadBalancer) (admission.Warnings, error) {
	return nil, validateNetworkLoadBalancerCapabilities(lb, v.Config)
}

func (v *NetworkLoadBalancerValidator) ValidateUpdate(_ context.Context, _, lb *networkingv1alpha.NetworkLoadBalancer) (admission.Warnings, error) {
	return nil, validateNetworkLoadBalancerCapabilities(lb, v.Config)
}

func (v *NetworkLoadBalancerValidator) ValidateDelete(context.Context, *networkingv1alpha.NetworkLoadBalancer) (admission.Warnings, error) {
	return nil, nil
}

func validateNetworkLoadBalancerCapabilities(lb *networkingv1alpha.NetworkLoadBalancer, settings ...config.NetworkLoadBalancerConfig) error {
	var cfg config.NetworkLoadBalancerConfig
	if len(settings) > 0 {
		cfg = settings[0]
	}
	maxPorts := cfg.MaxExposedPorts
	if maxPorts <= 0 {
		maxPorts = 1024
	}
	var coverage int32
	var errs field.ErrorList
	for i, request := range lb.Spec.Exposure.Addresses {
		if slices.Contains(request.Families, networkingv1alpha.IPv4Protocol) {
			errs = append(errs, field.Forbidden(field.NewPath("spec", "exposure", "addresses").Index(i).Child("families"),
				"UnsupportedCapability: public IPv4 NLB ingress is unavailable"))
		}
	}
	for i, listener := range lb.Spec.Listeners {
		path := field.NewPath("spec", "listeners").Index(i)
		interval := networkingv1alpha.PortInterval(listener.Port, listener.PortRange)
		if !interval.Valid() || (listener.Port != 0) == (listener.PortRange != nil) {
			errs = append(errs, field.Invalid(path, listener.Name, "exactly one valid port or portRange is required"))
		} else {
			coverage += interval.Width() * int32(len(listener.AddressRefs))
		}
		if listener.PortRange != nil && !interval.Singleton() && (!cfg.UDPPortRanges || listener.Protocol != networkingv1alpha.NetworkLoadBalancerProtocolUDP) {
			errs = append(errs, field.Forbidden(path.Child("portRange"), "UnsupportedCapability: UDP port ranges are not enabled"))
		}
		if len(listener.AddressRefs) > 1 {
			errs = append(errs, field.Forbidden(path.Child("addressRefs"),
				"UnsupportedCapability: a listener can use only one address request until multi-VIP serving is supported"))
		}
		if listener.Protocol == networkingv1alpha.NetworkLoadBalancerProtocolTLS || listener.TLS != nil || listener.Hostname != "" {
			errs = append(errs, field.Forbidden(path, "UnsupportedCapability: TLS listeners require a proxy-capable edge path"))
		}
		for _, addressName := range listener.AddressRefs {
			if !cfg.SharedAddressListeners && slices.ContainsFunc(lb.Spec.Listeners[:i], func(other networkingv1alpha.NetworkLoadBalancerListener) bool {
				return slices.Contains(other.AddressRefs, addressName)
			}) {
				errs = append(errs, field.Forbidden(path.Child("addressRefs"),
					"UnsupportedCapability: shared address listeners are not enabled"))
			}
		}
	}
	if coverage > maxPorts {
		errs = append(errs, field.Forbidden(field.NewPath("spec", "listeners"), "exposed port coverage exceeds the configured capacity limit"))
	}
	if len(errs) != 0 {
		return apierrors.NewInvalid(schema.GroupKind{Group: networkingv1alpha.GroupVersion.Group, Kind: "NetworkLoadBalancer"}, lb.Name, errs)
	}
	return nil
}
