// SPDX-License-Identifier: AGPL-3.0-only

package v1

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	gatewaynetworkingk8siov1 "sigs.k8s.io/gateway-api/apis/v1"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"go.datum.net/network-services-operator/internal/validation"
)

// nolint:unused

// SetupHTTPRouteWebhookWithManager registers the webhook for HTTPRoute in the manager.
func SetupHTTPRouteWebhookWithManager(mgr mcmanager.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager()).For(&gatewaynetworkingk8siov1.HTTPRoute{}).
		WithValidator(&HTTPRouteCustomValidator{mgr: mgr}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-gateway-networking-k8s-io-v1-httproute,mutating=false,failurePolicy=fail,sideEffects=None,groups=gateway.networking.k8s.io,resources=httproutes,verbs=create;update,versions=v1,name=vhttproute-v1.kb.io,admissionReviewVersions=v1

type HTTPRouteCustomValidator struct {
	mgr mcmanager.Manager
}

var _ webhook.CustomValidator = &HTTPRouteCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type HTTPRoute.
func (v *HTTPRouteCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	httproute, ok := obj.(*gatewaynetworkingk8siov1.HTTPRoute)
	if !ok {
		return nil, fmt.Errorf("expected a HTTPRoute object but got %T", obj)
	}
	logf.FromContext(ctx).Info("Validation for HTTPRoute upon creation", "name", httproute.GetName())

	// TODO(jreese) only validate httproutes attached to gateways with gateway classes
	// that this operator manages.
	//
	// This introduces an interesting problem, in that an HTTPRoute can and should
	// be something that can be created in the cluster before a Gateway is created.
	//
	// For now, validate any HTTPRoute based on this operator's validation rules.

	if errs := validation.ValidateHTTPRoute(httproute); len(errs) > 0 {
		return nil, errors.NewInvalid(obj.GetObjectKind().GroupVersionKind().GroupKind(), httproute.GetName(), errs)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type HTTPRoute.
func (v *HTTPRouteCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	httproute, ok := newObj.(*gatewaynetworkingk8siov1.HTTPRoute)
	if !ok {
		return nil, fmt.Errorf("expected a HTTPRoute object for the newObj but got %T", newObj)
	}
	logf.FromContext(ctx).Info("Validation for HTTPRoute upon update", "name", httproute.GetName())

	if errs := validation.ValidateHTTPRoute(httproute); len(errs) > 0 {
		return nil, errors.NewInvalid(oldObj.GetObjectKind().GroupVersionKind().GroupKind(), httproute.GetName(), errs)
	}

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type HTTPRoute.
func (v *HTTPRouteCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	httproute, ok := obj.(*gatewaynetworkingk8siov1.HTTPRoute)
	if !ok {
		return nil, fmt.Errorf("expected a HTTPRoute object but got %T", obj)
	}
	logf.FromContext(ctx).Info("Validation for HTTPRoute upon deletion", "name", httproute.GetName())

	// TODO(user): fill in your validation logic upon object deletion.

	return nil, nil
}
