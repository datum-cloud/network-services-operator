// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"context"
	"slices"

	"k8s.io/apimachinery/pkg/api/equality"
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
	Config      config.NetworkLoadBalancerConfig
	IPv4Enabled bool
}

var _ admission.Validator[*networkingv1alpha.NetworkLoadBalancer] = &NetworkLoadBalancerValidator{}

func SetupNetworkLoadBalancerWebhookWithManager(mgr mcmanager.Manager, ipv4Enabled bool, cfg config.NetworkLoadBalancerConfig) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager(), &networkingv1alpha.NetworkLoadBalancer{}).
		WithValidator(&NetworkLoadBalancerValidator{Config: cfg, IPv4Enabled: ipv4Enabled}).Complete()
}

func (v *NetworkLoadBalancerValidator) ValidateCreate(_ context.Context, lb *networkingv1alpha.NetworkLoadBalancer) (admission.Warnings, error) {
	return nil, v.validateCapabilities(lb)
}

func (v *NetworkLoadBalancerValidator) ValidateUpdate(_ context.Context, old, lb *networkingv1alpha.NetworkLoadBalancer) (admission.Warnings, error) {
	if equality.Semantic.DeepEqual(old.Spec, lb.Spec) {
		return nil, nil
	}
	cfg := v.Config
	if nonExpandingLoadBalancerUpdate(old, lb) {
		cfg.SharedAddressListeners, cfg.UDPPortRanges = true, true
		if coverage := loadBalancerPortCoverage(old); coverage > cfg.MaxExposedPorts {
			cfg.MaxExposedPorts = coverage
		}
	}
	validator := *v
	validator.Config = cfg
	return nil, validator.validateCapabilities(lb)
}

func (v *NetworkLoadBalancerValidator) ValidateDelete(context.Context, *networkingv1alpha.NetworkLoadBalancer) (admission.Warnings, error) {
	return nil, nil
}

func (v *NetworkLoadBalancerValidator) validateCapabilities(lb *networkingv1alpha.NetworkLoadBalancer) error {
	cfg := v.Config
	maxPorts := cfg.MaxExposedPorts
	if maxPorts <= 0 {
		maxPorts = 1024
	}
	var coverage int32
	var errs field.ErrorList
	for i, request := range lb.Spec.Exposure.Addresses {
		if slices.Contains(request.Families, networkingv1alpha.IPv4Protocol) && addressRequiresCoordination(lb, request.Name) {
			errs = append(errs, field.Forbidden(field.NewPath("spec", "exposure", "addresses").Index(i).Child("families"),
				"UnsupportedCapability: shared listeners and UDP ranges require IPv6-only addresses"))
		}
		if !v.IPv4Enabled && slices.Contains(request.Families, networkingv1alpha.IPv4Protocol) {
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

func nonExpandingLoadBalancerUpdate(old, next *networkingv1alpha.NetworkLoadBalancer) bool {
	if old.Spec.NetworkRef != next.Spec.NetworkRef {
		return false
	}
	for _, listener := range next.Spec.Listeners {
		i := slices.IndexFunc(old.Spec.Listeners, func(prior networkingv1alpha.NetworkLoadBalancerListener) bool { return prior.Name == listener.Name })
		if i < 0 {
			return false
		}
		prior := old.Spec.Listeners[i]
		if prior.Protocol != listener.Protocol || prior.Hostname != listener.Hostname || !equality.Semantic.DeepEqual(prior.TLS, listener.TLS) || !networkingv1alpha.PortInterval(prior.Port, prior.PortRange).Contains(networkingv1alpha.PortInterval(listener.Port, listener.PortRange)) {
			return false
		}
		for _, ref := range listener.AddressRefs {
			if !slices.Contains(prior.AddressRefs, ref) {
				return false
			}
		}
	}
	for _, request := range next.Spec.Exposure.Addresses {
		i := slices.IndexFunc(old.Spec.Exposure.Addresses, func(prior networkingv1alpha.NetworkLoadBalancerAddressRequest) bool {
			return prior.Name == request.Name
		})
		if i < 0 {
			return false
		}
		prior := old.Spec.Exposure.Addresses[i]
		for _, family := range request.Families {
			if !slices.Contains(prior.Families, family) {
				return false
			}
		}
		if equality.Semantic.DeepEqual(prior.Locations, request.Locations) {
			continue
		}
		if prior.Locations == nil || request.Locations == nil || request.Locations.From != networkingv1alpha.NetworkLoadBalancerLocationSourceNames {
			return false
		}
		if prior.Locations.From == networkingv1alpha.NetworkLoadBalancerLocationSourceAll {
			continue
		}
		if prior.Locations.From != networkingv1alpha.NetworkLoadBalancerLocationSourceNames {
			return false
		}
		for _, name := range request.Locations.Names {
			if !slices.Contains(prior.Locations.Names, name) {
				return false
			}
		}
	}
	return true
}

func loadBalancerPortCoverage(lb *networkingv1alpha.NetworkLoadBalancer) int32 {
	var total int32
	for _, listener := range lb.Spec.Listeners {
		total += networkingv1alpha.PortInterval(listener.Port, listener.PortRange).Width() * int32(len(listener.AddressRefs))
	}
	return total
}

func addressRequiresCoordination(lb *networkingv1alpha.NetworkLoadBalancer, name string) bool {
	listeners := 0
	for _, listener := range lb.Spec.Listeners {
		if !slices.Contains(listener.AddressRefs, name) {
			continue
		}
		listeners++
		if listeners > 1 || !networkingv1alpha.PortInterval(listener.Port, listener.PortRange).Singleton() {
			return true
		}
	}
	return false
}
