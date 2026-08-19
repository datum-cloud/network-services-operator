// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

// ProjectClusterResolver hands back a client for one project's control plane.
type ProjectClusterResolver interface {
	ClientForProject(ctx context.Context, project string) (client.Client, error)
}

// NewProjectClusterResolver reaches project control planes through the clusters
// the multicluster manager already engages, so the hub controller needs no
// credentials of its own.
func NewProjectClusterResolver(mgr mcmanager.Manager) ProjectClusterResolver {
	return &managerProjectClusterResolver{mgr: mgr}
}

type managerProjectClusterResolver struct {
	mgr mcmanager.Manager
}

func (r *managerProjectClusterResolver) ClientForProject(ctx context.Context, project string) (client.Client, error) {
	cl, err := r.mgr.GetCluster(ctx, multicluster.ClusterName(project))
	if err != nil {
		return nil, err
	}
	return cl.GetClient(), nil
}

// NetworkPresenceReconciler maintains one NetworkContext per (project, network,
// location) triple on the hub, for as long as any NetworkBinding declares the
// network is needed there.
//
// It is the only component with both a view of the hub and a view of project
// control planes, which is why the projection and the garbage collection in
// networkpresence_gc.go both live here.
type NetworkPresenceReconciler struct {
	Projects ProjectClusterResolver

	hub client.Client
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkbindings,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkbindings/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkcontexts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=locationbindings,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

// Reconcile is keyed on the presence, not on the binding that triggered it: the
// request names the hub namespace and the deterministic NetworkContext name. A
// binding being deleted is what has to decide teardown, and a deleted object
// cannot be read for the pair it declared.
func (r *NetworkPresenceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Only hub namespaces are this controller's. Where the hub and the project
	// control planes are separate clusters this is every namespace it can see;
	// where one cluster plays both roles it is not, and serving a project-plane
	// binding would fight the controller that owns it and tear down a context
	// this controller never created.
	serves, err := isHubNamespace(ctx, r.hub, req.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !serves {
		return ctrl.Result{}, nil
	}

	holders, err := r.holders(ctx, req)
	if err != nil {
		return ctrl.Result{}, err
	}

	if len(holders) == 0 {
		return ctrl.Result{}, r.teardown(ctx, req)
	}

	logger.Info("reconciling network presence", "holders", len(holders))
	defer logger.Info("reconcile complete")

	refused, err := r.ensure(ctx, req, holders)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Nothing watches a LocationBinding, a project namespace, or a network that
	// does not exist yet, so a refusal for a condition that later clears has no
	// other way back.
	if refused {
		return ctrl.Result{RequeueAfter: refusedPresenceRetryInterval}, nil
	}
	return ctrl.Result{}, nil
}

const refusedPresenceRetryInterval = time.Minute

// holders lists the bindings declaring the presence this request names. The
// count is a list rather than a stored number, so it cannot drift.
func (r *NetworkPresenceReconciler) holders(ctx context.Context, req ctrl.Request) ([]networkingv1alpha.NetworkBinding, error) {
	var bindings networkingv1alpha.NetworkBindingList
	if err := r.hub.List(ctx, &bindings, client.InNamespace(req.Namespace)); err != nil {
		return nil, fmt.Errorf("failed listing network bindings: %w", err)
	}

	holders := make([]networkingv1alpha.NetworkBinding, 0, len(bindings.Items))
	for _, binding := range bindings.Items {
		if !binding.DeletionTimestamp.IsZero() {
			continue
		}
		if networkContextNameForBinding(&binding) == req.Name {
			holders = append(holders, binding)
		}
	}
	return holders, nil
}

// teardown removes a presence nothing declares any more. At the location a
// local finalizer holds the propagated copy while addresses are still held, so
// this is prompt in the ordinary case and blocks where it must.
func (r *NetworkPresenceReconciler) teardown(ctx context.Context, req ctrl.Request) error {
	var networkContext networkingv1alpha.NetworkContext
	if err := r.hub.Get(ctx, req.NamespacedName, &networkContext); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !networkContext.DeletionTimestamp.IsZero() {
		return nil
	}

	// Only a presence this controller wrote is this controller's to remove. It
	// stamps the network UID on everything it creates, so a context without one
	// was put here by something else — a copy propagated in, or a context that
	// predates this controller — and deleting it would take away the object a
	// location reads.
	if networkContext.Labels[networkingv1alpha.NetworkUIDLabel] == "" {
		return nil
	}

	log.FromContext(ctx).Info("no binding declares this network presence, removing it")
	if err := r.hub.Delete(ctx, &networkContext); err != nil {
		return client.IgnoreNotFound(err)
	}
	return nil
}

// ensure reports whether it refused, so a refusal with no watch behind it gets
// a way back.
func (r *NetworkPresenceReconciler) ensure(
	ctx context.Context,
	req ctrl.Request,
	holders []networkingv1alpha.NetworkBinding,
) (bool, error) {
	pair := holders[0].Spec

	routing, err := resolveProjectRouting(ctx, r.hub, req.Namespace)
	if err != nil {
		var unresolvable *projectUnresolvable
		if errors.As(err, &unresolvable) {
			return true, r.report(ctx, holders, nil, refusal(
				networkingv1alpha.NetworkBindingReasonProjectUnresolved, unresolvable.Error()))
		}
		return false, err
	}

	projectClient, err := r.Projects.ClientForProject(ctx, routing.project)
	if err != nil {
		if errors.Is(err, multicluster.ErrClusterNotFound) {
			return true, r.report(ctx, holders, nil, refusal(
				networkingv1alpha.NetworkBindingReasonProjectUnresolved,
				fmt.Sprintf("Project %q is not engaged by this operator", routing.project)))
		}
		return false, fmt.Errorf("failed reaching project %q: %w", routing.project, err)
	}

	var locationBinding networkingv1alpha.LocationBinding
	if err := projectClient.Get(ctx, client.ObjectKey{Name: pair.Location.Name}, &locationBinding); err != nil {
		if apierrors.IsNotFound(err) {
			return true, r.report(ctx, holders, nil, refusal(
				networkingv1alpha.NetworkBindingReasonLocationNotAvailable,
				fmt.Sprintf("Project %q has no location binding for location %q",
					routing.project, pair.Location.Name)))
		}
		return false, fmt.Errorf("failed reading location binding %q: %w", pair.Location.Name, err)
	}

	networkKey := client.ObjectKey{Namespace: routing.projectNamespace, Name: pair.Network.Name}
	if pair.Network.Namespace != "" {
		networkKey.Namespace = pair.Network.Namespace
	}

	var network networkingv1alpha.Network
	if err := projectClient.Get(ctx, networkKey, &network); err != nil {
		if apierrors.IsNotFound(err) {
			return true, r.report(ctx, holders, nil, refusal(
				networkingv1alpha.NetworkBindingReasonNetworkNotFound,
				fmt.Sprintf("Network %q was not found in namespace %q of project %q",
					networkKey.Name, networkKey.Namespace, routing.project)))
		}
		return false, fmt.Errorf("failed reading network %q: %w", networkKey.Name, err)
	}

	if err := r.stamp(ctx, holders, &network); err != nil {
		return false, err
	}

	networkContext, err := r.project(ctx, req, routing, &pair, &network)
	if err != nil {
		return false, err
	}

	ref := &networkingv1alpha.NetworkContextRef{
		Namespace: networkContext.Namespace,
		Name:      networkContext.Name,
	}

	if !apimeta.IsStatusConditionTrue(networkContext.Status.Conditions, networkingv1alpha.NetworkContextReady) {
		return false, r.report(ctx, holders, ref, refusal(
			networkingv1alpha.NetworkBindingReasonNetworkContextNotReady,
			"Network context is not ready."))
	}

	return false, r.report(ctx, holders, ref, metav1.Condition{
		Type:    networkingv1alpha.NetworkBindingReady,
		Status:  metav1.ConditionTrue,
		Reason:  networkingv1alpha.NetworkBindingReasonNetworkContextReady,
		Message: "Network context is ready.",
	})
}

// stamp records the network's UID on every binding declaring the presence, which
// is what garbage collection keys on. A consumer cannot be asked for it: the
// binding names a network by name and the UID lives in a control plane the
// consumer may not read.
//
// It runs before the context is written, so a binding is countable by the time
// anything it caused to exist is.
func (r *NetworkPresenceReconciler) stamp(
	ctx context.Context,
	holders []networkingv1alpha.NetworkBinding,
	network *networkingv1alpha.Network,
) error {
	var errs []error
	for i := range holders {
		binding := &holders[i]
		if binding.Labels[networkingv1alpha.NetworkUIDLabel] == string(network.UID) {
			continue
		}

		patch := client.MergeFrom(binding.DeepCopy())
		if binding.Labels == nil {
			binding.Labels = map[string]string{}
		}
		binding.Labels[networkingv1alpha.NetworkUIDLabel] = string(network.UID)

		if err := r.hub.Patch(ctx, binding, patch); err != nil {
			errs = append(errs, fmt.Errorf("failed labelling network binding %q: %w", binding.Name, err))
		}
	}
	return errors.Join(errs...)
}

// project writes the network's rules into the hub context. Everything a
// location reads is in spec, because propagation carries nothing else.
func (r *NetworkPresenceReconciler) project(
	ctx context.Context,
	req ctrl.Request,
	routing projectRouting,
	pair *networkingv1alpha.NetworkBindingSpec,
	network *networkingv1alpha.Network,
) (*networkingv1alpha.NetworkContext, error) {
	networkContext := &networkingv1alpha.NetworkContext{}
	networkContext.Namespace = req.Namespace
	networkContext.Name = req.Name

	_, err := controllerutil.CreateOrUpdate(ctx, r.hub, networkContext, func() error {
		if networkContext.Labels == nil {
			networkContext.Labels = map[string]string{}
		}
		networkContext.Labels[downstreamclient.UpstreamOwnerClusterNameLabel] = routing.clusterNameLabel
		networkContext.Labels[downstreamclient.UpstreamOwnerNamespaceLabel] = routing.projectNamespace
		networkContext.Labels[networkingv1alpha.NetworkLabel] = pair.Network.Name
		networkContext.Labels[networkingv1alpha.LocationLabel] = pair.Location.Name
		networkContext.Labels[networkingv1alpha.NetworkUIDLabel] = string(network.UID)

		networkContext.Spec.Network = networkingv1alpha.LocalNetworkRef{Name: pair.Network.Name}
		networkContext.Spec.Location = pair.Location
		networkContext.Spec.IPFamilies = append([]networkingv1alpha.IPFamily(nil), network.Spec.IPFamilies...)
		networkContext.Spec.MTU = network.Spec.MTU
		projectRoutingIdentityIfAllocated(networkContext, network)
		networkContext.Spec.NetworkGeneration = network.Generation
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed writing network context %q: %w", req.Name, err)
	}

	return networkContext, nil
}

// projectRoutingIdentityIfAllocated leaves an identity already delivered to a
// location in place. An identity does not change, so a network read before its
// allocation lands has nothing to say about the one a location is already
// forwarding on.
func projectRoutingIdentityIfAllocated(
	networkContext *networkingv1alpha.NetworkContext,
	network *networkingv1alpha.Network,
) {
	if network.Status.RoutingIdentity == nil {
		return
	}
	networkContext.Spec.RoutingIdentity = network.Status.RoutingIdentity.Prefix
}

// report writes the same answer onto every binding for the pair, so a consumer
// never has to find the shared context or reason about the other consumers of
// it. Status is written only where it differs from what is already recorded.
func (r *NetworkPresenceReconciler) report(
	ctx context.Context,
	holders []networkingv1alpha.NetworkBinding,
	ref *networkingv1alpha.NetworkContextRef,
	condition metav1.Condition,
) error {
	var errs []error
	for i := range holders {
		binding := &holders[i]

		changed := false
		if ref != nil && binding.Status.NetworkContextRef == nil {
			binding.Status.NetworkContextRef = ref
			changed = true
		}

		condition.ObservedGeneration = binding.Generation
		if apimeta.SetStatusCondition(&binding.Status.Conditions, condition) {
			changed = true
		}

		if !changed {
			continue
		}

		if err := r.hub.Status().Update(ctx, binding); err != nil {
			errs = append(errs, fmt.Errorf("failed updating status of network binding %q: %w", binding.Name, err))
		}
	}
	return errors.Join(errs...)
}

func refusal(reason, message string) metav1.Condition {
	return metav1.Condition{
		Type:    networkingv1alpha.NetworkBindingReady,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: message,
	}
}

// isHubNamespace reports whether a namespace was created to hold one project's
// objects, which is what makes it a hub namespace rather than a namespace in a
// project control plane.
//
// Either label is enough. A namespace carrying one of them is a hub namespace
// that is missing the other, and it has to stay this controller's so the
// binding reports ProjectUnresolved rather than being served by both
// controllers or by neither.
func isHubNamespace(ctx context.Context, cl client.Client, namespaceName string) (bool, error) {
	var namespace corev1.Namespace
	if err := cl.Get(ctx, client.ObjectKey{Name: namespaceName}, &namespace); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed reading namespace %q: %w", namespaceName, err)
	}

	for _, label := range []string{
		downstreamclient.UpstreamOwnerClusterNameLabel,
		downstreamclient.UpstreamOwnerNamespaceLabel,
	} {
		if namespace.Labels[label] != "" {
			return true, nil
		}
	}
	return false, nil
}

// SetupWithManager registers the controller against the hub.
//
// This must be a manager that runs one replica. The sharded managers run three
// with leader election disabled, so registering there reconciles every hub
// object in all three.
func (r *NetworkPresenceReconciler) SetupWithManager(mgr manager.Manager) error {
	if r.Projects == nil {
		return errors.New("a project cluster resolver is required")
	}
	r.hub = mgr.GetClient()

	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkContext{}).
		Watches(&networkingv1alpha.NetworkBinding{}, handler.EnqueueRequestsFromMapFunc(
			func(_ context.Context, obj client.Object) []reconcile.Request {
				binding, ok := obj.(*networkingv1alpha.NetworkBinding)
				if !ok {
					return nil
				}
				return []reconcile.Request{{NamespacedName: client.ObjectKey{
					Namespace: binding.Namespace,
					Name:      networkContextNameForBinding(binding),
				}}}
			})).
		Named("networkpresence").
		Complete(r)
}
