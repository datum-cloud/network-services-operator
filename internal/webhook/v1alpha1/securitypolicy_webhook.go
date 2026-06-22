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

// SetupSecurityPolicyWebhookWithManager registers the webhook for SecurityPolicy in the manager.
func SetupSecurityPolicyWebhookWithManager(mgr mcmanager.Manager, cfg config.NetworkServicesOperator) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager(), &gatewayv1alpha1.SecurityPolicy{}).
		WithValidator(&SecurityPolicyCustomValidator{mgr: mgr, validationOpts: cfg.Gateway.ExtensionAPIValidationOptions.SecurityPolicies}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-gateway-envoyproxy-io-v1alpha1-securitypolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=gateway.envoyproxy.io,resources=securitypolicies,verbs=create;update,versions=v1alpha1,name=vsecuritypolicy-v1alpha1.kb.io,admissionReviewVersions=v1

type SecurityPolicyCustomValidator struct {
	mgr            mcmanager.Manager
	validationOpts config.SecurityPolicyValidationOptions
}

var _ admission.Validator[*gatewayv1alpha1.SecurityPolicy] = &SecurityPolicyCustomValidator{}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type SecurityPolicy.
func (v *SecurityPolicyCustomValidator) ValidateCreate(ctx context.Context, securityPolicy *gatewayv1alpha1.SecurityPolicy) (admission.Warnings, error) {
	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("expected a cluster name in the context")
	}

	log := logf.FromContext(ctx).WithValues("cluster", clusterName)
	log.Info("Validating SecurityPolicy", "name", securityPolicy.GetName(), "cluster", clusterName)

	if errs := validation.ValidateSecurityPolicy(securityPolicy, v.validationOpts); len(errs) > 0 {
		return nil, apierrors.NewInvalid(securityPolicy.GetObjectKind().GroupVersionKind().GroupKind(), securityPolicy.GetName(), errs)
	}

	return nil, nil
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type SecurityPolicy.
func (v *SecurityPolicyCustomValidator) ValidateUpdate(ctx context.Context, oldSecurityPolicy, newSecurityPolicy *gatewayv1alpha1.SecurityPolicy) (admission.Warnings, error) {
	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("expected a cluster name in the context")
	}

	log := logf.FromContext(ctx).WithValues("cluster", clusterName)
	log.Info("Validating SecurityPolicy", "name", newSecurityPolicy.GetName(), "cluster", clusterName)

	if errs := validation.ValidateSecurityPolicy(newSecurityPolicy, v.validationOpts); len(errs) > 0 {
		return nil, apierrors.NewInvalid(oldSecurityPolicy.GetObjectKind().GroupVersionKind().GroupKind(), newSecurityPolicy.GetName(), errs)
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type SecurityPolicy.
func (v *SecurityPolicyCustomValidator) ValidateDelete(ctx context.Context, securityPolicy *gatewayv1alpha1.SecurityPolicy) (admission.Warnings, error) {
	return nil, nil
}
