// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	"context"
	"fmt"

	gatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/downstreamclient"
	"go.datum.net/network-services-operator/internal/validation"
	webhookutil "go.datum.net/network-services-operator/internal/webhook"
)

// SetupBackendTrafficPolicyWebhookWithManager registers the webhook for BackendTrafficPolicy in the manager.
func SetupBackendTrafficPolicyWebhookWithManager(mgr mcmanager.Manager, cfg config.NetworkServicesOperator) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager(), &gatewayv1alpha1.BackendTrafficPolicy{}).
		WithValidator(&BackendTrafficPolicyCustomValidator{mgr: mgr, validationOpts: cfg.Gateway.ExtensionAPIValidationOptions.BackendTrafficPolicies}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-gateway-envoyproxy-io-v1alpha1-backendtrafficpolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=gateway.envoyproxy.io,resources=backendtrafficpolicies,verbs=create;update,versions=v1alpha1,name=vbackendtrafficpolicy-v1alpha1.kb.io,admissionReviewVersions=v1

type BackendTrafficPolicyCustomValidator struct {
	mgr            mcmanager.Manager
	validationOpts config.BackendTrafficPolicyValidationOptions
}

var _ admission.Validator[*gatewayv1alpha1.BackendTrafficPolicy] = &BackendTrafficPolicyCustomValidator{}

// ValidateCreate implements admission.Validator so a webhook will be registered for the type BackendTrafficPolicy.
func (v *BackendTrafficPolicyCustomValidator) ValidateCreate(ctx context.Context, backendTrafficPolicy *gatewayv1alpha1.BackendTrafficPolicy) (admission.Warnings, error) {
	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("expected a cluster name in the context")
	}

	log := logf.FromContext(ctx).WithValues("cluster", clusterName)
	log.Info("Validating BackendTrafficPolicy", "name", backendTrafficPolicy.GetName(), "cluster", clusterName)

	if errs := validation.ValidateBackendTrafficPolicy(backendTrafficPolicy, v.validationOpts); len(errs) > 0 {
		return nil, apierrors.NewInvalid(backendTrafficPolicy.GetObjectKind().GroupVersionKind().GroupKind(), backendTrafficPolicy.GetName(), errs)
	}

	return nil, v.refuseGeneratedRouteTargets(ctx, clusterName, backendTrafficPolicy)
}

// ValidateUpdate implements admission.Validator so a webhook will be registered for the type BackendTrafficPolicy.
func (v *BackendTrafficPolicyCustomValidator) ValidateUpdate(ctx context.Context, oldBackendTrafficPolicy, newBackendTrafficPolicy *gatewayv1alpha1.BackendTrafficPolicy) (admission.Warnings, error) {
	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return nil, fmt.Errorf("expected a cluster name in the context")
	}

	log := logf.FromContext(ctx).WithValues("cluster", clusterName)
	log.Info("Validating BackendTrafficPolicy", "name", newBackendTrafficPolicy.GetName(), "cluster", clusterName)

	if webhookutil.SkipUpdateValidation(newBackendTrafficPolicy, oldBackendTrafficPolicy.Spec, newBackendTrafficPolicy.Spec) {
		return nil, nil
	}

	if errs := validation.ValidateBackendTrafficPolicy(newBackendTrafficPolicy, v.validationOpts); len(errs) > 0 {
		return nil, apierrors.NewInvalid(oldBackendTrafficPolicy.GetObjectKind().GroupVersionKind().GroupKind(), newBackendTrafficPolicy.GetName(), errs)
	}
	return nil, v.refuseGeneratedRouteTargets(ctx, clusterName, newBackendTrafficPolicy)
}

// ValidateDelete implements admission.Validator so a webhook will be registered for the type BackendTrafficPolicy.
func (v *BackendTrafficPolicyCustomValidator) ValidateDelete(ctx context.Context, backendTrafficPolicy *gatewayv1alpha1.BackendTrafficPolicy) (admission.Warnings, error) {
	return nil, nil
}

