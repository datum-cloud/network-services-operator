// SPDX-License-Identifier: AGPL-3.0-only

package v1

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"go.datum.net/network-services-operator/internal/config"
	gatewayutil "go.datum.net/network-services-operator/internal/util/gateway"
	"go.datum.net/network-services-operator/internal/validation"
)

// nolint:unused

// SetupGatewayWebhookWithManager registers the webhook for Gateway in the manager.
func SetupGatewayWebhookWithManager(mgr mcmanager.Manager, config config.NetworkServicesOperator) error {
	validationOpts := validation.GatewayValidationOptions{
		ControllerName:             config.Gateway.ControllerName,
		PermittedTLSOptions:        config.Gateway.PermittedTLSOptions,
		ValidPortNumbers:           config.Gateway.ValidPortNumbers,
		ValidProtocolTypes:         config.Gateway.ValidProtocolTypes,
		GatewayDNSAddressFunc:      config.Gateway.GatewayDNSAddress,
		SkipHostnameFQDNValidation: config.Gateway.DisableHostnameVerification,
	}

	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager()).For(&gatewayv1.Gateway{}).
		WithValidator(&GatewayCustomValidator{mgr: mgr, validationOpts: validationOpts}).
		WithDefaulter(&GatewayCustomDefaulter{mgr: mgr, config: config}).
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

	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("expected a cluster name in the context")
	}

	cluster, err := v.mgr.GetCluster(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	clusterClient := cluster.GetClient()

	if shouldProcess, err := shouldProcess(ctx, clusterClient, v.validationOpts.ControllerName, gateway); !shouldProcess || err != nil {
		return nil, err
	}

	gatewaylog := logf.FromContext(ctx).WithValues("cluster", clusterName)
	gatewaylog.Info("Validating Gateway", "name", gateway.GetName(), "cluster", clusterName)

	clusterValidationOpts := v.validationOpts
	clusterValidationOpts.ClusterName = clusterName

	if errs := validation.ValidateGateway(gateway, clusterValidationOpts); len(errs) > 0 {
		return nil, apierrors.NewInvalid(obj.GetObjectKind().GroupVersionKind().GroupKind(), gateway.GetName(), errs)
	}

	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type Gateway.
func (v *GatewayCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	gateway, ok := newObj.(*gatewayv1.Gateway)
	if !ok {
		return nil, fmt.Errorf("expected a Gateway object for the newObj but got %T", newObj)
	}

	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("expected a cluster name in the context")
	}

	cluster, err := v.mgr.GetCluster(ctx, clusterName)
	if err != nil {
		return nil, err
	}
	clusterClient := cluster.GetClient()

	if shouldProcess, err := shouldProcess(ctx, clusterClient, v.validationOpts.ControllerName, gateway); !shouldProcess || err != nil {
		return nil, err
	}

	gatewaylog := logf.FromContext(ctx).WithValues("cluster", clusterName)
	gatewaylog.Info("Validating Gateway", "name", gateway.GetName())

	clusterValidationOpts := v.validationOpts
	clusterValidationOpts.ClusterName = clusterName

	if errs := validation.ValidateGateway(gateway, clusterValidationOpts); len(errs) > 0 {
		return nil, apierrors.NewInvalid(oldObj.GetObjectKind().GroupVersionKind().GroupKind(), gateway.GetName(), errs)
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

	return nil, nil
}

// +kubebuilder:webhook:path=/mutate-gateway-networking-k8s-io-v1-gateway,mutating=true,failurePolicy=fail,sideEffects=None,groups=gateway.networking.k8s.io,resources=gateways,verbs=create;update,versions=v1,name=mgateway-v1.kb.io,admissionReviewVersions=v1

type GatewayCustomDefaulter struct {
	mgr    mcmanager.Manager
	config config.NetworkServicesOperator
}

var _ webhook.CustomDefaulter = &GatewayCustomDefaulter{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind Gateway.
func (d *GatewayCustomDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	gateway, ok := obj.(*gatewayv1.Gateway)
	if !ok {
		return fmt.Errorf("expected a Gateway object but got %T", obj)
	}

	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return fmt.Errorf("expected a cluster name in the context")
	}

	cluster, err := d.mgr.GetCluster(ctx, clusterName)
	if err != nil {
		return err
	}
	clusterClient := cluster.GetClient()

	if shouldProcess, err := shouldProcess(ctx, clusterClient, d.config.Gateway.ControllerName, gateway); !shouldProcess || err != nil {
		return err
	}

	gatewaylog := logf.FromContext(ctx).WithValues("cluster", clusterName)
	gatewaylog.Info("Defaulting for Gateway", "name", gateway.GetName())

	// Inject default listeners at time of creation. These will be updated by
	// the controller to have the hostname fields set to a value that includes the
	// UID of the resource, which is not available in a mutating webhook at time
	// of creation.

	if gateway.CreationTimestamp.IsZero() {
		gatewayutil.SetDefaultListeners(gateway, d.config.Gateway)
	}

	return nil
}

func shouldProcess(
	ctx context.Context,
	clusterClient client.Client,
	controllerName gatewayv1.GatewayController,
	gateway *gatewayv1.Gateway,
) (bool, error) {
	var gatewayClass gatewayv1.GatewayClass
	if err := clusterClient.Get(ctx, types.NamespacedName{Name: string(gateway.Spec.GatewayClassName)}, &gatewayClass); err != nil {
		if apierrors.IsNotFound(err) {
			// No error if the GatewayClass is not found, it's not managed by the operator
			logf.FromContext(ctx).Info("GatewayClass is not found, skipping validation", "name", gateway.Spec.GatewayClassName)
			return false, nil
		}
		return false, err
	}

	if gatewayClass.Spec.ControllerName != controllerName {
		// No error if the GatewayClass is not managed by the operator
		logf.FromContext(ctx).Info("GatewayClass is not managed by the operator, skipping validation", "name", gatewayClass.GetName())
		return false, nil
	}

	return true, nil
}
