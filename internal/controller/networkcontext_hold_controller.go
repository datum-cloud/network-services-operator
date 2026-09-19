// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

// NetworkContextHoldReconciler owns the cell-local finalizer that holds a
// propagated NetworkContext while an interface here still holds addresses on
// the network.
//
// The finalizer lives on the context, so the decision to release it has to be
// keyed on the context too. Driving it only from whatever released the last
// interface evaluates the hold while that interface is still terminating, and
// then never looks again, leaving the copy stuck terminating for good.
type NetworkContextHoldReconciler struct {
	Location config.LocationConfig

	mgr mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkcontexts,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkcontexts/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces,verbs=get;list;watch

func (r *NetworkContextHoldReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, reconcileNetworkContextHold(ctx, cl.GetClient(), req.NamespacedName)
}

func (r *NetworkContextHoldReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	location := locationsv1alpha1.LocationReference{
		Name: r.Location.Name,
	}

	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkContext{}, mcbuilder.WithEngageWithLocalCluster(false)).
		// An interface going away is what can release the hold, and the context
		// itself gets no event for it.
		Watches(&networkingv1alpha.NetworkInterface{}, mchandler.EnqueueRequestsFromMapFunc(
			func(_ context.Context, obj client.Object) []ctrl.Request {
				iface, ok := obj.(*networkingv1alpha.NetworkInterface)
				if !ok || iface.Spec.Network.Name == "" {
					return nil
				}
				return []ctrl.Request{{NamespacedName: client.ObjectKey{
					Namespace: iface.Namespace,
					Name:      networkContextName(iface.Spec.Network.Name, location),
				}}}
			})).
		Named("networkcontexthold").
		Complete(r)
}
