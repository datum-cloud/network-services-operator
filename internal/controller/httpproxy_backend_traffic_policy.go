// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

func (r *HTTPProxyReconciler) applyBackendSettingsNotApplied(
	ctx context.Context,
	upstreamClient client.Client,
	clusterName string,
	route *gatewayv1.HTTPRoute,
	gatewayName string,
	programmedCondition *metav1.Condition,
) error {
	if r.DownstreamCluster == nil || programmedCondition.Status != metav1.ConditionTrue {
		return nil
	}

	downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy(clusterName, upstreamClient, r.DownstreamCluster.GetClient())
	namespace, err := downstreamStrategy.GetDownstreamNamespaceNameForUpstreamNamespace(ctx, route.Namespace)
	if err != nil {
		return fmt.Errorf("failed to get downstream namespace for backend traffic policy: %w", err)
	}

	var policy envoygatewayv1alpha1.BackendTrafficPolicy
	key := types.NamespacedName{Namespace: namespace, Name: routeBackendTrafficPolicyName(route.UID)}
	if err := r.DownstreamCluster.GetClient().Get(ctx, key, &policy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get downstream backend traffic policy: %w", err)
	}

	reason, refused := refusedAncestorReason(policy.Status.Ancestors)
	if !refused {
		return nil
	}

	programmedCondition.Status = metav1.ConditionFalse
	programmedCondition.Reason = networkingv1alpha.HTTPProxyReasonBackendSettingsNotApplied
	if reason == string(gatewayv1.PolicyReasonConflicted) {
		programmedCondition.Message = fmt.Sprintf("The HTTPProxy's health check and load balancing settings are not "+
			"applied because another BackendTrafficPolicy is attached to its route. Attach that policy to the "+
			"Gateway %q instead.", gatewayName)
		return nil
	}
	programmedCondition.Message = fmt.Sprintf("The HTTPProxy's health check and load balancing settings are not "+
		"applied: the edge did not accept them (%s).", reason)
	return nil
}

func refusedAncestorReason(ancestors []gatewayv1.PolicyAncestorStatus) (string, bool) {
	reason, refused := "", false
	for _, ancestor := range ancestors {
		c := apimeta.FindStatusCondition(ancestor.Conditions, string(gatewayv1.PolicyConditionAccepted))
		if c == nil || c.Status != metav1.ConditionFalse {
			continue
		}
		if c.Reason == string(gatewayv1.PolicyReasonConflicted) {
			return c.Reason, true
		}
		reason, refused = c.Reason, true
	}
	return reason, refused
}

func (r *HTTPProxyReconciler) enqueueHTTPProxyForDownstreamBackendTrafficPolicy() func(multicluster.ClusterName, cluster.Cluster) handler.TypedEventHandler[*envoygatewayv1alpha1.BackendTrafficPolicy, mcreconcile.Request] {
	return func(multicluster.ClusterName, cluster.Cluster) handler.TypedEventHandler[*envoygatewayv1alpha1.BackendTrafficPolicy, mcreconcile.Request] {
		return handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, policy *envoygatewayv1alpha1.BackendTrafficPolicy) []mcreconcile.Request {
			labels := policy.GetLabels()
			if labels[downstreamclient.UpstreamOwnerGroupLabel] != gatewayv1.GroupName || labels[downstreamclient.UpstreamOwnerKindLabel] != KindHTTPRoute {
				return nil
			}
			namespace := labels[downstreamclient.UpstreamOwnerNamespaceLabel]
			name := labels[downstreamclient.UpstreamOwnerNameLabel]
			upstreamCluster := labels[downstreamclient.UpstreamOwnerClusterNameLabel]
			if namespace == "" || name == "" || upstreamCluster == "" {
				return nil
			}
			return []mcreconcile.Request{{
				ClusterName: multicluster.ClusterName(downstreamclient.UpstreamClusterNameFromLabel(upstreamCluster)),
				Request:     ctrl.Request{NamespacedName: types.NamespacedName{Namespace: namespace, Name: name}},
			}}
		})
	}
}
