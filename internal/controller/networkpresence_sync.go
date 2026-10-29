// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// hubNamespaceProjectIndex indexes hub namespaces by the project namespace they
// stand for, as "<project>/<namespace>". A hub namespace names both on itself,
// which is the only place the two are related.
const hubNamespaceProjectIndex = "hubNamespaceProjectIndex"

// NetworkPresenceObservedAnnotation carries, on every hub binding declaring a
// presence, the last state of a project-plane object the presence is derived
// from. The presence controller watches the bindings, so changing it is how a
// change it cannot see reaches it, in whichever replica it runs.
const NetworkPresenceObservedAnnotation = "networking.datumapis.com/presence-observed"

// NetworkPresenceSyncReconciler brings a presence back to the controller that
// owns it when something it is derived from changes.
//
// NetworkPresenceReconciler is level-triggered on the hub and reads a Network, a
// projected Location and a NetworkContext that all live in a project control plane
// it does not watch. A network edited after a location already carries it, or a
// context that finished being deleted, are both invisible there.
//
// This controller watches those control planes and does nothing else: it maps
// what it sees onto the presence the change is about and records the change in
// one annotation on the hub bindings declaring it. That write goes through the
// API server, so it reaches the presence controller however the two are spread
// across replicas. It writes nothing else, holds no finalizer, deletes nothing
// and reports no status, so the presence keeps exactly one writer.
type NetworkPresenceSyncReconciler struct {
	Projects ProjectClusterResolver

	hub client.Client
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networks,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkcontexts,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkbindings,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

// reconcileNetworkContext maps a context in a project control plane onto the
// presences the hub holds for it. A context lives in the project that named it
// and in that project's own namespace, and its name is the presence's name, so
// the hub namespaces standing for that pair are the whole answer.
//
// The event that matters most is the context finishing being deleted, and there
// is nothing left to read by then; the request names everything the mapping
// needs, and a context that is gone is recorded as such.
func (r *NetworkPresenceSyncReconciler) reconcileNetworkContext(
	ctx context.Context,
	req mcreconcile.Request,
) (ctrl.Result, error) {
	namespaces, err := r.hubNamespaces(ctx, string(req.ClusterName), req.Namespace)
	if err != nil || len(namespaces) == 0 {
		return ctrl.Result{}, err
	}

	observed, err := r.observe(ctx, req, &networkingv1alpha.NetworkContext{})
	if err != nil {
		return ctrl.Result{}, err
	}

	var errs []error
	for _, namespace := range namespaces {
		errs = append(errs, r.signal(ctx, namespace, req.Name, observed))
	}
	return ctrl.Result{}, errors.Join(errs...)
}

// reconcileNetwork maps a network onto every presence derived from it. The
// network carries nothing naming the hub, so the relation is read the other way
// round: the hub namespaces standing for the network's own namespace, and the
// bindings in them that name it.
//
// The bindings are matched on spec rather than on the network UID label, because
// the label is only stamped once the network has been found. A presence refused
// for a network that did not exist yet carries no label to be found by.
func (r *NetworkPresenceSyncReconciler) reconcileNetwork(
	ctx context.Context,
	req mcreconcile.Request,
) (ctrl.Result, error) {
	namespaces, err := r.hubNamespaces(ctx, string(req.ClusterName), req.Namespace)
	if err != nil || len(namespaces) == 0 {
		return ctrl.Result{}, err
	}

	observed, err := r.observe(ctx, req, &networkingv1alpha.Network{})
	if err != nil {
		return ctrl.Result{}, err
	}

	var errs []error
	for _, namespace := range namespaces {
		var bindings networkingv1alpha.NetworkBindingList
		if err := r.hub.List(ctx, &bindings, client.InNamespace(namespace)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed listing network bindings in %q: %w", namespace, err)
		}

		for i := range bindings.Items {
			binding := &bindings.Items[i]
			if binding.Spec.Network.Name != req.Name {
				continue
			}
			errs = append(errs, r.mark(ctx, binding, observed))
		}
	}
	return ctrl.Result{}, errors.Join(errs...)
}

// observe names the state of the project-plane object a request is about: its
// kind, UID and resource version, or that it is gone.
func (r *NetworkPresenceSyncReconciler) observe(
	ctx context.Context,
	req mcreconcile.Request,
	obj client.Object,
) (string, error) {
	kind := fmt.Sprintf("%T", obj)
	switch obj.(type) {
	case *networkingv1alpha.Network:
		kind = "Network"
	case *networkingv1alpha.NetworkContext:
		kind = "NetworkContext"
	}

	project, err := r.Projects.ClusterForProject(ctx, string(req.ClusterName))
	if err != nil {
		return "", fmt.Errorf("failed reaching project %q: %w", req.ClusterName, err)
	}
	if err := project.GetClient().Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return kind + "/" + req.Name + "/gone", nil
		}
		return "", fmt.Errorf("failed reading %s %q: %w", kind, req.NamespacedName, err)
	}
	return kind + "/" + req.Name + "/" + string(obj.GetUID()) + "/" + obj.GetResourceVersion(), nil
}

