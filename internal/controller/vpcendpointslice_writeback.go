// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"time"

	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

const (
	// VPCEndpointSliceProjectionLabel marks a copy this write-back published.
	// Only a slice carrying it is ever written or removed here.
	VPCEndpointSliceProjectionLabel = "networking.datumapis.com/vpc-endpointslice-projection"

	// VPCEndpointSliceSourceNameAnnotation preserves the name the cell's slice
	// carried, which the copy cannot keep. It is how the sweep finds the
	// source of a copy it is looking at.
	//
	// An annotation rather than a label because a pod name is a DNS subdomain
	// and runs to 253 characters, while a label value stops at 63. The sweep
	// selects on the projection and location labels and reads this off the
	// object it already has, so nothing needs it to be selectable.
	VPCEndpointSliceSourceNameAnnotation = "networking.datumapis.com/vpc-endpointslice-source-name"

	// karmadaManagedLabel marks an object federation placed on a cluster. A
	// slice carrying it was propagated here, not published here.
	karmadaManagedLabel = "karmada.io/managed"
)

// vpcEndpointSliceSweepInterval paces the collection of copies whose source
// went while the cell could not see the hub. Nothing replays a deletion.
const vpcEndpointSliceSweepInterval = 10 * time.Minute

// vpcEndpointSliceResyncInterval paces the pass that acts on a change only the
// hub saw. A pod put behind a proxy, or taken out from behind one, moves a
// record on the hub and nothing in the cell. Without a pass of its own, a pod
// would wait for an unrelated local event before the edge could reach it.
const vpcEndpointSliceResyncInterval = time.Minute

// VPCEndpointSliceWriteBackReconciler publishes the per-pod EndpointSlices
// galactic-cni writes in a cell to the federation hub, so the propagation
// policy already selecting EndpointSlices carries them to every gateway
// cluster. An edge needs them to reach a VPC pod at all: galactic-vrf reads
// the pod's address and its SRv6 SID off the slice and installs the egress
// route Envoy's traffic is encapsulated onto.
//
// The copy is transport, not interpretation. Address type, endpoints,
// conditions, ports and both galactic annotations cross unread and unaltered.
// Nothing here parses the SID — normalising it would break the contract
// galactic-vrf reads it under, silently.
type VPCEndpointSliceWriteBackReconciler struct {
	Location config.LocationConfig

	// HubCluster is the federation hub the copies are written to.
	HubCluster cluster.Cluster

	mgr         mcmanager.Manager
	localReader client.Reader
}

// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=edgereachabilities,verbs=get;list;watch

func (r *VPCEndpointSliceWriteBackReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, r.publish(ctx, cl.GetClient(), req.NamespacedName)
}

