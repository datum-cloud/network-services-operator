// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"context"
	"encoding/json"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/display"
)

func SetupTrafficProtectionPolicyWebhookWithManager(mgr mcmanager.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr.GetLocalManager(), &networkingv1alpha.TrafficProtectionPolicy{}).
		WithDefaulter(&TrafficProtectionPolicyDefaulter{mgr: mgr}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-networking-datumapis-com-v1alpha-trafficprotectionpolicy,mutating=true,failurePolicy=fail,sideEffects=None,groups=networking.datumapis.com,resources=trafficprotectionpolicies,verbs=create;update,versions=v1alpha,name=mtrafficprotectionpolicy-v1alpha.kb.io,admissionReviewVersions=v1

type TrafficProtectionPolicyDefaulter struct {
	mgr mcmanager.Manager
}

var _ admission.Defaulter[*networkingv1alpha.TrafficProtectionPolicy] = &TrafficProtectionPolicyDefaulter{}

func (d *TrafficProtectionPolicyDefaulter) Default(ctx context.Context, policy *networkingv1alpha.TrafficProtectionPolicy) error {
	displayName := lookupTPPDisplayName(ctx, d.clusterClient(ctx), policy)
	_ = display.EnsureTPPAnnotations(policy, oldTPP(ctx), displayName)
	return nil
}

func (d *TrafficProtectionPolicyDefaulter) clusterClient(ctx context.Context) client.Client {
	if d == nil || d.mgr == nil {
		return nil
	}
	clusterName, ok := mccontext.ClusterFrom(ctx)
	if !ok {
		return d.mgr.GetLocalManager().GetClient()
	}
	cluster, err := d.mgr.GetCluster(ctx, clusterName)
	if err != nil {
		return nil
	}
	return cluster.GetClient()
}

func oldTPP(ctx context.Context) *networkingv1alpha.TrafficProtectionPolicy {
	req, err := admission.RequestFromContext(ctx)
	if err != nil || len(req.OldObject.Raw) == 0 {
		return nil
	}
	var old networkingv1alpha.TrafficProtectionPolicy
	if err := json.Unmarshal(req.OldObject.Raw, &old); err != nil {
		logf.FromContext(ctx).V(1).Info("skipping TPP activity annotations; failed to decode OldObject", "error", err)
		return nil
	}
	return &old
}

func lookupTPPDisplayName(ctx context.Context, cl client.Client, policy *networkingv1alpha.TrafficProtectionPolicy) string {
	if cl == nil || policy == nil || len(policy.Spec.TargetRefs) == 0 {
		return ""
	}

	target := policy.Spec.TargetRefs[0]
	key := types.NamespacedName{Namespace: policy.Namespace, Name: string(target.Name)}

	var proxy *networkingv1alpha.HTTPProxy
	switch target.Kind {
	case "Gateway":
		var gateway gatewayv1.Gateway
		if err := cl.Get(ctx, key, &gateway); err != nil {
			if !apierrors.IsNotFound(err) {
				logf.FromContext(ctx).V(1).Info("looking up Gateway for TPP display name", "error", err)
			}
			proxy = httpProxyByName(ctx, cl, key)
		} else {
			proxy = httpProxyFromOwner(ctx, cl, &gateway)
		}
	case "HTTPRoute":
		var route gatewayv1.HTTPRoute
		if err := cl.Get(ctx, key, &route); err != nil {
			if !apierrors.IsNotFound(err) {
				logf.FromContext(ctx).V(1).Info("looking up HTTPRoute for TPP display name", "error", err)
			}
			proxy = httpProxyByName(ctx, cl, key)
		} else {
			proxy = httpProxyFromOwner(ctx, cl, &route)
		}
	default:
		proxy = httpProxyByName(ctx, cl, key)
	}

	if proxy == nil {
		return ""
	}
	return display.HTTPProxyDisplayName(proxy)
}

func httpProxyFromOwner(ctx context.Context, cl client.Client, obj client.Object) *networkingv1alpha.HTTPProxy {
	if owner := metav1.GetControllerOf(obj); owner != nil && owner.Kind == "HTTPProxy" {
		return httpProxyByName(ctx, cl, types.NamespacedName{Namespace: obj.GetNamespace(), Name: owner.Name})
	}
	return httpProxyByName(ctx, cl, types.NamespacedName{Namespace: obj.GetNamespace(), Name: obj.GetName()})
}

func httpProxyByName(ctx context.Context, cl client.Client, key types.NamespacedName) *networkingv1alpha.HTTPProxy {
	var proxy networkingv1alpha.HTTPProxy
	if err := cl.Get(ctx, key, &proxy); err != nil {
		if !apierrors.IsNotFound(err) {
			logf.FromContext(ctx).V(1).Info("looking up HTTPProxy for TPP display name", "error", err, "name", fmt.Sprintf("%s/%s", key.Namespace, key.Name))
		}
		return nil
	}
	return &proxy
}
