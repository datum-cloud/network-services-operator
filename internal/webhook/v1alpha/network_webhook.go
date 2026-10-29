// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"context"
	"encoding/json"
	"fmt"

	admissionv1 "k8s.io/api/admission/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/validation"
	webhookutil "go.datum.net/network-services-operator/internal/webhook"
)

// SetupNetworkWebhookWithManager registers the webhook for Network in the manager.
func SetupNetworkWebhookWithManager(mgr mcmanager.Manager, egress config.NetworkEgressConfig) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager(), &networkingv1alpha.Network{}).
		WithDefaulter(&NetworkCustomDefaulter{DefaultInternetMode: egress.DefaultInternetMode}).
		WithValidator(&NetworkCustomValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-networking-datumapis-com-v1alpha-network,mutating=true,failurePolicy=fail,sideEffects=None,groups=networking.datumapis.com,resources=networks,verbs=create;update,versions=v1alpha,name=mnetwork-v1alpha.kb.io,admissionReviewVersions=v1

// NetworkCustomDefaulter stamps a network with an internet access mode, so
// every stored network records the mode it runs with and none depends on what
// the platform default happens to be later.
type NetworkCustomDefaulter struct {
	DefaultInternetMode networkingv1alpha.NetworkInternetEgressMode
}

var _ admission.Defaulter[*networkingv1alpha.Network] = &NetworkCustomDefaulter{}

// Default implements admission.Defaulter so a webhook will be registered for the type Network.
//
// An update that drops the mode keeps the one the network already had, so
// reapplying a manifest written before the field existed never moves a
// network onto a platform default that has changed since it was created.
func (d *NetworkCustomDefaulter) Default(ctx context.Context, network *networkingv1alpha.Network) error {
	if internetEgressMode(network) != "" {
		return nil
	}

	mode := d.DefaultInternetMode
	previous, err := previousNetwork(ctx)
	if err != nil {
		return err
	}
	if previous != nil && internetEgressMode(previous) != "" {
		mode = internetEgressMode(previous)
	}
	if mode == "" {
		return nil
	}

	SetInternetEgressMode(network, mode)
	return nil
}

// SetInternetEgressMode writes mode into the network's spec.
func SetInternetEgressMode(network *networkingv1alpha.Network, mode networkingv1alpha.NetworkInternetEgressMode) {
	if network.Spec.Egress == nil {
		network.Spec.Egress = &networkingv1alpha.NetworkEgress{}
	}
	if network.Spec.Egress.Internet == nil {
		network.Spec.Egress.Internet = &networkingv1alpha.NetworkInternetEgress{}
	}
	network.Spec.Egress.Internet.Mode = mode
}

func internetEgressMode(network *networkingv1alpha.Network) networkingv1alpha.NetworkInternetEgressMode {
	if network.Spec.Egress == nil || network.Spec.Egress.Internet == nil {
		return ""
	}
	return network.Spec.Egress.Internet.Mode
}

func previousNetwork(ctx context.Context) (*networkingv1alpha.Network, error) {
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed reading the admission request: %w", err)
	}
	if req.Operation != admissionv1.Update || len(req.OldObject.Raw) == 0 {
		return nil, nil
	}
	previous := &networkingv1alpha.Network{}
	if err := json.Unmarshal(req.OldObject.Raw, previous); err != nil {
		return nil, fmt.Errorf("failed decoding the network being updated: %w", err)
	}
	return previous, nil
}

// +kubebuilder:webhook:path=/validate-networking-datumapis-com-v1alpha-network,mutating=false,failurePolicy=fail,sideEffects=None,groups=networking.datumapis.com,resources=networks,verbs=create;update,versions=v1alpha,name=vnetwork-v1alpha.kb.io,admissionReviewVersions=v1

type NetworkCustomValidator struct{}

var _ admission.Validator[*networkingv1alpha.Network] = &NetworkCustomValidator{}

var networkGroupKind = schema.GroupKind{
	Group: networkingv1alpha.GroupVersion.Group,
	Kind:  "Network",
}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type Network.
func (v *NetworkCustomValidator) ValidateCreate(ctx context.Context, network *networkingv1alpha.Network) (admission.Warnings, error) {
	if errs := validation.ValidateNetwork(network); len(errs) > 0 {
		return nil, apierrors.NewInvalid(networkGroupKind, network.GetName(), errs)
	}

	return nil, nil
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type Network.
func (v *NetworkCustomValidator) ValidateUpdate(ctx context.Context, oldNetwork, newNetwork *networkingv1alpha.Network) (admission.Warnings, error) {
	if webhookutil.SkipUpdateValidation(newNetwork, oldNetwork.Spec, newNetwork.Spec) {
		return nil, nil
	}

	if errs := validation.ValidateNetworkUpdate(newNetwork, oldNetwork); len(errs) > 0 {
		return nil, apierrors.NewInvalid(networkGroupKind, newNetwork.GetName(), errs)
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type Network.
func (v *NetworkCustomValidator) ValidateDelete(ctx context.Context, network *networkingv1alpha.Network) (admission.Warnings, error) {
	return nil, nil
}
