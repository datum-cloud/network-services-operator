// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

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

	// A binding in a namespace that names a project is a hub binding, and the
	// presence controller owns it. The two never overlap where the hub and the
	// project control planes are separate clusters; they do where one cluster
	// plays both roles, and then both would write the same context and fight
	// over the same status.
	hubBinding, err := isHubNamespace(ctx, cl.GetClient(), binding.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}
	if hubBinding {
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
		if apimeta.SetStatusCondition(&binding.Status.Conditions, readyCondition) {
			err = errors.Join(err, cl.GetClient().Status().Update(ctx, &binding))
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

		// Backstop only: the watch on contexts above covers the normal case.
		return ctrl.Result{RequeueAfter: time.Second * 5}, nil
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
//
// Bindings are reconciled regardless of whether they already reference a
// context. A binding that stopped reconciling once its reference was set would
// never notice the context going away, and nothing else recreates it.
func (r *NetworkBindingReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkBinding{},
			mcbuilder.WithEngageWithLocalCluster(false),
		).
		Watches(
			&networkingv1alpha.NetworkContext{},
			func(clusterName multicluster.ClusterName, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
				return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
					return networkBindingRequestsForContext(ctx, cl.GetClient(), clusterName, obj)
				})
			},
		).
		Named("networkbinding").
		Complete(r)
}

// networkBindingRequestsForContext maps a NetworkContext back to the bindings
// that resolve to it, so a context being deleted or becoming ready re-triggers
// them.
func networkBindingRequestsForContext(
	ctx context.Context,
	c client.Client,
	clusterName multicluster.ClusterName,
	obj client.Object,
) []mcreconcile.Request {
	logger := log.FromContext(ctx)

	networkContext, ok := obj.(*networkingv1alpha.NetworkContext)
	if !ok {
		return nil
	}

	var bindings networkingv1alpha.NetworkBindingList
	if err := c.List(ctx, &bindings, client.InNamespace(networkContext.Namespace)); err != nil {
		logger.Error(err, "failed to list NetworkBindings for NetworkContext watch", "networkContext", networkContext.Name)
		return nil
	}

	var requests []mcreconcile.Request
	for i := range bindings.Items {
		binding := &bindings.Items[i]
		if networkContextNameForBinding(binding) != networkContext.Name {
			continue
		}
		requests = append(requests, mcreconcile.Request{
			ClusterName: clusterName,
			Request: ctrl.Request{
				NamespacedName: client.ObjectKeyFromObject(binding),
			},
		})
	}

	return requests
}

func networkContextNameForBinding(binding *networkingv1alpha.NetworkBinding) string {
	return networkContextName(binding.Spec.Network.Name, binding.Spec.Location)
}

func networkContextName(network string, location networkingv1alpha.LocationReference) string {
	return fmt.Sprintf("%s-%s", network, location.Name)
}
