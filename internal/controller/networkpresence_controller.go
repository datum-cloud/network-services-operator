// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
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
// location) triple, for as long as any NetworkBinding declares the network is
// needed there.
//
// The hub decides a presence exists and the project control plane holds the
// object: declarations arrive on the hub, and the context lives beside the
// Network it is derived from, owned by it, where the reconciler that allocates
// the location's subnet already runs.
type NetworkPresenceReconciler struct {
	Projects ProjectClusterResolver

	// Events carries presences that something in a project control plane says
	// need looking at again. NetworkPresenceSyncReconciler is the only writer;
	// this controller stays the only writer to the presence itself.
	Events <-chan event.GenericEvent

	// UnclaimedGracePeriod is how long a presence nothing declares any more is
	// kept before it is torn down. A location keeps the address space it was
	// given for this long after the last consumer goes, so a redeploy neither
	// loses the network there nor changes the prefix it is addressed from.
	//
	// Zero tears the presence down on the first observation that nothing
	// declares it.
	UnclaimedGracePeriod time.Duration

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
		return r.teardown(ctx, req)
	}

	logger.Info("reconciling network presence", "holders", len(holders))
	defer logger.Info("reconcile complete")

	refused, err := r.ensure(ctx, req, holders)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Nothing here watches a LocationBinding, a project namespace, a network
	// that does not exist yet, or the context in the project control plane, so a
	// refusal for a condition that later clears has no other way back.
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