func (r *VPCEndpointSliceWriteBackReconciler) publish(
	ctx context.Context,
	cl client.Reader,
	key client.ObjectKey,
) error {
	logger := log.FromContext(ctx)
	hub := r.HubCluster.GetClient()

	location, err := r.location(ctx)
	if err != nil {
		return err
	}
	if location == "" {
		return nil
	}

	var slice discoveryv1.EndpointSlice
	if err := cl.Get(ctx, key, &slice); err != nil {
		// A read that failed for any other reason says nothing about whether
		// the slice is still there. Withdrawing reachability on a stalled
		// apiserver would black-hole a pod that never moved.
		if apierrors.IsNotFound(err) {
			return r.collect(ctx, hub, location, key)
		}
		return err
	}

	// A slice on its way out has already stopped describing a live pod.
	if !slice.DeletionTimestamp.IsZero() || !isVPCPodEndpointSlice(&slice) {
		return r.collect(ctx, hub, location, key)
	}

	// A copy that came back down from the hub must never be published again.
	// This cell is also a gateway cluster, so the propagation policy returns
	// everything this write-back sends up; re-publishing it would loop.
	if isVPCEndpointSliceCopy(&slice) {
		return nil
	}

	// galactic writes exactly one endpoint per slice. None means the slice
	// describes no address to reach, and an empty slice is not something to
	// publish. Leave whatever is already up alone rather than withdraw a
	// route over what is most likely a partial write.
	if len(slice.Endpoints) == 0 {
		logger.Info("not publishing a vpc endpointslice that carries no endpoint",
			"namespace", slice.Namespace, jsonKeyName, slice.Name)
		return nil
	}

	// A pod nothing serves through a proxy is a pod no edge has to reach, and
	// carrying it puts one tenant's addresses on every other tenant's edge.
	//
	// Silence is not a withdrawal. A namespace the control plane has not
	// answered for keeps whatever it already reaches: a route pulled out from
	// under a pod that is still serving black-holes live traffic, while a route
	// left up for a pod nothing sends to costs a table entry.
	reachable, recorded, err := r.reachability(ctx, hub, slice.Namespace)
	if err != nil {
		return err
	}
	if recorded && !servesReachableAddress(&slice, reachable) {
		return r.collect(ctx, hub, location, key)
	}

	// A copy exists to be federated onward. A slice whose namespace names no
	// project cannot be routed and would leave an object nothing collects.
	routing, err := resolveProjectRouting(ctx, cl, slice.Namespace)
	if err != nil {
		var unresolvable *projectUnresolvable
		if errors.As(err, &unresolvable) {
			logger.Info("not publishing a vpc endpointslice whose namespace names no project",
				"namespace", slice.Namespace, jsonKeyName, slice.Name)
			return nil
		}
		return err
	}

	desired := federatedEndpointSlice(&slice, location, map[string]string{
		downstreamclient.UpstreamOwnerClusterNameLabel: routing.clusterNameLabel,
		downstreamclient.UpstreamOwnerNamespaceLabel:   routing.projectNamespace,
	})

	if err := writeFederatedEndpointSlice(ctx, hub, desired); err != nil {
		// The hub namespace is made by the federation that carries a
		// project's work to this cell. Nothing here should invent one.
		if apierrors.IsNotFound(err) {
			logger.Info("the hub has no namespace to publish this vpc endpointslice into yet",
				"namespace", slice.Namespace)
			return nil
		}
		return err
	}

	return nil
}

// reachability reads the control plane's answer for a namespace: which of the
// project's workload addresses are behind a proxy. The second return says
// whether an answer exists at all, which no set of addresses can express.
func (r *VPCEndpointSliceWriteBackReconciler) reachability(
	ctx context.Context,
	hub client.Reader,
	namespace string,
) (map[string]struct{}, bool, error) {
	var record networkingv1alpha.EdgeReachability
	key := client.ObjectKey{Namespace: namespace, Name: networkingv1alpha.EdgeReachabilityName}
	if err := hub.Get(ctx, key, &record); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed reading edge reachability for %q: %w", namespace, err)
	}

	addresses := make(map[string]struct{}, len(record.Spec.Addresses))
	for _, address := range record.Spec.Addresses {
		addresses[address] = struct{}{}
	}
	return addresses, true, nil
}

// servesReachableAddress reports whether the slice describes an address the
// control plane says something serves.
func servesReachableAddress(slice *discoveryv1.EndpointSlice, reachable map[string]struct{}) bool {
	for _, endpoint := range slice.Endpoints {
		for _, address := range endpoint.Addresses {
			if _, ok := reachable[address]; ok {
				return true
			}
		}
	}
	return false
}

// isVPCPodEndpointSlice reports whether a slice is one galactic-cni published
// for a VPC pod.
func isVPCPodEndpointSlice(slice *discoveryv1.EndpointSlice) bool {
	_, ok := slice.Labels[VPCPodTenantIDLabel]
	return ok
}

// isVPCEndpointSliceCopy reports whether a slice is a published copy rather
// than a cell's own object. Both marks are checked: a copy carries this
// operator's projection label, and one that has been through federation also
// carries Karmada's.
func isVPCEndpointSliceCopy(slice *discoveryv1.EndpointSlice) bool {
	if slice.Labels[VPCEndpointSliceProjectionLabel] == "true" {
		return true
	}
	return slice.Labels[karmadaManagedLabel] == "true"
}

// federatedEndpointSliceName names a copy so that it cannot land on top of a
// slice galactic published. The cell this copy came from is also a gateway
// cluster, so the propagation policy delivers the copy straight back into the
// namespace the original sits in; sharing the original's name there would hand
// the original to federation, which overwrites it and then resurrects it after
// galactic deletes it with the pod.
//
// The location is what makes the name unique rather than merely different: two
// cells can hold pods of the same name in the same project namespace, and both
// publish into one hub namespace.
func federatedEndpointSliceName(location, sourceName string) string {
	name := fmt.Sprintf("vpc-%s-%s", location, sourceName)
	if len(name) <= maxObjectNameLength {
		return name
	}

	sum := sha256.Sum256([]byte(location + "/" + sourceName))
	suffix := "-" + hex.EncodeToString(sum[:])[:16]
	return name[:maxObjectNameLength-len(suffix)] + suffix
}

