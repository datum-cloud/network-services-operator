// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

// NetworkInterfaceReconciler releases an interface's addresses when the
// interface is deleted. A retained interface has no claim to do this for it.
type NetworkInterfaceReconciler struct {
	Location config.LocationConfig
	IPAM     IPAMClientFactory

	claims *NetworkInterfaceClaimReconciler
	mgr    mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces/finalizers,verbs=update

func (r *NetworkInterfaceReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("reconciling network interface")
	defer logger.Info("reconcile complete")

	return ctrl.Result{}, r.reconcileInterface(ctx, cl.GetClient(), req.NamespacedName)
}

func (r *NetworkInterfaceReconciler) reconcileInterface(
	ctx context.Context,
	cl client.Client,
	key client.ObjectKey,
) error {
	var iface networkingv1alpha.NetworkInterface
	if err := cl.Get(ctx, key, &iface); err != nil {
		return client.IgnoreNotFound(err)
	}

	if iface.DeletionTimestamp.IsZero() {
		if controllerutil.AddFinalizer(&iface, networkInterfaceFinalizer) {
			if err := cl.Update(ctx, &iface); err != nil {
				return fmt.Errorf("failed adding finalizer: %w", err)
			}
		}
		return nil
	}

	if !controllerutil.ContainsFinalizer(&iface, networkInterfaceFinalizer) {
		return nil
	}

	held, err := r.heldByLiveClaim(ctx, cl, &iface)
	if err != nil {
		return err
	}

	// The claim rebuilds the interface and finds the same addresses by name.
	// Releasing here would give a running workload new ones.
	if !held {
		if err := r.claims.releaseAddresses(ctx, cl, iface.Namespace, &iface); err != nil {
			return err
		}
	}

	controllerutil.RemoveFinalizer(&iface, networkInterfaceFinalizer)
	if err := cl.Update(ctx, &iface); err != nil {
		return fmt.Errorf("failed removing finalizer: %w", err)
	}

	location, err := r.claims.location(ctx)
	if err != nil {
		return err
	}

	return r.claims.syncNetworkContextHold(ctx, cl, iface.Namespace, iface.Spec.Network.Name, location)
}

// heldByLiveClaim reports whether a claim named in claimRef still exists and is
// not being deleted.
func (r *NetworkInterfaceReconciler) heldByLiveClaim(
	ctx context.Context,
	cl client.Client,
	iface *networkingv1alpha.NetworkInterface,
) (bool, error) {
	ref := iface.Spec.ClaimRef
	if ref == nil {
		return false, nil
	}

	var claim networkingv1alpha.NetworkInterfaceClaim
	key := client.ObjectKey{Namespace: iface.Namespace, Name: ref.Name}
	if err := cl.Get(ctx, key, &claim); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed reading claim %q: %w", ref.Name, err)
	}

	// A claim being deleted holds nothing. Its own release cannot free these
	// addresses once the interface is gone.
	return claim.DeletionTimestamp.IsZero(), nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NetworkInterfaceReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	if r.IPAM == nil {
		return errors.New("an IPAM client factory is required")
	}

	r.mgr = mgr
	r.claims = &NetworkInterfaceClaimReconciler{
		Location:    r.Location,
		IPAM:        r.IPAM,
		mgr:         mgr,
		localReader: mgr.GetLocalManager().GetClient(),
	}

	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkInterface{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Named("networkinterface").
		Complete(r)
}
