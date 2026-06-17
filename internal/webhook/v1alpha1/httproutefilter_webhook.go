// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	"context"
	"fmt"

	gatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/validation"
)

// SetupHTTPRouteFilterWebhookWithManager registers the webhook for HTTPRouteFilter in the manager.
func SetupHTTPRouteFilterWebhookWithManager(mgr mcmanager.Manager, cfg config.NetworkServicesOperator) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager(), &gatewayv1alpha1.HTTPRouteFilter{}).
		WithValidator(&HTTPRouteFilterCustomValidator{mgr: mgr, validationOpts: cfg.Gateway.ExtensionAPIValidationOptions.HTTPRouteFilters}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-gateway-envoyproxy-io-v1alpha1-httproutefilter,mutating=false,failurePolicy=fail,sideEffects=None,groups=gateway.envoyproxy.io,resources=httproutefilters,verbs=create;update,versions=v1alpha1,name=vhttproutefilter-v1alpha1.kb.io,admissionReviewVersions=v1

type HTTPRouteFilterCustomValidator struct {
	mgr            mcmanager.Manager
	validationOpts config.HTTPRouteFilterValidationOptions
}

var _ admission.Validator[*gatewayv1alpha1.HTTPRouteFilter] = &HTTPRouteFilterCustomValidator{}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type HTTPRouteFilter.
func (v *HTTPRouteFilterCustomValidator) ValidateCreate(ctx context.Context, httpRouteFilter *gatewayv1alpha1.HTTPRouteFilter) (admission.Warnings, error) {
	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("expected a cluster name in the context")
	}

	log := logf.FromContext(ctx).WithValues("cluster", clusterName)
	log.Info("Validating HTTPRouteFilter", "name", httpRouteFilter.GetName(), "cluster", clusterName)

	if errs := validation.ValidateHTTPRouteFilter(httpRouteFilter, v.validationOpts); len(errs) > 0 {
		return nil, apierrors.NewInvalid(httpRouteFilter.GetObjectKind().GroupVersionKind().GroupKind(), httpRouteFilter.GetName(), errs)
	}

	return nil, nil
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type HTTPRouteFilter.
func (v *HTTPRouteFilterCustomValidator) ValidateUpdate(ctx context.Context, oldHTTPRouteFilter, newHTTPRouteFilter *gatewayv1alpha1.HTTPRouteFilter) (admission.Warnings, error) {
	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("expected a cluster name in the context")
	}

	log := logf.FromContext(ctx).WithValues("cluster", clusterName)
	log.Info("Validating HTTPRouteFilter", "name", newHTTPRouteFilter.GetName(), "cluster", clusterName)

	if errs := validation.ValidateHTTPRouteFilter(newHTTPRouteFilter, v.validationOpts); len(errs) > 0 {
		return nil, apierrors.NewInvalid(oldHTTPRouteFilter.GetObjectKind().GroupVersionKind().GroupKind(), newHTTPRouteFilter.GetName(), errs)
	}

	return nil, nil
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type HTTPRouteFilter.
func (v *HTTPRouteFilterCustomValidator) ValidateDelete(ctx context.Context, httpRouteFilter *gatewayv1alpha1.HTTPRouteFilter) (admission.Warnings, error) {
	return nil, nil
}
