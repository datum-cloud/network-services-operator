// SPDX-License-Identifier: AGPL-3.0-only

package v1

import (
	"context"

	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	gatewaynetworkingk8siov1 "sigs.k8s.io/gateway-api/apis/v1"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/validation"
)

// nolint:unused

// SetupHTTPRouteWebhookWithManager registers the webhook for HTTPRoute in the manager.
func SetupHTTPRouteWebhookWithManager(mgr mcmanager.Manager, cfg config.NetworkServicesOperator) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager(), &gatewaynetworkingk8siov1.HTTPRoute{}).
		WithValidator(&HTTPRouteCustomValidator{mgr: mgr, validationOpts: cfg.Gateway.HTTPRoutes}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-gateway-networking-k8s-io-v1-httproute,mutating=false,failurePolicy=fail,sideEffects=None,groups=gateway.networking.k8s.io,resources=httproutes,verbs=create;update,versions=v1,name=vhttproute-v1.kb.io,admissionReviewVersions=v1

type HTTPRouteCustomValidator struct {
	mgr            mcmanager.Manager
	validationOpts config.HTTPRouteValidationOptions
}

var _ admission.Validator[*gatewaynetworkingk8siov1.HTTPRoute] = &HTTPRouteCustomValidator{}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type HTTPRoute.
func (v *HTTPRouteCustomValidator) ValidateCreate(ctx context.Context, httproute *gatewaynetworkingk8siov1.HTTPRoute) (admission.Warnings, error) {
	logf.FromContext(ctx).Info("Validation for HTTPRoute upon creation", "name", httproute.GetName())

	// TODO(jreese) only validate httproutes attached to gateways with gateway classes
	// that this operator manages.
	//
	// This introduces an interesting problem, in that an HTTPRoute can and should
	// be something that can be created in the cluster before a Gateway is created.
	//
	// For now, validate any HTTPRoute based on this operator's validation rules.

	if errs := validation.ValidateHTTPRoute(httproute, v.validationOpts); len(errs) > 0 {
		return nil, errors.NewInvalid(httproute.GetObjectKind().GroupVersionKind().GroupKind(), httproute.GetName(), errs)
	}

	return nil, nil
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type HTTPRoute.
func (v *HTTPRouteCustomValidator) ValidateUpdate(ctx context.Context, oldHTTPRoute, newHTTPRoute *gatewaynetworkingk8siov1.HTTPRoute) (admission.Warnings, error) {
	logf.FromContext(ctx).Info("Validation for HTTPRoute upon update", "name", newHTTPRoute.GetName())

	if errs := validation.ValidateHTTPRoute(newHTTPRoute, v.validationOpts); len(errs) > 0 {
		return nil, errors.NewInvalid(oldHTTPRoute.GetObjectKind().GroupVersionKind().GroupKind(), newHTTPRoute.GetName(), errs)
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type HTTPRoute.
func (v *HTTPRouteCustomValidator) ValidateDelete(ctx context.Context, httproute *gatewaynetworkingk8siov1.HTTPRoute) (admission.Warnings, error) {
	logf.FromContext(ctx).Info("Validation for HTTPRoute upon deletion", "name", httproute.GetName())

	// TODO(user): fill in your validation logic upon object deletion.

	return nil, nil
}
