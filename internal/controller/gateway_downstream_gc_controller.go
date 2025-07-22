package controller

import (
	"context"
	"fmt"

	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"go.datum.net/network-services-operator/internal/config"
	downstreamclient "go.datum.net/network-services-operator/internal/downstreamclient"
)

// GatewayDownstreamGCReconciler reconciles a Gateway object
type GatewayDownstreamGCReconciler struct {
	mgr    mcmanager.Manager
	Config config.NetworkServicesOperator

	DownstreamCluster cluster.Cluster
}

type GVKRequest struct {
	mcreconcile.Request
	GVK schema.GroupVersionKind
}

// Cluster returns the name of the cluster that the request belongs to.
func (r GVKRequest) Cluster() string {
	return r.ClusterName
}

// WithCluster sets the name of the cluster that the request belongs to.
func (r GVKRequest) WithCluster(name string) GVKRequest {
	r.ClusterName = name
	return r
}

func (r *GatewayDownstreamGCReconciler) Reconcile(ctx context.Context, req GVKRequest) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues(
		"gvk",
		req.GVK.String(),
		"cluster",
		req.ClusterName,
		"namespace",
		req.Namespace, "name",
		req.Name,
	)

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	var obj unstructured.Unstructured
	obj.SetGroupVersionKind(req.GVK)
	if err := cl.GetClient().Get(ctx, req.NamespacedName, &obj); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Only process objects that are being deleted and have the gateway finalizer.
	if dt := obj.GetDeletionTimestamp(); dt.IsZero() || !controllerutil.ContainsFinalizer(&obj, gatewayControllerGCFinalizer) {
		return ctrl.Result{}, nil
	}

	logger.Info("garbage collecting downstream resources")

	downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy(req.ClusterName, cl.GetClient(), r.DownstreamCluster.GetClient())

	if err := downstreamStrategy.DeleteAnchorForObject(ctx, &obj); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed deleting anchor: %w", err)
	}

	// When an HTTPRoute is deleted, ensure that the downstream EndpointSlices are
	// deleted as well. They're currently logically owned by the route as a result
	// of duplicating the upstream EndpointSlice.

	if req.GVK.Group == gatewayv1.GroupName && req.GVK.Kind == "HTTPRoute" {
		httpRoute := &gatewayv1.HTTPRoute{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, httpRoute); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to convert unstructured httproute: %w", err)
		}

		downstreamObjectMeta, err := downstreamStrategy.ObjectMetaFromUpstreamObject(ctx, httpRoute)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed getting downstream object metadata: %w", err)
		}

		logger.Info("looking for endpointslices", "downstream_namespace", downstreamObjectMeta.Namespace)

		for ruleIdx, rule := range httpRoute.Spec.Rules {
			for backendRefIdx, backendRef := range rule.BackendRefs {
				if ptr.Deref(backendRef.Group, "") != discoveryv1.GroupName ||
					ptr.Deref(backendRef.Kind, "") != "EndpointSlice" {
					continue
				}

				resourceName := fmt.Sprintf("route-%s-rule-%d-backendref-%d", httpRoute.UID, ruleIdx, backendRefIdx)

				endpointSlice := &discoveryv1.EndpointSlice{}
				if err := r.DownstreamCluster.GetClient().Get(ctx, client.ObjectKey{
					Namespace: string(ptr.Deref(backendRef.Namespace, gatewayv1.Namespace(downstreamObjectMeta.Namespace))),
					Name:      resourceName,
				}, endpointSlice); err != nil {
					if apierrors.IsNotFound(err) {
						logger.Info("endpointslice not found", "namespace", downstreamObjectMeta.Namespace, "name", resourceName)
						// Nothing to do
						continue
					}
					return ctrl.Result{}, fmt.Errorf("failed fetching endpointslice: %w", err)
				}

				logger.Info("deleting endpointslice", "namespace", downstreamObjectMeta.Namespace, "name", resourceName)

				if dt := endpointSlice.GetDeletionTimestamp(); dt == nil {
					if err := r.DownstreamCluster.GetClient().Delete(ctx, endpointSlice); err != nil {
						return ctrl.Result{}, fmt.Errorf("failed to delete endpointslice: %w", err)
					}
				}
			}
		}
	}

	if controllerutil.RemoveFinalizer(&obj, gatewayControllerGCFinalizer) {
		if err := cl.GetClient().Update(ctx, &obj); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GatewayDownstreamGCReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	return mcbuilder.TypedControllerManagedBy[GVKRequest](mgr).
		Watches(&gatewayv1.HTTPRoute{}, TypedEnqueueRequestForObjectWithGVK(&gatewayv1.HTTPRoute{})).
		Watches(&discoveryv1.EndpointSlice{}, TypedEnqueueRequestForObjectWithGVK(&discoveryv1.EndpointSlice{})).
		Named("gateway_downstream_resources").
		Complete(r)
}

func TypedEnqueueRequestForObjectWithGVK(
	obj client.Object,
) mchandler.TypedEventHandlerFunc[client.Object, GVKRequest] {
	return func(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[client.Object, GVKRequest] {
		return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []GVKRequest {
			// Only reconcile objects that are being deleted
			if dt := obj.GetDeletionTimestamp(); dt.IsZero() {
				return nil
			}

			logger := log.FromContext(ctx)

			gvk, err := apiutil.GVKForObject(obj, cl.GetScheme())
			if err != nil {
				logger.Error(fmt.Errorf("failed to get GVK in TypedEnqueueRequestForObjectWithGVK: %w", err), "cluster", clusterName, clusterName)
				return nil
			}

			return []GVKRequest{
				{
					GVK: gvk,
					Request: mcreconcile.Request{
						ClusterName: clusterName,
						Request: reconcile.Request{
							NamespacedName: types.NamespacedName{
								Namespace: obj.GetNamespace(),
								Name:      obj.GetName(),
							},
						},
					},
				},
			}
		})
	}
}