func (v *BackendTrafficPolicyCustomValidator) refuseGeneratedRouteTargets(
	ctx context.Context,
	clusterName multicluster.ClusterName,
	policy *gatewayv1alpha1.BackendTrafficPolicy,
) error {
	if !targetsHTTPRoutes(policy) {
		return nil
	}

	cluster, err := v.mgr.GetCluster(ctx, clusterName)
	if err != nil {
		return err
	}

	generated, err := generatedRouteNames(ctx, cluster.GetClient(), policy.Namespace)
	if err != nil {
		return err
	}

	specPath := field.NewPath("spec")
	var errs field.ErrorList
	for i, ref := range policy.Spec.TargetRefs {
		if isHTTPRoute(ref.Group, ref.Kind) && generated.Has(string(ref.Name)) {
			errs = append(errs, field.Forbidden(specPath.Child("targetRefs").Index(i), generatedRouteMessage(string(ref.Name))))
		}
	}

	for i, selector := range policy.Spec.TargetSelectors {
		if !isHTTPRoute(ptr.Deref(selector.Group, gatewayv1.GroupName), selector.Kind) {
			continue
		}
		selectorPath := specPath.Child("targetSelectors").Index(i)
		matcher, err := metav1.LabelSelectorAsSelector(withoutClusterLabel(selector))
		if err != nil {
			errs = append(errs, field.Invalid(selectorPath, selector, err.Error()))
			continue
		}
		// here be dragons
		for _, name := range sets.List(generated) {
			if matcher.Matches(downstreamRouteLabels(policy.Namespace, name)) {
				errs = append(errs, field.Forbidden(selectorPath, generatedRouteMessage(name)))
				break
			}
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return apierrors.NewInvalid(schema.GroupKind{Group: gatewayv1alpha1.GroupName, Kind: gatewayv1alpha1.KindBackendTrafficPolicy}, policy.Name, errs)
}

func targetsHTTPRoutes(policy *gatewayv1alpha1.BackendTrafficPolicy) bool {
	for _, ref := range policy.Spec.TargetRefs {
		if isHTTPRoute(ref.Group, ref.Kind) {
			return true
		}
	}
	for _, selector := range policy.Spec.TargetSelectors {
		if isHTTPRoute(ptr.Deref(selector.Group, gatewayv1.GroupName), selector.Kind) {
			return true
		}
	}
	return false
}

func isHTTPRoute(group gatewayv1.Group, kind gatewayv1.Kind) bool {
	return group == gatewayv1.GroupName && kind == "HTTPRoute"
}

func generatedRouteNames(ctx context.Context, cl client.Client, namespace string) (sets.Set[string], error) {
	var proxies networkingv1alpha.HTTPProxyList
	if err := cl.List(ctx, &proxies, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed listing httpproxies: %w", err)
	}
	var routes gatewayv1.HTTPRouteList
	if err := cl.List(ctx, &routes, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed listing httproutes: %w", err)
	}

	names := sets.New[string]()
	for _, proxy := range proxies.Items {
		names.Insert(proxy.Name)
	}
	for _, route := range routes.Items {
		if ownedByHTTPProxy(&route) {
			names.Insert(route.Name)
		} else {
			names.Delete(route.Name)
		}
	}
	return names, nil
}

func ownedByHTTPProxy(obj metav1.Object) bool {
	owner := metav1.GetControllerOf(obj)
	if owner == nil || owner.Kind != "HTTPProxy" {
		return false
	}
	gv, err := schema.ParseGroupVersion(owner.APIVersion)
	return err == nil && gv.Group == networkingv1alpha.GroupVersion.Group
}

func withoutClusterLabel(selector gatewayv1alpha1.TargetSelector) *metav1.LabelSelector {
	out := &metav1.LabelSelector{MatchLabels: map[string]string{}}
	for k, v := range selector.MatchLabels {
		if k != downstreamclient.UpstreamOwnerClusterNameLabel {
			out.MatchLabels[k] = v
		}
	}
	for _, expression := range selector.MatchExpressions {
		if expression.Key != downstreamclient.UpstreamOwnerClusterNameLabel {
			out.MatchExpressions = append(out.MatchExpressions, expression)
		}
	}
	return out
}

func downstreamRouteLabels(namespace, name string) labels.Set {
	return labels.Set{
		downstreamclient.UpstreamOwnerGroupLabel:     gatewayv1.GroupName,
		downstreamclient.UpstreamOwnerKindLabel:      "HTTPRoute",
		downstreamclient.UpstreamOwnerNameLabel:      name,
		downstreamclient.UpstreamOwnerNamespaceLabel: namespace,
	}
}

func generatedRouteMessage(name string) string {
	return fmt.Sprintf("HTTPRoute %q belongs to the load balancer %q, which keeps its health check and load "+
		"balancing settings in a policy on that route, and a route takes only one BackendTrafficPolicy. "+
		"To change backend traffic settings for this load balancer, target the Gateway %q instead", name, name, name)
}
