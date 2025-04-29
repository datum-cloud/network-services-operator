// SPDX-License-Identifier: AGPL-3.0-only

package v1

import (
	"context"
	"fmt"

	"go.datum.net/network-services-operator/internal/validation"
	networkingwebhook "go.datum.net/network-services-operator/internal/webhook"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
)

// nolint:unused

// SetupGatewayWebhookWithManager registers the webhook for Gateway in the manager.
func SetupGatewayWebhookWithManager(mgr mcmanager.Manager, validationOpts validation.GatewayValidationOptions) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager()).For(&gatewayv1.Gateway{}).
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
	gateway, ok := obj.(*gatewayv1.Gateway)
	if !ok {
		return nil, fmt.Errorf("expected a Gateway object but got %T", obj)
	}

	clusterName := networkingwebhook.ClusterNameFromContext(ctx)

	cluster, err := v.mgr.GetCluster(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	clusterClient := cluster.GetClient()

	if shouldProcess, err := v.shouldProcess(ctx, clusterClient, gateway); !shouldProcess || err != nil {
		return nil, err
	}

	gatewaylog := logf.FromContext(ctx).WithValues("cluster", clusterName)
	gatewaylog.Info("Validating Gateway", "name", gateway.GetName(), "cluster", clusterName)

	if errs := validation.ValidateGateway(gateway, v.validationOpts); len(errs) > 0 {
		return nil, errors.NewInvalid(obj.GetObjectKind().GroupVersionKind().GroupKind(), gateway.GetName(), errs)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type Gateway.
func (v *GatewayCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	gateway, ok := newObj.(*gatewayv1.Gateway)
	if !ok {
		return nil, fmt.Errorf("expected a Gateway object for the newObj but got %T", newObj)
	}

	clusterName := networkingwebhook.ClusterNameFromContext(ctx)

	cluster, err := v.mgr.GetCluster(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	clusterClient := cluster.GetClient()

	if shouldProcess, err := v.shouldProcess(ctx, clusterClient, gateway); !shouldProcess || err != nil {
		return nil, err
	}

	gatewaylog := logf.FromContext(ctx).WithValues("cluster", clusterName)
	gatewaylog.Info("Validating Gateway", "name", gateway.GetName())

	if errs := validation.ValidateGateway(gateway, v.validationOpts); len(errs) > 0 {
		return nil, errors.NewInvalid(oldObj.GetObjectKind().GroupVersionKind().GroupKind(), gateway.GetName(), errs)
	}

	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type Gateway.
func (v *GatewayCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	gateway, ok := obj.(*gatewayv1.Gateway)
	if !ok {
		return nil, fmt.Errorf("expected a Gateway object but got %T", obj)
	}
	gatewaylog := logf.FromContext(ctx)
	gatewaylog.Info("Validation for Gateway upon deletion", "name", gateway.GetName())

	// TODO(user): fill in your validation logic upon object deletion.

	return nil, nil
}

func (v *GatewayCustomValidator) shouldProcess(ctx context.Context, clusterClient client.Client, gateway *gatewayv1.Gateway) (bool, error) {
	var gatewayClass gatewayv1.GatewayClass
	if err := clusterClient.Get(ctx, types.NamespacedName{Name: string(gateway.Spec.GatewayClassName)}, &gatewayClass); err != nil {
		if apierrors.IsNotFound(err) {
			// No error if the GatewayClass is not found, it's not managed by the operator
			logf.FromContext(ctx).Info("GatewayClass is not found, skipping validation", "name", gateway.Spec.GatewayClassName)
			return false, nil
		}
		return false, err
	}

	if gatewayClass.Spec.ControllerName != v.validationOpts.ControllerName {
		// No error if the GatewayClass is not managed by the operator
		logf.FromContext(ctx).Info("GatewayClass is not managed by the operator, skipping validation", "name", gatewayClass.GetName())
		return false, nil
	}

	return true, nil
}
