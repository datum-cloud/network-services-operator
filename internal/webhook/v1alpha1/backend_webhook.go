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

// SetupBackendWebhookWithManager registers the webhook for Backend in the manager.
func SetupBackendWebhookWithManager(mgr mcmanager.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager()).For(&gatewayv1alpha1.Backend{}).
		WithValidator(&BackendCustomValidator{mgr: mgr}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-gateway-envoyproxy-io-v1alpha1-backend,mutating=false,failurePolicy=fail,sideEffects=None,groups=gateway.envoyproxy.io,resources=backends,verbs=create;update,versions=v1alpha1,name=vbackend-v1alpha1.kb.io,admissionReviewVersions=v1

type BackendCustomValidator struct {
	mgr mcmanager.Manager
}

var _ webhook.CustomValidator = &BackendCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type Backend.
func (v *BackendCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	backend, ok := obj.(*gatewayv1alpha1.Backend)
	if !ok {
		return nil, fmt.Errorf("expected a Backend object but got %T", obj)
	}

	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("expected a cluster name in the context")
	}

	log := logf.FromContext(ctx).WithValues("cluster", clusterName)
	log.Info("Validating Backend", "name", backend.GetName(), "cluster", clusterName)

	if errs := validation.ValidateBackend(backend); len(errs) > 0 {
		return nil, apierrors.NewInvalid(obj.GetObjectKind().GroupVersionKind().GroupKind(), backend.GetName(), errs)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type Backend.
func (v *BackendCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	backend, ok := newObj.(*gatewayv1alpha1.Backend)
	if !ok {
		return nil, fmt.Errorf("expected a Backend object but got %T", newObj)
	}

	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("expected a cluster name in the context")
	}

	log := logf.FromContext(ctx).WithValues("cluster", clusterName)
	log.Info("Validating Backend", "name", backend.GetName(), "cluster", clusterName)

	if errs := validation.ValidateBackend(backend); len(errs) > 0 {
		return nil, apierrors.NewInvalid(oldObj.GetObjectKind().GroupVersionKind().GroupKind(), backend.GetName(), errs)
	}

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type Backend.
func (v *BackendCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	return nil, nil
}
