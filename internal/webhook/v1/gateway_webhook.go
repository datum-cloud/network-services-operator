// SPDX-License-Identifier: AGPL-3.0-only

package v1

import (
	"context"
	"fmt"

	"go.datum.net/network-services-operator/internal/validation"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	gatewaynetworkingk8siov1 "sigs.k8s.io/gateway-api/apis/v1"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
)

// nolint:unused
// log is for logging in this package.
var gatewaylog = logf.Log.WithName("gateway-resource")

// SetupGatewayWebhookWithManager registers the webhook for Gateway in the manager.
func SetupGatewayWebhookWithManager(mgr mcmanager.Manager, validationOpts validation.GatewayValidationOptions) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager()).For(&gatewaynetworkingk8siov1.Gateway{}).
		WithValidator(&GatewayCustomValidator{mgr: mgr, validationOpts: validationOpts}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-gateway-networking-k8s-io-v1-gateway,mutating=false,failurePolicy=fail,sideEffects=None,groups=gateway.networking.k8s.io,resources=gateways,verbs=create;update,versions=v1,name=vgateway-v1.kb.io,admissionReviewVersions=v1

type GatewayCustomValidator struct {
	mgr            mcmanager.Manager
	validationOpts validation.GatewayValidationOptions
}

var _ webhook.CustomValidator = &GatewayCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type Gateway.
func (v *GatewayCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	gateway, ok := obj.(*gatewaynetworkingk8siov1.Gateway)
	if !ok {
		return nil, fmt.Errorf("expected a Gateway object but got %T", obj)
	}

	// TODO(jreese) only validate if the GatewayClass is one that the operator manages

	gatewaylog.Info("Validating Gateway", "name", gateway.GetName())

	if errs := validation.ValidateGateway(gateway, v.validationOpts); len(errs) > 0 {
		return nil, errors.NewInvalid(obj.GetObjectKind().GroupVersionKind().GroupKind(), gateway.GetName(), errs)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type Gateway.
func (v *GatewayCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	gateway, ok := newObj.(*gatewaynetworkingk8siov1.Gateway)
	if !ok {
		return nil, fmt.Errorf("expected a Gateway object for the newObj but got %T", newObj)
	}

	gatewaylog.Info("Validating Gateway", "name", gateway.GetName())

	if errs := validation.ValidateGateway(gateway, v.validationOpts); len(errs) > 0 {
		return nil, errors.NewInvalid(oldObj.GetObjectKind().GroupVersionKind().GroupKind(), gateway.GetName(), errs)
	}

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type Gateway.
func (v *GatewayCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	gateway, ok := obj.(*gatewaynetworkingk8siov1.Gateway)
	if !ok {
		return nil, fmt.Errorf("expected a Gateway object but got %T", obj)
	}
	gatewaylog.Info("Validation for Gateway upon deletion", "name", gateway.GetName())

	// TODO(user): fill in your validation logic upon object deletion.

	return nil, nil
}
