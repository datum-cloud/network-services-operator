// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	"context"
	"fmt"

	gatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"go.datum.net/network-services-operator/internal/validation"
)

// SetupBackendTrafficPolicyWebhookWithManager registers the webhook for BackendTrafficPolicy in the manager.
func SetupBackendTrafficPolicyWebhookWithManager(mgr mcmanager.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager()).For(&gatewayv1alpha1.BackendTrafficPolicy{}).
		WithValidator(&BackendTrafficPolicyCustomValidator{mgr: mgr}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-gateway-envoyproxy-io-v1alpha1-backendtrafficpolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=gateway.envoyproxy.io,resources=backendtrafficpolicies,verbs=create;update,versions=v1alpha1,name=vbackendtrafficpolicy-v1alpha1.kb.io,admissionReviewVersions=v1

type BackendTrafficPolicyCustomValidator struct {
	mgr mcmanager.Manager
}

var _ webhook.CustomValidator = &BackendTrafficPolicyCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type BackendTrafficPolicy.
func (v *BackendTrafficPolicyCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	backendTrafficPolicy, ok := obj.(*gatewayv1alpha1.BackendTrafficPolicy)
	if !ok {
		return nil, fmt.Errorf("expected a BackendTrafficPolicy object but got %T", obj)
	}

	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("expected a cluster name in the context")
	}

	log := logf.FromContext(ctx).WithValues("cluster", clusterName)
	log.Info("Validating BackendTrafficPolicy", "name", backendTrafficPolicy.GetName(), "cluster", clusterName)

	if errs := validation.ValidateBackendTrafficPolicy(backendTrafficPolicy); len(errs) > 0 {
		return nil, apierrors.NewInvalid(obj.GetObjectKind().GroupVersionKind().GroupKind(), backendTrafficPolicy.GetName(), errs)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type BackendTrafficPolicy.
func (v *BackendTrafficPolicyCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	backendTrafficPolicy, ok := newObj.(*gatewayv1alpha1.BackendTrafficPolicy)
	if !ok {
		return nil, fmt.Errorf("expected a BackendTrafficPolicy object for the newObj but got %T", newObj)
	}

	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("expected a cluster name in the context")
	}

	log := logf.FromContext(ctx).WithValues("cluster", clusterName)
	log.Info("Validating BackendTrafficPolicy", "name", backendTrafficPolicy.GetName(), "cluster", clusterName)

	if errs := validation.ValidateBackendTrafficPolicy(backendTrafficPolicy); len(errs) > 0 {
		return nil, apierrors.NewInvalid(oldObj.GetObjectKind().GroupVersionKind().GroupKind(), backendTrafficPolicy.GetName(), errs)
	}
	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type BackendTrafficPolicy.
func (v *BackendTrafficPolicyCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}