// federatedEndpointSlice builds the copy of a cell's slice. Everything the
// edge reads crosses untouched: address type, endpoints and their conditions,
// ports, and the galactic annotations carrying the tenant and the SRv6 SID.
//
// Owner references are deliberately dropped. The original is owned by its Pod,
// which does not exist on the hub, and a copy carrying that reference would be
// collected the moment the hub's garbage collector looked at it.
func federatedEndpointSlice(
	source *discoveryv1.EndpointSlice,
	location string,
	routingLabels map[string]string,
) *discoveryv1.EndpointSlice {
	labels := map[string]string{}
	maps.Copy(labels, source.Labels)
	maps.Copy(labels, routingLabels)
	labels[VPCEndpointSliceProjectionLabel] = "true"
	labels[networkingv1alpha.NetworkInterfaceLocationLabel] = location

	annotations := map[string]string{}
	maps.Copy(annotations, source.Annotations)
	annotations[VPCEndpointSliceSourceNameAnnotation] = source.Name

	carried := source.DeepCopy()

	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:        federatedEndpointSliceName(location, source.Name),
			Namespace:   source.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		AddressType: source.AddressType,
		Endpoints:   carried.Endpoints,
		Ports:       carried.Ports,
	}
}

// writeFederatedEndpointSlice converges a copy onto what the source says. The
// copy is overwritten in full on every pass, so an edit to it never survives
// and never reaches the source.
func writeFederatedEndpointSlice(
	ctx context.Context,
	cl client.Client,
	desired *discoveryv1.EndpointSlice,
) error {
	copied := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace},
	}

	// AddressType is immutable. A source that changed families cannot be
	// converged onto an existing copy, and leaving the old one in place would
	// wedge this slice for as long as the pod lived.
	if err := cl.Get(ctx, client.ObjectKeyFromObject(copied), copied); err == nil {
		if copied.AddressType != desired.AddressType {
			if err := cl.Delete(ctx, copied); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed removing a vpc endpointslice copy of another family: %w", err)
			}
			copied = &discoveryv1.EndpointSlice{
				ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace},
			}
		}
	} else if !apierrors.IsNotFound(err) {
		return err
	}

	if _, err := controllerutil.CreateOrUpdate(ctx, cl, copied, func() error {
		copied.Labels = desired.Labels
		copied.Annotations = desired.Annotations
		copied.AddressType = desired.AddressType
		copied.Endpoints = desired.Endpoints
		copied.Ports = desired.Ports
		return nil
	}); err != nil {
		return fmt.Errorf("failed writing the vpc endpointslice copy: %w", err)
	}

	return nil
}

// collect removes a copy, and only ever a copy.
func (r *VPCEndpointSliceWriteBackReconciler) collect(
	ctx context.Context,
	cl client.Client,
	location string,
	source client.ObjectKey,
) error {
	key := client.ObjectKey{
		Namespace: source.Namespace,
		Name:      federatedEndpointSliceName(location, source.Name),
	}

	var copied discoveryv1.EndpointSlice
	if err := cl.Get(ctx, key, &copied); err != nil {
		return client.IgnoreNotFound(err)
	}

	if copied.Labels[VPCEndpointSliceProjectionLabel] != "true" {
		return nil
	}

	return client.IgnoreNotFound(cl.Delete(ctx, &copied))
}

