// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// networkPresenceFinalizer holds a project-plane Network open until the hub
// objects derived from it are gone. The network controller's own finalizer
// covers project-plane contexts and is untouched by this one.
const networkPresenceFinalizer = "networking.datumapis.com/network-presence"

// networkPresenceCollectRetry paces the wait for hub objects that are deleting
// but not yet gone.
const networkPresenceCollectRetry = 5 * time.Second

// NetworkPresenceGCReconciler is the presence controller's half in the project
// control planes. A hub NetworkContext cannot be owned by the Network it
// projects, being in another cluster, so the apiserver would collect nothing and
// a deleted network would orphan every hub object and every propagated copy
// derived from it.
//
// It runs on the multicluster manager because that is what engages project
// control planes, and it writes the hub through the presence controller's
// client so every write to a presence goes through one place.
type NetworkPresenceGCReconciler struct {
	Presence *NetworkPresenceReconciler
	Projects ProjectClusterResolver
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networks,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networks/finalizers,verbs=update

func (r *NetworkPresenceGCReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	projectClient, err := r.Projects.ClientForProject(ctx, string(req.ClusterName))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed reaching project %q: %w", req.ClusterName, err)
	}

	var network networkingv1alpha.Network
	if err := projectClient.Get(ctx, req.NamespacedName, &network); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !network.DeletionTimestamp.IsZero() {
		return r.collect(ctx, projectClient, &network)
	}

	if controllerutil.AddFinalizer(&network, networkPresenceFinalizer) {
		if err := projectClient.Update(ctx, &network); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed adding the network presence finalizer: %w", err)
		}
	}

	return ctrl.Result{}, r.converge(ctx, &network)
}

// converge re-projects the network onto every hub presence carrying it. Nothing
// else would ever rewrite those contexts, so without this an edited network
// never reaches the locations already carrying it.
func (r *NetworkPresenceGCReconciler) converge(ctx context.Context, network *networkingv1alpha.Network) error {
	contexts, err := r.hubContexts(ctx, network.UID)
	if err != nil {
		return err
	}

	var errs []error
	for i := range contexts {
		request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&contexts[i])}
		if _, err := r.Presence.Reconcile(ctx, request); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// collect deletes every hub object carrying the network's UID and releases the
// finalizer once none remain. Deleting a hub context deletes the propagated copy
// with it.
func (r *NetworkPresenceGCReconciler) collect(
	ctx context.Context,
	projectClient client.Client,
	network *networkingv1alpha.Network,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(network, networkPresenceFinalizer) {
		return ctrl.Result{}, nil
	}

	remaining, err := r.deleteHubObjects(ctx, network.UID)
	if err != nil {
		return ctrl.Result{}, err
	}

	if remaining > 0 {
		logger.Info("network presence objects are still terminating", "remaining", remaining)
		return ctrl.Result{RequeueAfter: networkPresenceCollectRetry}, nil
	}

	if controllerutil.RemoveFinalizer(network, networkPresenceFinalizer) {
		if err := projectClient.Update(ctx, network); err != nil {
			if apierrors.IsNotFound(err) {
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed removing the network presence finalizer: %w", err)
		}
	}

	return ctrl.Result{}, nil
}

func (r *NetworkPresenceGCReconciler) deleteHubObjects(ctx context.Context, networkUID types.UID) (int, error) {
	objects, err := r.hubObjects(ctx, networkUID)
	if err != nil {
		return 0, err
	}

	for _, object := range objects {
		if !object.GetDeletionTimestamp().IsZero() {
			continue
		}
		log.FromContext(ctx).Info("deleting a hub object of a deleted network",
			"namespace", object.GetNamespace(), "name", object.GetName())
		if err := r.Presence.hub.Delete(ctx, object); client.IgnoreNotFound(err) != nil {
			return 0, fmt.Errorf("failed deleting hub object %q: %w", object.GetName(), err)
		}
	}

	remaining, err := r.hubObjects(ctx, networkUID)
	if err != nil {
		return 0, err
	}
	return len(remaining), nil
}

// hubObjects lists the bindings before the contexts, so a binding never
// out-lives the presence it declares.
func (r *NetworkPresenceGCReconciler) hubObjects(ctx context.Context, networkUID types.UID) ([]client.Object, error) {
	var bindings networkingv1alpha.NetworkBindingList
	if err := r.Presence.hub.List(ctx, &bindings, hubNetworkUIDSelector(networkUID)); err != nil {
		return nil, fmt.Errorf("failed listing hub network bindings: %w", err)
	}

	contexts, err := r.hubContexts(ctx, networkUID)
	if err != nil {
		return nil, err
	}

	objects := make([]client.Object, 0, len(bindings.Items)+len(contexts))
	for i := range bindings.Items {
		objects = append(objects, &bindings.Items[i])
	}
	for i := range contexts {
		objects = append(objects, &contexts[i])
	}
	return objects, nil
}

func (r *NetworkPresenceGCReconciler) hubContexts(
	ctx context.Context,
	networkUID types.UID,
) ([]networkingv1alpha.NetworkContext, error) {
	var contexts networkingv1alpha.NetworkContextList
	if err := r.Presence.hub.List(ctx, &contexts, hubNetworkUIDSelector(networkUID)); err != nil {
		return nil, fmt.Errorf("failed listing hub network contexts: %w", err)
	}
	return contexts.Items, nil
}

// hubNetworkUIDSelector keys on the UID rather than the name. A network deleted
// and recreated under the same name is a different network with a different
// address space, and its predecessor's presences must not be adopted.
func hubNetworkUIDSelector(networkUID types.UID) client.MatchingLabels {
	return client.MatchingLabels{networkingv1alpha.NetworkUIDLabel: string(networkUID)}
}

// SetupWithManager registers the controller against the project control planes
// the multicluster manager engages. The presence controller must be set up
// first: this shares its hub client.
func (r *NetworkPresenceGCReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	if r.Presence == nil || r.Presence.hub == nil {
		return errors.New("a network presence reconciler set up against the hub is required")
	}
	if r.Projects == nil {
		r.Projects = NewProjectClusterResolver(mgr)
	}

	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha.Network{}, mcbuilder.WithEngageWithLocalCluster(false)).
		Named("networkpresencegc").
		Complete(r)
}
