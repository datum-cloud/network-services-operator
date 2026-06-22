// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"go.datum.net/network-services-operator/internal/validation"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// nolint:unused

// SetupHTTPProxyWebhookWithManager registers the webhook for HTTPProxy in the manager.
func SetupHTTPProxyWebhookWithManager(mgr mcmanager.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager(), &networkingv1alpha.HTTPProxy{}).
		WithValidator(&HTTPProxyCustomValidator{mgr: mgr}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-networking-datumapis-com-v1alpha-httpproxy,mutating=false,failurePolicy=fail,sideEffects=None,groups=networking.datumapis.com,resources=httpproxies,verbs=create;update,versions=v1alpha,name=vhttpproxy-v1alpha.kb.io,admissionReviewVersions=v1

type HTTPProxyCustomValidator struct {
	mgr mcmanager.Manager
}

var _ admission.Validator[*networkingv1alpha.HTTPProxy] = &HTTPProxyCustomValidator{}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type HTTPProxy.
func (v *HTTPProxyCustomValidator) ValidateCreate(ctx context.Context, httpProxy *networkingv1alpha.HTTPProxy) (admission.Warnings, error) {
	logf.FromContext(ctx).Info("Validation for HTTPProxy upon creation", "name", httpProxy.GetName())

	// TODO(jreese) only validate HTTPProxys attached to gateways with gateway classes
	// that this operator manages.
	//
	// This introduces an interesting problem, in that an HTTPProxy can and should
	// be something that can be created in the cluster before a Gateway is created.
	//
	// For now, validate any HTTPProxy based on this operator's validation rules.

	if errs := validation.ValidateHTTPProxy(httpProxy); len(errs) > 0 {
		return nil, errors.NewInvalid(httpProxy.GetObjectKind().GroupVersionKind().GroupKind(), httpProxy.GetName(), errs)
	}

	// TODO(user): fill in your validation logic upon object creation.

	return nil, nil
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type HTTPProxy.
func (v *HTTPProxyCustomValidator) ValidateUpdate(ctx context.Context, oldHTTPProxy, newHTTPProxy *networkingv1alpha.HTTPProxy) (admission.Warnings, error) {
	logf.FromContext(ctx).Info("Validation for HTTPProxy upon update", "name", newHTTPProxy.GetName())

	if errs := validation.ValidateHTTPProxy(newHTTPProxy); len(errs) > 0 {
		return nil, errors.NewInvalid(oldHTTPProxy.GetObjectKind().GroupVersionKind().GroupKind(), newHTTPProxy.GetName(), errs)
	}

	// TODO(user): fill in your validation logic upon object update.

	return nil, nil
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type HTTPProxy.
func (v *HTTPProxyCustomValidator) ValidateDelete(ctx context.Context, httpProxy *networkingv1alpha.HTTPProxy) (admission.Warnings, error) {
	logf.FromContext(ctx).Info("Validation for HTTPProxy upon deletion", "name", httpProxy.GetName())

	// TODO(user): fill in your validation logic upon object deletion.

	return nil, nil
}
