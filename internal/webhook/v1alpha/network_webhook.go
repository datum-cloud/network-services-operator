// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/validation"
	webhookutil "go.datum.net/network-services-operator/internal/webhook"
)

// SetupNetworkWebhookWithManager registers the webhook for Network in the manager.
func SetupNetworkWebhookWithManager(mgr mcmanager.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager(), &networkingv1alpha.Network{}).
		WithValidator(&NetworkCustomValidator{}).
		Complete()
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