// hubNamespaces lists the hub namespaces standing for one namespace of one
// project.
func (r *NetworkPresenceSyncReconciler) hubNamespaces(
	ctx context.Context,
	project string,
	projectNamespace string,
) ([]string, error) {
	if project == "" || projectNamespace == "" {
		return nil, nil
	}

	var namespaces corev1.NamespaceList
	if err := r.hub.List(ctx, &namespaces, client.MatchingFields{
		hubNamespaceProjectIndex: hubNamespaceKey(project, projectNamespace),
	}); err != nil {
		return nil, fmt.Errorf("failed listing hub namespaces for %q/%q: %w", project, projectNamespace, err)
	}

	names := make([]string, 0, len(namespaces.Items))
	for i := range namespaces.Items {
		names = append(names, namespaces.Items[i].Name)
	}
	return names, nil
}

// signal records an observation on every hub binding in a namespace that
// declares the named presence.
func (r *NetworkPresenceSyncReconciler) signal(ctx context.Context, namespace, presence, observed string) error {
	var bindings networkingv1alpha.NetworkBindingList
	if err := r.hub.List(ctx, &bindings, client.InNamespace(namespace)); err != nil {
		return fmt.Errorf("failed listing network bindings in %q: %w", namespace, err)
	}

	var errs []error
	for i := range bindings.Items {
		binding := &bindings.Items[i]
		if networkContextNameForBinding(binding) != presence {
			continue
		}
		errs = append(errs, r.mark(ctx, binding, observed))
	}
	return errors.Join(errs...)
}

// mark writes an observation onto one binding, and only when it differs from
// the one already there, so a resync of unchanged objects writes nothing.
func (r *NetworkPresenceSyncReconciler) mark(
	ctx context.Context,
	binding *networkingv1alpha.NetworkBinding,
	observed string,
) error {
	if !binding.DeletionTimestamp.IsZero() || binding.Annotations[NetworkPresenceObservedAnnotation] == observed {
		return nil
	}

	patch := client.MergeFrom(binding.DeepCopy())
	if binding.Annotations == nil {
		binding.Annotations = map[string]string{}
	}
	binding.Annotations[NetworkPresenceObservedAnnotation] = observed
	if err := r.hub.Patch(ctx, binding, patch); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed signalling network binding %q: %w", client.ObjectKeyFromObject(binding), err)
	}
	return nil
}

func hubNamespaceKey(project, projectNamespace string) string {
	return project + "/" + projectNamespace
}

// hubNamespaceProjectIndexFunc keys a hub namespace by the project namespace it
// stands for. A namespace that names only one of the two is not a hub namespace
// this controller can map anything onto.
func hubNamespaceProjectIndexFunc(obj client.Object) []string {
	namespace, ok := obj.(*corev1.Namespace)
	if !ok {
		return nil
	}
	project, err := projectFromNamespace(namespace)
	if err != nil {
		return nil
	}
	projectNamespace, err := projectNamespaceFromNamespace(namespace)
	if err != nil {
		return nil
	}
	return []string{hubNamespaceKey(project, projectNamespace)}
}

// SetupWithManager watches the project control planes on the multicluster
// manager and reads the hub through the manager the presence controller runs on,
// so both see the same namespaces.
//
// The local cluster is left out of the watches deliberately: the hub carries a
// replicated copy of every context under the same name, and reconciling those
// would map a presence onto itself.
//
// Why this is a separate controller, rather than the presence controller simply
// watching these types itself:
//
// The presence controller has to stay on the singleton manager. It is the only
// writer to a presence and to the statuses of every binding declaring it, and
// the sharded managers run in every replica with leader election disabled, so a
// controller registered there would have three replicas writing the same two
// objects. Only the multicluster manager engages project control planes, and
// they are discovered at runtime, so the presence controller cannot watch them
// where it lives: controller-runtime has no cross-manager watch, and a source
// added to a running controller can never be removed again, so engaging them by
// hand would leak an informer per project that ever disengages.
//
// Inverting it does not work either. Making the presence controller an
// mc-controller would give it the project-plane watches natively and let it
// watch the hub as a static source, but it would put it back on the sharded
// managers, which is the one thing it cannot afford.
//
// The two meet on the hub bindings instead, which the presence controller
// already watches, so the signal survives this controller being sharded across
// replicas the presence controller does not run in.
func (r *NetworkPresenceSyncReconciler) SetupWithManager(mgr mcmanager.Manager, hub manager.Manager) error {
	if r.Projects == nil {
		return errors.New("a project cluster resolver is required")
	}
	r.hub = hub.GetClient()

	if err := hub.GetFieldIndexer().IndexField(
		context.Background(), &corev1.Namespace{}, hubNamespaceProjectIndex, hubNamespaceProjectIndexFunc,
	); err != nil {
		return fmt.Errorf("failed adding hub namespace indexer %q: %w", hubNamespaceProjectIndex, err)
	}

	if err := mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkContext{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Named("networkpresencesync-networkcontext").
		Complete(mcreconcile.Func(r.reconcileNetworkContext)); err != nil {
		return err
	}

	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.Network{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Named("networkpresencesync-network").
		Complete(mcreconcile.Func(r.reconcileNetwork))
}
