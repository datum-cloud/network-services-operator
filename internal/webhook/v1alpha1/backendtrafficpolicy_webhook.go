// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	"context"
	"fmt"

	gatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/validation"
)

// SetupBackendTrafficPolicyWebhookWithManager registers the webhook for BackendTrafficPolicy in the manager.
func SetupBackendTrafficPolicyWebhookWithManager(mgr mcmanager.Manager, cfg config.NetworkServicesOperator) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager(), &gatewayv1alpha1.BackendTrafficPolicy{}).
		WithValidator(&BackendTrafficPolicyCustomValidator{mgr: mgr, validationOpts: cfg.Gateway.ExtensionAPIValidationOptions.BackendTrafficPolicies}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-gateway-envoyproxy-io-v1alpha1-backendtrafficpolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=gateway.envoyproxy.io,resources=backendtrafficpolicies,verbs=create;update,versions=v1alpha1,name=vbackendtrafficpolicy-v1alpha1.kb.io,admissionReviewVersions=v1

type BackendTrafficPolicyCustomValidator struct {
	mgr            mcmanager.Manager
	validationOpts config.BackendTrafficPolicyValidationOptions
}

var _ admission.Validator[*gatewayv1alpha1.BackendTrafficPolicy] = &BackendTrafficPolicyCustomValidator{}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type BackendTrafficPolicy.
func (v *BackendTrafficPolicyCustomValidator) ValidateCreate(ctx context.Context, backendTrafficPolicy *gatewayv1alpha1.BackendTrafficPolicy) (admission.Warnings, error) {
	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("expected a cluster name in the context")
	}

	log := logf.FromContext(ctx).WithValues("cluster", clusterName)
	log.Info("Validating BackendTrafficPolicy", "name", backendTrafficPolicy.GetName(), "cluster", clusterName)

	if errs := validation.ValidateBackendTrafficPolicy(backendTrafficPolicy, v.validationOpts); len(errs) > 0 {
		return nil, apierrors.NewInvalid(backendTrafficPolicy.GetObjectKind().GroupVersionKind().GroupKind(), backendTrafficPolicy.GetName(), errs)
	}

	return nil, nil
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type BackendTrafficPolicy.
func (v *BackendTrafficPolicyCustomValidator) ValidateUpdate(ctx context.Context, oldBackendTrafficPolicy, newBackendTrafficPolicy *gatewayv1alpha1.BackendTrafficPolicy) (admission.Warnings, error) {
	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("expected a cluster name in the context")
	}

	log := logf.FromContext(ctx).WithValues("cluster", clusterName)
	log.Info("Validating BackendTrafficPolicy", "name", newBackendTrafficPolicy.GetName(), "cluster", clusterName)

	if errs := validation.ValidateBackendTrafficPolicy(newBackendTrafficPolicy, v.validationOpts); len(errs) > 0 {
		return nil, apierrors.NewInvalid(oldBackendTrafficPolicy.GetObjectKind().GroupVersionKind().GroupKind(), newBackendTrafficPolicy.GetName(), errs)
	}
	return nil, nil
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type BackendTrafficPolicy.
func (v *BackendTrafficPolicyCustomValidator) ValidateDelete(ctx context.Context, backendTrafficPolicy *gatewayv1alpha1.BackendTrafficPolicy) (admission.Warnings, error) {
	return nil, nil
}
