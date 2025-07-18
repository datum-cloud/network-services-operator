// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"go.datum.net/network-services-operator/internal/validation"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// nolint:unused

// SetupHTTPProxyWebhookWithManager registers the webhook for HTTPProxy in the manager.
func SetupHTTPProxyWebhookWithManager(mgr mcmanager.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager()).For(&networkingv1alpha.HTTPProxy{}).
		WithValidator(&HTTPProxyCustomValidator{mgr: mgr}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-networking-datumapis-com-v1alpha-httpproxy,mutating=false,failurePolicy=fail,sideEffects=None,groups=networking.datumapis.com,resources=httpproxies,verbs=create;update,versions=v1alpha,name=vhttpproxy-v1alpha.kb.io,admissionReviewVersions=v1

type HTTPProxyCustomValidator struct {
	mgr mcmanager.Manager
}

var _ webhook.CustomValidator = &HTTPProxyCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type HTTPProxy.
func (v *HTTPProxyCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	httpProxy, ok := obj.(*networkingv1alpha.HTTPProxy)
	if !ok {
		return nil, fmt.Errorf("expected a HTTPProxy object but got %T", obj)
	}
	logf.FromContext(ctx).Info("Validation for HTTPProxy upon creation", "name", httpProxy.GetName())

	// TODO(jreese) only validate HTTPProxys attached to gateways with gateway classes
	// that this operator manages.
	//
	// This introduces an interesting problem, in that an HTTPProxy can and should
	// be something that can be created in the cluster before a Gateway is created.
	//
	// For now, validate any HTTPProxy based on this operator's validation rules.

	if errs := validation.ValidateHTTPProxy(httpProxy); len(errs) > 0 {
		return nil, errors.NewInvalid(obj.GetObjectKind().GroupVersionKind().GroupKind(), httpProxy.GetName(), errs)
	}

	// TODO(user): fill in your validation logic upon object creation.

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type HTTPProxy.
func (v *HTTPProxyCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	httpProxy, ok := newObj.(*networkingv1alpha.HTTPProxy)
	if !ok {
		return nil, fmt.Errorf("expected a HTTPProxy object for the newObj but got %T", newObj)
	}
	logf.FromContext(ctx).Info("Validation for HTTPProxy upon update", "name", httpProxy.GetName())

	if errs := validation.ValidateHTTPProxy(httpProxy); len(errs) > 0 {
		return nil, errors.NewInvalid(oldObj.GetObjectKind().GroupVersionKind().GroupKind(), httpProxy.GetName(), errs)
	}

	// TODO(user): fill in your validation logic upon object update.

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type HTTPProxy.
func (v *HTTPProxyCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	httpProxy, ok := obj.(*networkingv1alpha.HTTPProxy)
	if !ok {
		return nil, fmt.Errorf("expected a HTTPProxy object but got %T", obj)
	}
	logf.FromContext(ctx).Info("Validation for HTTPProxy upon deletion", "name", httpProxy.GetName())

	// TODO(user): fill in your validation logic upon object deletion.

	return nil, nil
}