// teardown removes a presence nothing declares any more. It targets the project
// control plane, which is where this controller writes: the hub carries a
// replicated copy under the same name and the same labels, and reaping that copy
// would take the network's rules away from every cell reading them.
//
// At the location a local finalizer holds the propagated copy while addresses
// are still held, so this is prompt in the ordinary case and blocks where it
// must.
//
// Nothing declaring the presence is not on its own enough to remove it. A
// workload being replaced stops declaring it for a few seconds, and the removal
// waits out that gap before it commits.
func (r *NetworkPresenceReconciler) teardown(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	routing, err := resolveProjectRouting(ctx, r.hub, req.Namespace)
	if err != nil {
		var unresolvable *projectUnresolvable
		if errors.As(err, &unresolvable) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	projectClient, err := r.Projects.ClientForProject(ctx, routing.project)
	if err != nil {
		if errors.Is(err, multicluster.ErrClusterNotFound) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed reaching project %q: %w", routing.project, err)
	}

	var networkContext networkingv1alpha.NetworkContext
	key := client.ObjectKey{Namespace: routing.projectNamespace, Name: req.Name}
	if err := projectClient.Get(ctx, key, &networkContext); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !networkContext.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Only a presence this controller wrote is this controller's to remove. It
	// stamps the network UID on everything it creates, so a context without one
	// was put here by something else — the binding controller that predates this
	// one, or a context an operator wrote by hand — and deleting it would take
	// away the object a location reads.
	if networkContext.Labels[networkingv1alpha.NetworkUIDLabel] == "" {
		return ctrl.Result{}, nil
	}

	remaining, err := r.grace(ctx, projectClient, &networkContext)
	if err != nil {
		return ctrl.Result{}, err
	}
	if remaining > 0 {
		return ctrl.Result{RequeueAfter: remaining}, nil
	}

	log.FromContext(ctx).Info("no binding declares this network presence, removing it",
		"project", routing.project, "namespace", key.Namespace, "name", key.Name)
	if err := projectClient.Delete(ctx, &networkContext); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

// grace reports how much longer a presence nothing declares is kept. The instant
// the last declaration went away is recorded on the object rather than held in
// memory, so a restart or a change of leader does not restart the wait, and does
// not skip it either.
func (r *NetworkPresenceReconciler) grace(
	ctx context.Context,
	projectClient client.Client,
	networkContext *networkingv1alpha.NetworkContext,
) (time.Duration, error) {
	if r.UnclaimedGracePeriod <= 0 {
		return 0, nil
	}

	// An unparseable stamp is treated as no stamp: the wait restarts rather than
	// a presence being torn down on the strength of a value nothing can read.
	if stamp := networkContext.Annotations[networkingv1alpha.NetworkContextUnclaimedSinceAnnotation]; stamp != "" {
		if since, err := time.Parse(time.RFC3339, stamp); err == nil {
			remaining := r.UnclaimedGracePeriod - time.Since(since)
			if remaining < 0 {
				remaining = 0
			}
			return remaining, nil
		}
		log.FromContext(ctx).Info("network presence carries an unreadable unclaimed-since stamp, restarting the wait",
			"stamp", stamp)
	}

	patch := client.MergeFrom(networkContext.DeepCopy())
	if networkContext.Annotations == nil {
		networkContext.Annotations = map[string]string{}
	}
	networkContext.Annotations[networkingv1alpha.NetworkContextUnclaimedSinceAnnotation] =
		time.Now().UTC().Format(time.RFC3339)

	if err := projectClient.Patch(ctx, networkContext, patch); err != nil {
		return 0, fmt.Errorf("failed recording when network presence %q went unclaimed: %w",
			networkContext.Name, err)
	}
	return r.UnclaimedGracePeriod, nil
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

	networkContext, err := r.project(ctx, req, projectClient, routing, &pair, &network)
	if err != nil {
		// A context being deleted is not a context. Adopting it would hand every
		// consumer a reference to an object that is about to go, and the
		// finalizers still running on it would take the location's address space
		// with them.
		var terminating *networkContextTerminating
		if errors.As(err, &terminating) {
			return true, r.report(ctx, holders, nil, refusal(
				networkingv1alpha.NetworkBindingReasonNetworkContextTerminating, terminating.Error()))
		}
		return false, err
	}

	ref := &networkingv1alpha.NetworkContextRef{
		Namespace: networkContext.Namespace,
		Name:      networkContext.Name,
	}

	// The context is in a control plane this controller does not watch, so its
	// becoming ready is not an event here. Without a way back the binding would
	// report NotReady for as long as it exists.
	if !apimeta.IsStatusConditionTrue(networkContext.Status.Conditions, networkingv1alpha.NetworkContextReady) {
		return true, r.report(ctx, holders, ref, refusal(
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

// project writes the network's rules into the context, in the project control
// plane, owned by the network. The apiserver collects it when the network goes,
// and the replicator carries it to the hub and on to the cells. Everything a
// location reads is in spec, because propagation carries nothing else.
func (r *NetworkPresenceReconciler) project(
	ctx context.Context,
	req ctrl.Request,
	projectClient client.Client,
	routing projectRouting,
	pair *networkingv1alpha.NetworkBindingSpec,
	network *networkingv1alpha.Network,
) (*networkingv1alpha.NetworkContext, error) {
	networkContext := &networkingv1alpha.NetworkContext{}
	networkContext.Namespace = routing.projectNamespace
	networkContext.Name = req.Name

	_, err := controllerutil.CreateOrUpdate(ctx, projectClient, networkContext, func() error {
		// The guard is here rather than only in the caller so the invariant does
		// not depend on the order anything else happens in: CreateOrUpdate reads
		// the object immediately before this runs, which is the last moment the
		// answer can still be current.
		if !networkContext.DeletionTimestamp.IsZero() {
			return &networkContextTerminating{name: networkContext.Name}
		}

		// The presence is declared again, so whatever wait was running is over.
		delete(networkContext.Annotations, networkingv1alpha.NetworkContextUnclaimedSinceAnnotation)

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
		networkContext.Spec.NetworkGeneration = network.Generation

		return controllerutil.SetControllerReference(network, networkContext, projectClient.Scheme())
	})
	if err != nil {
		var terminating *networkContextTerminating
		if errors.As(err, &terminating) {
			return nil, err
		}
		return nil, fmt.Errorf("failed writing network context %q: %w", req.Name, err)
	}

	return networkContext, nil
}

// networkContextTerminating says the presence for this pair still exists and is
// being deleted.
type networkContextTerminating struct {
	name string
}

func (e *networkContextTerminating) Error() string {
	return fmt.Sprintf("Network context %q is being deleted, so the network is not present in this location", e.name)
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
		// The reference is the current answer, not the first one: a refusal that
		// means there is no context clears it, so a consumer is never pointed at
		// an object that has gone.
		if !equality.Semantic.DeepEqual(binding.Status.NetworkContextRef, ref) {
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

	builder := ctrl.NewControllerManagedBy(mgr).
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
		Named("networkpresence")

	// Everything this controller reads besides the binding lives in a project
	// control plane it does not watch. The sync controller does watch them, and
	// maps what it sees onto the presence the change is about.
	if r.Events != nil {
		builder = builder.WatchesRawSource(source.Channel(r.Events, &handler.EnqueueRequestForObject{}))
	}

	return builder.Complete(r)
}
