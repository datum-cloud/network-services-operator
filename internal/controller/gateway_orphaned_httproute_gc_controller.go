// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
	mcsource "sigs.k8s.io/multicluster-runtime/pkg/source"

	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
)

// orphanMinAge is the minimum time a downstream HTTPRoute must exist without a
// reachable upstream Gateway before it is considered orphaned and eligible for
// deletion.
const orphanMinAge = 5 * time.Minute

// OrphanedDownstreamHTTPRouteGCReconciler watches downstream HTTPRoutes on the
// Karmada cluster and deletes any that reference a Gateway which no longer
// exists on the upstream project cluster. This catches orphans left behind by
// failed or raced gateway finalization (e.g. the pre-v0.23.6 bug where
// detachHTTPRoutes checked Status.Parents instead of Spec.ParentRefs).
//
// Reconcile requests identify the upstream HTTPRoute that the downstream route
// was created from: ClusterName=upstream cluster, NamespacedName=upstream
// HTTPRoute namespace/name. This key is derived from the labels NSO stamps on
// every downstream HTTPRoute via SetControllerReference.
type OrphanedDownstreamHTTPRouteGCReconciler struct {
	mgr               mcmanager.Manager
	DownstreamCluster cluster.Cluster

	// minOrphanAge overrides the orphanMinAge constant. Zero means use the
	// constant. Exposed for unit tests.
	minOrphanAge time.Duration
}

func (r *OrphanedDownstreamHTTPRouteGCReconciler) effectiveMinAge() time.Duration {
	if r.minOrphanAge > 0 {
		return r.minOrphanAge
	}
	return orphanMinAge
}

// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=list;watch;delete

func (r *OrphanedDownstreamHTTPRouteGCReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues(
		"cluster", req.ClusterName,
		"namespace", req.Namespace,
		jsonKeyName, req.Name,
	)

	upstreamCluster, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Find all downstream HTTPRoutes that were projected from this upstream
	// HTTPRoute. There may be multiple if NSO wrote the route into more than
	// one downstream namespace, though in practice there is one per upstream.
	labelValue := fmt.Sprintf("cluster-%s", strings.ReplaceAll(string(req.ClusterName), "/", "_"))

	var routes gatewayv1.HTTPRouteList
	if err := r.DownstreamCluster.GetClient().List(ctx, &routes,
		client.MatchingLabels{
			downstreamclient.UpstreamOwnerClusterNameLabel: labelValue,
			downstreamclient.UpstreamOwnerNamespaceLabel:   req.Namespace,
			downstreamclient.UpstreamOwnerNameLabel:        req.Name,
		},
	); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing downstream HTTPRoutes: %w", err)
	}

	for i := range routes.Items {
		route := &routes.Items[i]

		if !route.DeletionTimestamp.IsZero() {
			continue
		}

		age := time.Since(route.CreationTimestamp.Time)
		if age < r.effectiveMinAge() {
			logger.Info("downstream HTTPRoute too young; requeueing",
				"route", route.Name, "age", age.Round(time.Second))
			return ctrl.Result{RequeueAfter: r.effectiveMinAge() - age}, nil
		}

		// Check whether any upstream Gateway referenced by this route still exists.
		// If at least one live parent Gateway is found the route is healthy.
		hasLiveParent := false
		for _, ref := range route.Spec.ParentRefs {
			if ptr.Deref(ref.Group, gatewayv1.GroupName) != gatewayv1.GroupName ||
				ptr.Deref(ref.Kind, KindGateway) != KindGateway {
				continue
			}

			key := types.NamespacedName{
				Namespace: req.Namespace,
				Name:      string(ref.Name),
			}
			gw := &gatewayv1.Gateway{}
			if err := upstreamCluster.GetClient().Get(ctx, key, gw); err != nil {
				if !apierrors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("checking upstream Gateway %s: %w", key, err)
				}
				// NotFound: this parent is gone, keep checking others.
				continue
			}
			hasLiveParent = true
			break
		}

		if hasLiveParent {
			continue
		}

		logger.Info("deleting orphaned downstream HTTPRoute: no live upstream Gateway",
			"route", route.Name, "namespace", route.Namespace)

		if err := r.DownstreamCluster.GetClient().Delete(ctx, route); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("deleting orphaned downstream HTTPRoute %s/%s: %w",
				route.Namespace, route.Name, err)
		}
	}

	return ctrl.Result{}, nil
}

func (r *OrphanedDownstreamHTTPRouteGCReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	downstreamHTTPRouteSrc := mcsource.Kind(
		&gatewayv1.HTTPRoute{},
		r.enqueueFromDownstreamHTTPRoute,
	)
	downstreamSrc, _, err := downstreamHTTPRouteSrc.ForCluster("", r.DownstreamCluster)
	if err != nil {
		return fmt.Errorf("building downstream HTTPRoute watch source: %w", err)
	}

	return mcbuilder.ControllerManagedBy(mgr).
		// Watch upstream HTTPRoutes so we reconcile when they are synced or deleted.
		// On deletion the reconcile will find no live Gateway and clean up any
		// surviving downstream HTTPRoute.
		Watches(
			&gatewayv1.HTTPRoute{},
			mchandler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []ctrl.Request {
				return []ctrl.Request{{NamespacedName: client.ObjectKeyFromObject(obj)}}
			}),
		).
		// Watch downstream HTTPRoutes so we reconcile when a new one appears on
		// Karmada — this covers existing orphans present at startup.
		WatchesRawSource(downstreamSrc).
		Named("orphaned_downstream_httproute_gc").
		Complete(r)
}

// enqueueFromDownstreamHTTPRoute maps a downstream HTTPRoute event to the
// reconcile request that identifies its upstream HTTPRoute.
func (r *OrphanedDownstreamHTTPRouteGCReconciler) enqueueFromDownstreamHTTPRoute(
	_ multicluster.ClusterName,
	_ cluster.Cluster,
) handler.TypedEventHandler[*gatewayv1.HTTPRoute, mcreconcile.Request] {
	return handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, route *gatewayv1.HTTPRoute) []mcreconcile.Request {
		if !route.DeletionTimestamp.IsZero() {
			return nil
		}

		labels := route.GetLabels()
		clusterLabel := labels[downstreamclient.UpstreamOwnerClusterNameLabel]
		upstreamNamespace := labels[downstreamclient.UpstreamOwnerNamespaceLabel]
		upstreamName := labels[downstreamclient.UpstreamOwnerNameLabel]

		if clusterLabel == "" || upstreamNamespace == "" || upstreamName == "" {
			return nil
		}

		return []mcreconcile.Request{{
			ClusterName: multicluster.ClusterName(downstreamclient.UpstreamClusterNameFromLabel(clusterLabel)),
			Request: ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: upstreamNamespace,
					Name:      upstreamName,
				},
			},
		}}
	})
}