// sweep compares every copy this location published against the slices the
// cell still holds, and collects the ones with nothing behind them. It closes
// the gap a missed deletion opens: the cell unreachable when a pod went, or
// the process restarting across the event.
//
// A source this sweep cannot read is left alone. Absence of an answer is not
// absence of a pod.
func (r *VPCEndpointSliceWriteBackReconciler) sweep(ctx context.Context) error {
	logger := log.FromContext(ctx)

	location, err := r.location(ctx)
	if err != nil {
		return err
	}
	if location == "" {
		return nil
	}

	var published discoveryv1.EndpointSliceList
	if err := r.HubCluster.GetClient().List(ctx, &published, client.MatchingLabels{
		VPCEndpointSliceProjectionLabel:                 "true",
		networkingv1alpha.NetworkInterfaceLocationLabel: location,
	}); err != nil {
		return fmt.Errorf("failed listing published vpc endpointslices: %w", err)
	}

	var errs []error
	for i := range published.Items {
		copied := &published.Items[i]

		sourceName := copied.Annotations[VPCEndpointSliceSourceNameAnnotation]
		if sourceName == "" {
			errs = append(errs, fmt.Errorf(
				"vpc endpointslice copy %s/%s names no slice it was published from",
				copied.Namespace, copied.Name))
			continue
		}

		key := client.ObjectKey{Namespace: copied.Namespace, Name: sourceName}

		var slice discoveryv1.EndpointSlice
		err := r.localReader.Get(ctx, key, &slice)
		if err == nil && slice.DeletionTimestamp.IsZero() && isVPCPodEndpointSlice(&slice) {
			continue
		}
		if err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, err)
			continue
		}

		logger.Info("collecting a published vpc endpointslice with no slice behind it",
			"namespace", key.Namespace, jsonKeyName, key.Name)
		if err := r.collect(ctx, r.HubCluster.GetClient(), location, key); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// resync republishes every vpc slice the cell holds, so a reachability record
// that has changed on the hub is acted on without waiting for the pod to change.
// publish decides both directions, so this both restores a copy that was
// withheld and removes one that is no longer served.
func (r *VPCEndpointSliceWriteBackReconciler) resync(ctx context.Context) error {
	var held discoveryv1.EndpointSliceList
	if err := r.localReader.List(ctx, &held, client.HasLabels{VPCPodTenantIDLabel}); err != nil {
		return fmt.Errorf("failed listing vpc endpointslices: %w", err)
	}

	var errs []error
	for i := range held.Items {
		slice := &held.Items[i]
		if isVPCEndpointSliceCopy(slice) {
			continue
		}
		if err := r.publish(ctx, r.localReader, client.ObjectKeyFromObject(slice)); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (r *VPCEndpointSliceWriteBackReconciler) location(ctx context.Context) (string, error) {
	identity, err := ResolveLocationIdentity(ctx, r.localReader, r.Location)
	if err != nil {
		var unresolved *LocationUnresolved
		if errors.As(err, &unresolved) {
			return "", nil
		}
		return "", err
	}
	return identity.Reference.Name, nil
}

// Start runs the sweep on a timer for as long as the manager runs.
func (r *VPCEndpointSliceWriteBackReconciler) Start(ctx context.Context) error {
	sweeps := time.NewTicker(vpcEndpointSliceSweepInterval)
	defer sweeps.Stop()

	resyncs := time.NewTicker(vpcEndpointSliceResyncInterval)
	defer resyncs.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sweeps.C:
			if err := r.sweep(ctx); err != nil {
				log.FromContext(ctx).Error(err, "failed sweeping published vpc endpointslices")
			}
		case <-resyncs.C:
			if err := r.resync(ctx); err != nil {
				log.FromContext(ctx).Error(err, "failed resyncing published vpc endpointslices")
			}
		}
	}
}

// vpcPodEndpointSlicePredicate passes the slices this write-back carries. An
// update is passed when either side carries the tenant label, so that a slice
// which loses it still reaches Reconcile and has its copy collected.
func vpcPodEndpointSlicePredicate() predicate.Predicate {
	carries := func(obj client.Object) bool {
		slice, ok := obj.(*discoveryv1.EndpointSlice)
		return ok && isVPCPodEndpointSlice(slice)
	}

	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return carries(e.Object) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return carries(e.Object) },
		GenericFunc: func(e event.GenericEvent) bool { return carries(e.Object) },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return carries(e.ObjectOld) || carries(e.ObjectNew)
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *VPCEndpointSliceWriteBackReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	if r.HubCluster == nil {
		return errors.New("a federation hub cluster is required")
	}

	r.mgr = mgr
	r.localReader = mgr.GetLocalManager().GetClient()

	if err := mgr.GetLocalManager().Add(r); err != nil {
		return fmt.Errorf("unable to add the vpc endpointslice sweep: %w", err)
	}

	return mcbuilder.ControllerManagedBy(mgr).
		For(
			&discoveryv1.EndpointSlice{},
			mcbuilder.WithEngageWithLocalCluster(false),
			mcbuilder.WithPredicates(vpcPodEndpointSlicePredicate()),
		).
		Named("vpcendpointslice_writeback").
		Complete(r)
}
