// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
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

// presenceEventBufferSize is how many presences may be waiting to be handed to
// the presence controller's workqueue. The queue coalesces by key, so the buffer
// only has to absorb a burst; a full one makes the sync controller wait rather
// than drop the event.
const presenceEventBufferSize = 1024

// NewNetworkPresenceEvents builds the channel the sync controller writes and the
// presence controller reads.
func NewNetworkPresenceEvents() chan event.GenericEvent {
	return make(chan event.GenericEvent, presenceEventBufferSize)
}

// NetworkPresenceSyncReconciler brings a presence back to the controller that
// owns it when something it is derived from changes.
//
// NetworkPresenceReconciler is level-triggered on the hub and reads a Network, a
// LocationBinding and a NetworkContext that all live in a project control plane
// it does not watch. A network edited after a location already carries it, or a
// context that finished being deleted, are both invisible there.
//
// This controller watches those control planes and does nothing else: it maps
// what it sees onto the presence the change is about and hands the key over. It
// writes no object, holds no finalizer, deletes nothing and reports no status,
// so the presence keeps exactly one writer.
type NetworkPresenceSyncReconciler struct {
	// Events is the channel the presence controller consumes.
	Events chan<- event.GenericEvent

	hub client.Client
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networks,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkcontexts,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkbindings,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

// reconcileNetworkContext maps a context in a project control plane onto the
// presences the hub holds for it. A context lives in the project that named it
// and in that project's own namespace, and its name is the presence's name, so
// the hub namespaces standing for that pair are the whole answer.
//
// The object is never read. The event that matters most is the context
// finishing being deleted, and there is nothing left to read by then; the
// request names everything the mapping needs.
func (r *NetworkPresenceSyncReconciler) reconcileNetworkContext(
	ctx context.Context,
	req mcreconcile.Request,
) (ctrl.Result, error) {
	namespaces, err := r.hubNamespaces(ctx, string(req.ClusterName), req.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	for _, namespace := range namespaces {
		r.enqueue(ctx, namespace, req.Name)
	}
	return ctrl.Result{}, nil
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
	if err != nil {
		return ctrl.Result{}, err
	}

	for _, namespace := range namespaces {
		var bindings networkingv1alpha.NetworkBindingList
		if err := r.hub.List(ctx, &bindings, client.InNamespace(namespace)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed listing network bindings in %q: %w", namespace, err)
		}

		seen := map[string]struct{}{}
		for i := range bindings.Items {
			binding := &bindings.Items[i]
			if binding.Spec.Network.Name != req.Name {
				continue
			}
			name := networkContextNameForBinding(binding)
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			r.enqueue(ctx, namespace, name)
		}
	}
	return ctrl.Result{}, nil
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

// enqueue hands one presence to the presence controller. The handover blocks
// rather than drops: a missed event is a presence that stays wrong until
// something else touches it.
func (r *NetworkPresenceSyncReconciler) enqueue(ctx context.Context, namespace, name string) {
	presence := &networkingv1alpha.NetworkContext{}
	presence.Namespace = namespace
	presence.Name = name

	select {
	case r.Events <- event.GenericEvent{Object: presence}:
	case <-ctx.Done():
		log.FromContext(ctx).Info("dropped a network presence sync event while shutting down",
			"namespace", namespace, "name", name)
	}
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
func (r *NetworkPresenceSyncReconciler) SetupWithManager(mgr mcmanager.Manager, hub manager.Manager) error {
	if r.Events == nil {
		return errors.New("a network presence event channel is required")
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
