// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"

	mcbuilder "github.com/multicluster-runtime/multicluster-runtime/pkg/builder"
	mcmanager "github.com/multicluster-runtime/multicluster-runtime/pkg/manager"
	mcreconcile "github.com/multicluster-runtime/multicluster-runtime/pkg/reconcile"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// NetworkBindingReconciler reconciles a NetworkBinding object
type NetworkBindingReconciler struct {
	mgr mcmanager.Manager
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkbindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkbindings/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkbindings/finalizers,verbs=update

func (r *NetworkBindingReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (_ ctrl.Result, err error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)

	// Each valid network binding should result in a NetworkAttachment being
	// created for each unique `topology` that's found.

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	var binding networkingv1alpha.NetworkBinding
	if err := cl.GetClient().Get(ctx, req.NamespacedName, &binding); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !binding.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	logger.Info("reconciling network binding")
	defer logger.Info("reconcile complete")

	readyCondition := metav1.Condition{
		Type:               networkingv1alpha.NetworkBindingReady,
		Status:             metav1.ConditionFalse,
		Reason:             "Unknown",
		ObservedGeneration: binding.Generation,
		Message:            "Unknown state",
	}

	defer func() {
		if err != nil {
			// Don't update the status if errors are encountered
			return
		}
		statusChanged := apimeta.SetStatusCondition(&binding.Status.Conditions, readyCondition)

		if statusChanged {
			err = cl.GetClient().Status().Update(ctx, &binding)
		}
	}()

	networkNamespace := binding.Spec.Network.Namespace

	if len(networkNamespace) == 0 {
		// Fall back to binding's namespace if NetworkRef does not specify one.
		networkNamespace = binding.Namespace
	}

	var network networkingv1alpha.Network
	networkObjectKey := client.ObjectKey{
		Namespace: networkNamespace,
		Name:      binding.Spec.Network.Name,
	}
	if err := cl.GetClient().Get(ctx, networkObjectKey, &network); err != nil {
		readyCondition.Reason = "NetworkNotFound"
		readyCondition.Message = "The network referenced in the binding was not found."
		return ctrl.Result{}, fmt.Errorf("failed fetching network for binding: %w", err)
	}

	var networkContext networkingv1alpha.NetworkContext
	networkContextObjectKey := client.ObjectKey{
		Namespace: networkNamespace,
		Name:      networkContextNameForBinding(&binding),
	}
	if err := cl.GetClient().Get(ctx, networkContextObjectKey, &networkContext); client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, fmt.Errorf("failed fetching network context: %w", err)
	}

	if networkContext.CreationTimestamp.IsZero() {
		networkContext = networkingv1alpha.NetworkContext{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: networkContextObjectKey.Namespace,
				Name:      networkContextObjectKey.Name,
			},
			Spec: networkingv1alpha.NetworkContextSpec{
				Network: networkingv1alpha.LocalNetworkRef{
					Name: binding.Spec.Network.Name,
				},
				Location: binding.Spec.Location,
			},
		}

		if err := controllerutil.SetControllerReference(&network, &networkContext, cl.GetScheme()); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set controller on network context: %w", err)
		}

		if err := cl.GetClient().Create(ctx, &networkContext); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed creating network context: %w", err)
		}
	}

	if !apimeta.IsStatusConditionTrue(networkContext.Status.Conditions, networkingv1alpha.NetworkContextReady) {
		logger.Info("network context is not ready")
		readyCondition.Reason = "NetworkContextNotReady"
		readyCondition.Message = "Network context is not ready."

		// Choosing to requeue here instead of establishing a watch on contexts, as
		// once the context is created an ready, future bindings will immediately
		// become ready.
		return ctrl.Result{Requeue: true}, nil
	}

	binding.Status.NetworkContextRef = &networkingv1alpha.NetworkContextRef{
		Namespace: networkContext.Namespace,
		Name:      networkContext.Name,
	}

	readyCondition.Status = metav1.ConditionTrue
	readyCondition.Reason = "NetworkContextReady"
	readyCondition.Message = "Network context is ready."

	// Update is handled in the defer function above.

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NetworkBindingReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkBinding{}, mcbuilder.WithPredicates(
			predicate.NewPredicateFuncs(func(object client.Object) bool {
				o := object.(*networkingv1alpha.NetworkBinding)
				return o.Status.NetworkContextRef == nil
			}),
		)).
		Complete(r)
}

func networkContextNameForBinding(binding *networkingv1alpha.NetworkBinding) string {
	return fmt.Sprintf("%s-%s-%s", binding.Spec.Network.Name, binding.Spec.Location.Namespace, binding.Spec.Location.Name)
}
