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

// presenceEventBufferSize is how many presences may be waiting to be picked up
// by the channel source.
//
// It does not need to be large. source.Channel runs a goroutine that drains this
// channel continuously into a destination buffer of its own, so this only has to
// cover the moment between a send and that goroutine being scheduled.
const presenceEventBufferSize = 64

// NewNetworkPresenceEvents builds the channel the sync controller writes and the
// presence controller reads.
func NewNetworkPresenceEvents() chan event.GenericEvent {
	return make(chan event.GenericEvent, presenceEventBufferSize)
}

// NetworkPresenceSyncReconciler brings a presence back to the controller that
// owns it when something it is derived from changes.
//
// NetworkPresenceReconciler is level-triggered on the hub and reads a Network, a
// projected Location and a NetworkContext that all live in a project control plane
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

// enqueue hands one presence to the presence controller.
//
// The handover never blocks. Nothing drains this channel unless the presence
// controller is running in this process, and under cluster sharding it may not
// be: the sharded managers run every replica, while the presence controller runs
// on the singleton manager in whichever replica holds its lease. Blocking would
// stall this controller's workers behind a reader that is never going to arrive.
// Dropping leaves the presence to the refusal retry instead, which is worse but
// bounded. See the note on SetupWithManager.
func (r *NetworkPresenceSyncReconciler) enqueue(ctx context.Context, namespace, name string) {
	presence := &networkingv1alpha.NetworkContext{}
	presence.Namespace = namespace
	presence.Name = name

	select {
	case r.Events <- event.GenericEvent{Object: presence}:
	default:
		log.FromContext(ctx).Info("network presence sync event dropped; nothing is reading the channel",
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
//
// Why this is a separate controller feeding a channel, rather than the presence
// controller simply watching these types itself:
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
// The cost of the split is that producer and consumer are only in the same
// process while cluster sharding is off, which is the default and is how every
// configuration in this repository runs. Turning sharding on would shard this
// controller across replicas while the presence controller stayed in one of
// them, and the events raised in the others would be dropped rather than
// delivered. That needs a trigger that goes through the API server rather than a
// channel, and it is not solved here.
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
