package controller

import (
	"context"
	"fmt"

	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"go.datum.net/network-services-operator/internal/downstreamclient"
)

const endpointSliceControllerFinalizer = "endpointslices.controller.datumapis.com/finalizer"

type EndpointSliceReconciler struct {
	mgr               mcmanager.Manager
	DownstreamCluster cluster.Cluster
}

func (r *EndpointSliceReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	return mcbuilder.ControllerManagedBy(mgr).
		Named("endpointslice-controller").
		For(&discoveryv1.EndpointSlice{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Complete(r)
}

// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices/finalizers,verbs=update

func (r *EndpointSliceReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)

	upstreamCluster, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		logger.Error(err, "failed to get upstream cluster client")
		return ctrl.Result{}, err
	}
	upstreamClient := upstreamCluster.GetClient()

	var upstreamEndpointSlice discoveryv1.EndpointSlice
	if err := upstreamClient.Get(ctx, req.NamespacedName, &upstreamEndpointSlice); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("reconciling endpointslice")
	defer logger.Info("reconcile complete")

	downstreamStrategy := downstreamclient.NewMappedNamespaceResourceStrategy(req.ClusterName, upstreamClient, r.DownstreamCluster.GetClient())
	downstreamClient := downstreamStrategy.GetClient()

	if !upstreamEndpointSlice.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&upstreamEndpointSlice, endpointSliceControllerFinalizer) {
			logger.Info("finalizing upstream endpointslice")

			controllerutil.RemoveFinalizer(&upstreamEndpointSlice, endpointSliceControllerFinalizer)
			if err := upstreamClient.Update(ctx, &upstreamEndpointSlice); err != nil {
				logger.Error(err, "failed to remove finalizer from upstream endpointslice")
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from upstream endpointslice: %w", err)
			}
			logger.Info("removed finalizer from upstream endpointslice")
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&upstreamEndpointSlice, endpointSliceControllerFinalizer) {
		controllerutil.AddFinalizer(&upstreamEndpointSlice, endpointSliceControllerFinalizer)
		if err := upstreamClient.Update(ctx, &upstreamEndpointSlice); err != nil {
			logger.Error(err, "failed to add finalizer to upstream endpointslice")
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer to upstream endpointslice: %w", err)
		}
		logger.Info("added finalizer to upstream endpointslice")
		return ctrl.Result{}, nil
	}

	downstreamObjectMeta, err := downstreamStrategy.ObjectMetaFromUpstreamObject(ctx, &upstreamEndpointSlice)
	if err != nil {
		logger.Error(err, "failed to get downstream object metadata")
		return ctrl.Result{}, fmt.Errorf("failed to get downstream object metadata for endpointslices: %w", err)
	}

	var downstreamEndpointSliceList discoveryv1.EndpointSliceList
	listOpts := []client.ListOption{
		client.InNamespace(downstreamObjectMeta.Namespace),
		client.MatchingLabels{
			downstreamclient.UpstreamOwnerNameLabel: upstreamEndpointSlice.Name,
		},
	}

	if err := downstreamClient.List(ctx, &downstreamEndpointSliceList, listOpts...); err != nil {
		logger.Error(err, "failed to list downstream endpointslice")
		return ctrl.Result{}, fmt.Errorf("failed listing downstream endpointslice in namespace %q: %w", downstreamObjectMeta.Namespace, err)
	}

	for _, downstreamEndpointSlice := range downstreamEndpointSliceList.Items {

		// Update existing downstream EndpointSlice
		logger.Info("updating downstream endpointslice", "namespace", downstreamEndpointSlice.Namespace, "name", downstreamEndpointSlice.Name)

		// Create a deep copy for comparison later
		originalDownstreamEndpointSlice := downstreamEndpointSlice.DeepCopy()

		// Apply desired changes directly to the object
		downstreamEndpointSlice.AddressType = upstreamEndpointSlice.AddressType
		downstreamEndpointSlice.Endpoints = upstreamEndpointSlice.Endpoints
		downstreamEndpointSlice.Ports = upstreamEndpointSlice.Ports

		if !equality.Semantic.DeepEqual(originalDownstreamEndpointSlice, downstreamEndpointSlice) {
			logger.Info("changes detected, updating downstream endpointslice", "namespace", downstreamEndpointSlice.Namespace, "name", downstreamEndpointSlice.Name)
			if err := downstreamClient.Update(ctx, &downstreamEndpointSlice); err != nil {
				if apierrors.IsConflict(err) {
					return ctrl.Result{Requeue: true}, nil
				}
				return ctrl.Result{}, fmt.Errorf("failed updating downstream endpointslice %s/%s: %w", downstreamEndpointSlice.Namespace, downstreamEndpointSlice.Name, err)
			}
		}
	}

	return ctrl.Result{}, nil
}
