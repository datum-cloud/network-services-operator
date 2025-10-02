// SPDX-License-Identifier: AGPL-3.0-only

package v1

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha3 "sigs.k8s.io/gateway-api/apis/v1alpha3"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"go.datum.net/network-services-operator/internal/validation"
)

// nolint:unused

// Note: move to struct when gateway-api dep is updated to 1.4.x
var backendTLSPolicyV1GVK = gatewayv1.SchemeGroupVersion.WithKind("BackendTLSPolicy")

// SetupBackendTLSPolicyWebhookWithManager registers the webhook for BackendTLSPolicy in the manager.
func SetupBackendTLSPolicyWebhookWithManager(mgr mcmanager.Manager) error {
	backendTLSPolicy := &unstructured.Unstructured{}
	backendTLSPolicy.SetGroupVersionKind(backendTLSPolicyV1GVK)
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager()).For(backendTLSPolicy).
		WithValidator(&BackendTLSPolicyCustomValidator{mgr: mgr}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-gateway-networking-k8s-io-v1-backendtlspolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=gateway.networking.k8s.io,resources=backendtlspolicies,verbs=create;update,versions=v1,name=vbackendtlspolicy-v1.kb.io,admissionReviewVersions=v1

type BackendTLSPolicyCustomValidator struct {
	mgr mcmanager.Manager
}

var _ webhook.CustomValidator = &BackendTLSPolicyCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type BackendTLSPolicy.
func (v *BackendTLSPolicyCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	unstructuredPolicy, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("expected a BackendTLSPolicy object but got %T", obj)
	}

	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("expected a cluster name in the context")
	}

	var backendTLSPolicy gatewayv1alpha3.BackendTLSPolicy
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredPolicy.Object, &backendTLSPolicy); err != nil {
		return nil, fmt.Errorf("failed to convert unstructured to BackendTLSPolicy: %w", err)
	}

	logger := logf.FromContext(ctx).WithValues("cluster", clusterName)
	logger.Info("Validating BackendTLSPolicy", "name", backendTLSPolicy.GetName(), "cluster", clusterName)

	if errs := validation.ValidateBackendTLSPolicy(&backendTLSPolicy); len(errs) > 0 {
		return nil, apierrors.NewInvalid(obj.GetObjectKind().GroupVersionKind().GroupKind(), backendTLSPolicy.GetName(), errs)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type BackendTLSPolicy.
func (v *BackendTLSPolicyCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	unstructuredPolicy, ok := newObj.(*unstructured.Unstructured)
	if !ok {
		return nil, fmt.Errorf("expected a BackendTLSPolicy object but got %T", newObj)
	}

	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("expected a cluster name in the context")
	}

	var backendTLSPolicy gatewayv1alpha3.BackendTLSPolicy
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredPolicy.Object, &backendTLSPolicy); err != nil {
		return nil, fmt.Errorf("failed to convert unstructured to BackendTLSPolicy: %w", err)
	}

	logger := logf.FromContext(ctx).WithValues("cluster", clusterName)
	logger.Info("Validating BackendTLSPolicy", "name", backendTLSPolicy.GetName())

	if errs := validation.ValidateBackendTLSPolicy(&backendTLSPolicy); len(errs) > 0 {
		return nil, apierrors.NewInvalid(oldObj.GetObjectKind().GroupVersionKind().GroupKind(), backendTLSPolicy.GetName(), errs)
	}

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type BackendTLSPolicy.
func (v *BackendTLSPolicyCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}
