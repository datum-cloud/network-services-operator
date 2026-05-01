// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
	mcsource "sigs.k8s.io/multicluster-runtime/pkg/source"

	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/iroh"
)

const (
	irohDNSFinalizer    = "networking.datumapis.com/iroh-dns-cleanup"
	irohDNSFieldManager = "network-services-operator/iroh-dns"

	// labels stamped on every DNSRecordSet we manage. Used both for
	// observability and (more importantly) so the downstream watch
	// can route an event back to the owning Connector across clusters.
	irohDNSManagedByLabelValue     = "networking.datumapis.com"
	irohDNSClaimedByUIDLabel       = "networking.datumapis.com/iroh-dns-claimed-by-uid"
	irohDNSConnectorClusterLabel   = "networking.datumapis.com/iroh-dns-connector-cluster"
	irohDNSConnectorNamespaceLabel = "networking.datumapis.com/iroh-dns-connector-namespace"
	irohDNSConnectorNameLabel      = "networking.datumapis.com/iroh-dns-connector-name"

	// Connector status condition surfacing whether *this* Connector is the
	// one publishing the iroh DNS record for its endpoint id.
	connectorConditionIrohDNSPublished = "IrohDNSPublished"

	connectorReasonIrohOwner           = "Owner"
	connectorReasonIrohDeferredToOwner = "DeferredToOwner"
	connectorReasonIrohPending         = "Pending"
)

// allowedIrohControllerNames is the set of ConnectorClass.spec.controllerName
// values for which we publish iroh DNS records. Both names refer to the same
// controller; "datum-connect" is the legacy name kept alive while older
// desktop builds churn out.
var allowedIrohControllerNames = map[string]struct{}{
	"networking.datumapis.com/datum-connect":    {},
	"networking.datumapis.com/iroh-quic-tunnel": {},
}

// IrohDNSReconciler watches Connectors backed by an iroh-routed
// ConnectorClass and maintains a single downstream DNSRecordSet per iroh
// endpoint id (z32-encoded public key) carrying the iroh DNS-discovery
// TXT records. Multiple Connectors that share the same iroh keypair (e.g.
// the same agent registered against two projects) collapse to one
// DNSRecordSet — the first to claim wins, and the loser surfaces a
// DeferredToOwner condition rather than fighting at the DNS layer.
type IrohDNSReconciler struct {
	mgr        mcmanager.Manager
	Config     config.NetworkServicesOperator
	Downstream cluster.Cluster
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectors,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectors/status,verbs=update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectors/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectorclasses,verbs=get;list;watch

func (r *IrohDNSReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx, "cluster", req.ClusterName)
	ctx = log.IntoContext(ctx, logger)

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	var connector networkingv1alpha1.Connector
	if err := cl.GetClient().Get(ctx, req.NamespacedName, &connector); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !connector.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, cl, &connector)
	}

	matches, err := r.classRoutesToIroh(ctx, cl, &connector)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !matches {
		// Class doesn't route here. If we previously held a claim, release it.
		return ctrl.Result{}, r.releaseIfOwner(ctx, &connector)
	}

	if !controllerutil.ContainsFinalizer(&connector, irohDNSFinalizer) {
		controllerutil.AddFinalizer(&connector, irohDNSFinalizer)
		if err := cl.GetClient().Update(ctx, &connector); err != nil {
			return ctrl.Result{}, fmt.Errorf("add finalizer: %w", err)
		}
		return ctrl.Result{}, nil
	}

	desired, ok, err := r.buildDesiredRecordSet(req.ClusterName, &connector)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ok {
		// Status not yet populated by the agent. If we previously claimed
		// this endpoint id, release so a sibling can take over.
		if err := r.releaseIfOwner(ctx, &connector); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, r.setPublishedCondition(ctx, cl, &connector, metav1.ConditionFalse, connectorReasonIrohPending, "Connector status does not yet carry connection details.")
	}

	return ctrl.Result{}, r.applyClaim(ctx, cl, &connector, desired)
}

// applyClaim implements the claim-then-write loop:
//   - Get the DNSRecordSet at the deterministic z32-derived name.
//   - Not found → Create with our claim. AlreadyExists means a sibling beat
//     us; we re-fetch and continue.
//   - Found, our claim → SSA refresh content.
//   - Found, foreign claim → defer (no write) and surface a status
//     condition naming the owner.
func (r *IrohDNSReconciler) applyClaim(ctx context.Context, cl cluster.Cluster, connector *networkingv1alpha1.Connector, desired *dnsv1alpha1.DNSRecordSet) error {
	key := client.ObjectKeyFromObject(desired)
	var existing dnsv1alpha1.DNSRecordSet
	err := r.Downstream.GetClient().Get(ctx, key, &existing)

	switch {
	case apierrors.IsNotFound(err):
		if err := r.Downstream.GetClient().Create(ctx, desired); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("create DNSRecordSet: %w", err)
			}
			// Sibling raced us. Refetch and fall through to the foreign-claim
			// branch on next reconcile.
			return nil
		}
		return r.setPublishedCondition(ctx, cl, connector, metav1.ConditionTrue, connectorReasonIrohOwner, "Owns iroh DNS record.")

	case err != nil:
		return fmt.Errorf("get DNSRecordSet: %w", err)
	}

	currentClaim := existing.Labels[irohDNSClaimedByUIDLabel]
	if currentClaim != string(connector.UID) {
		ownerCluster := decodeIrohClusterLabel(existing.Labels[irohDNSConnectorClusterLabel])
		ownerRef := ownerCluster + "/" + existing.Labels[irohDNSConnectorNamespaceLabel] + "/" + existing.Labels[irohDNSConnectorNameLabel]
		return r.setPublishedCondition(ctx, cl, connector, metav1.ConditionFalse, connectorReasonIrohDeferredToOwner,
			fmt.Sprintf("iroh DNS record is owned by Connector %s (uid %s).", ownerRef, currentClaim))
	}

	// We own it. SSA the desired content.
	if err := r.Downstream.GetClient().Patch(ctx, desired, client.Apply, client.FieldOwner(irohDNSFieldManager), client.ForceOwnership); err != nil {
		return fmt.Errorf("apply DNSRecordSet: %w", err)
	}
	return r.setPublishedCondition(ctx, cl, connector, metav1.ConditionTrue, connectorReasonIrohOwner, "Owns iroh DNS record.")
}

// handleDeletion releases the claim (if held) and removes the finalizer.
func (r *IrohDNSReconciler) handleDeletion(ctx context.Context, cl cluster.Cluster, connector *networkingv1alpha1.Connector) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(connector, irohDNSFinalizer) {
		return ctrl.Result{}, nil
	}
	if err := r.releaseIfOwner(ctx, connector); err != nil {
		return ctrl.Result{}, err
	}
	controllerutil.RemoveFinalizer(connector, irohDNSFinalizer)
	if err := cl.GetClient().Update(ctx, connector); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

// releaseIfOwner deletes the DNSRecordSet for this Connector's endpoint id
// only if we currently hold the claim. Otherwise it's a no-op (the foreign
// owner manages the record's lifecycle).
//
// We compute the DNSRecordSet name from the Connector's status — if the
// status is empty we have no z32 to derive, which means we never could
// have created a record in the first place, so there's nothing to release.
func (r *IrohDNSReconciler) releaseIfOwner(ctx context.Context, connector *networkingv1alpha1.Connector) error {
	z32, err := connectorEndpointZ32(connector)
	if err != nil || z32 == "" {
		return nil
	}
	key := client.ObjectKey{
		Namespace: r.Config.Connector.Iroh.DNSZoneRef.Namespace,
		Name:      irohDNSRecordSetName(z32),
	}
	var existing dnsv1alpha1.DNSRecordSet
	if err := r.Downstream.GetClient().Get(ctx, key, &existing); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get DNSRecordSet for release: %w", err)
	}
	if existing.Labels[irohDNSClaimedByUIDLabel] != string(connector.UID) {
		return nil
	}
	if err := r.Downstream.GetClient().Delete(ctx, &existing); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete DNSRecordSet: %w", err)
	}
	return nil
}

func (r *IrohDNSReconciler) classRoutesToIroh(ctx context.Context, cl cluster.Cluster, connector *networkingv1alpha1.Connector) (bool, error) {
	if connector.Spec.ConnectorClassName == "" {
		return false, nil
	}
	var class networkingv1alpha1.ConnectorClass
	if err := cl.GetClient().Get(ctx, client.ObjectKey{Name: connector.Spec.ConnectorClassName}, &class); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("get connectorclass: %w", err)
	}
	_, ok := allowedIrohControllerNames[class.Spec.ControllerName]
	return ok, nil
}

// irohDNSRecordSetName returns the deterministic DNSRecordSet name for an
// iroh endpoint id. One name per endpoint id means multiple Connectors
// reusing the same key collapse onto a single record.
func irohDNSRecordSetName(z32 string) string {
	return "iroh-" + z32
}

// encodeIrohClusterLabel mirrors the inline pattern in
// downstreamclient/mappednamespace.go: multicluster-runtime cluster
// names start with "/" (invalid as a k8s label value), so we map "/"
// to "_" and prefix "cluster-" to produce a label-safe form.
func encodeIrohClusterLabel(clusterName string) string {
	return "cluster-" + strings.ReplaceAll(clusterName, "/", "_")
}

func decodeIrohClusterLabel(label string) string {
	return strings.TrimPrefix(strings.ReplaceAll(label, "_", "/"), "cluster-")
}

func connectorEndpointZ32(connector *networkingv1alpha1.Connector) (string, error) {
	if connector.Status.ConnectionDetails == nil || connector.Status.ConnectionDetails.PublicKey == nil {
		return "", nil
	}
	pk := connector.Status.ConnectionDetails.PublicKey
	if pk.Id == "" {
		return "", nil
	}
	return iroh.EndpointHexToZ32(pk.Id)
}

// buildDesiredRecordSet builds the DNSRecordSet we want present in the
// downstream cluster. The second return value is false when the Connector
// status doesn't yet carry enough data to publish a useful record.
func (r *IrohDNSReconciler) buildDesiredRecordSet(clusterName string, connector *networkingv1alpha1.Connector) (*dnsv1alpha1.DNSRecordSet, bool, error) {
	z32, err := connectorEndpointZ32(connector)
	if err != nil {
		return nil, false, fmt.Errorf("encode endpoint id: %w", err)
	}
	if z32 == "" {
		return nil, false, nil
	}
	pk := connector.Status.ConnectionDetails.PublicKey
	if pk.HomeRelay == "" && len(pk.Addresses) == 0 {
		return nil, false, nil
	}

	cfg := r.Config.Connector.Iroh
	// RecordEntry.Name is relative to the DNSZone — dns-operator
	// qualifies it with the zone's spec.domainName at apply time. So
	// we never include the zone origin here; we only express the labels
	// that should sit between iroh's "_iroh" prefix and the zone root.
	recordName := cfg.RecordPrefix + "." + z32
	if cfg.RecordSuffix != "" {
		recordName = recordName + "." + cfg.RecordSuffix
	}
	ttl := int64(cfg.TTLSeconds)

	var entries []dnsv1alpha1.RecordEntry
	if pk.HomeRelay != "" {
		entries = append(entries, dnsv1alpha1.RecordEntry{
			Name: recordName,
			TTL:  &ttl,
			TXT:  &dnsv1alpha1.TXTRecordSpec{Content: "relay=" + pk.HomeRelay},
		})
	}
	// One TXT entry per direct address. iroh's parser expects every
	// IrohAttr::Addr value to be exactly one socket address — it calls
	// SocketAddr::from_str on the value as-is and silently drops failures
	// (iroh-relay-0.95.1/src/endpoint_info.rs:307-312). Joining multiple
	// addrs with whitespace into a single TXT line makes the whole line
	// fail to parse, so iroh sees no direct addresses.
	for _, a := range sortIrohAddresses(pk.Addresses) {
		entries = append(entries, dnsv1alpha1.RecordEntry{
			Name: recordName,
			TTL:  &ttl,
			TXT:  &dnsv1alpha1.TXTRecordSpec{Content: "addr=" + net.JoinHostPort(a.Address, strconv.Itoa(int(a.Port)))},
		})
	}

	drs := &dnsv1alpha1.DNSRecordSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: dnsv1alpha1.GroupVersion.String(),
			Kind:       "DNSRecordSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      irohDNSRecordSetName(z32),
			Namespace: cfg.DNSZoneRef.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": irohDNSManagedByLabelValue,
				irohDNSClaimedByUIDLabel:       string(connector.UID),
				irohDNSConnectorClusterLabel:   encodeIrohClusterLabel(clusterName),
				irohDNSConnectorNamespaceLabel: connector.Namespace,
				irohDNSConnectorNameLabel:      connector.Name,
			},
		},
		Spec: dnsv1alpha1.DNSRecordSetSpec{
			DNSZoneRef: corev1.LocalObjectReference{Name: cfg.DNSZoneRef.Name},
			RecordType: dnsv1alpha1.RRTypeTXT,
			Records:    entries,
		},
	}
	return drs, true, nil
}

// sortIrohAddresses returns a deterministically-ordered copy of the
// input — by (address, port) lexicographic. The agent's
// iroh::Endpoint::endpoint_addr().ip_addrs() iterator is iter-over-set
// and not order-stable, so sorting here means the same set of
// endpoints produces the same DNS content across heartbeats and SSA
// stays a no-op when nothing actually changed.
func sortIrohAddresses(addrs []networkingv1alpha1.PublicKeyConnectorAddress) []networkingv1alpha1.PublicKeyConnectorAddress {
	sorted := slices.Clone(addrs)
	slices.SortFunc(sorted, func(a, b networkingv1alpha1.PublicKeyConnectorAddress) int {
		return cmp.Or(
			cmp.Compare(a.Address, b.Address),
			cmp.Compare(a.Port, b.Port),
		)
	})
	return sorted
}

func (r *IrohDNSReconciler) setPublishedCondition(ctx context.Context, cl cluster.Cluster, connector *networkingv1alpha1.Connector, status metav1.ConditionStatus, reason, message string) error {
	cond := metav1.Condition{
		Type:               connectorConditionIrohDNSPublished,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: connector.Generation,
	}
	if !apimeta.SetStatusCondition(&connector.Status.Conditions, cond) {
		return nil
	}
	if err := cl.GetClient().Status().Update(ctx, connector); err != nil {
		return fmt.Errorf("update connector status: %w", err)
	}
	return nil
}

// SetupWithManager wires the reconciler. Watches:
//
//   - Connector (For) and ConnectorClass (Watches) — the primary multicluster
//     event sources.
//
//   - Lease (Watches with EnqueueRequestForOwner) — agent heartbeats renew the
//     Connector's Lease on every interval; that update fires our reconcile
//     even when the Connector itself hasn't changed. This is the load-bearing
//     trigger for sibling handover: when an owner Connector is deleted and
//     its DNSRecordSet is GC'd, every sibling's next lease renewal drives its
//     reconcile, and one of them wins the Create race for the now-empty z32.
//     Bound on handover ≈ leaseDurationSeconds.
//
//   - DNSRecordSet on the downstream cluster — drift detection. Mapper
//     enqueues the *current owner* Connector identified by the labels on the
//     DNSRecordSet, catching cases like a manual external delete of the
//     record. Sibling handover does NOT flow through this watch:
//     multicluster-runtime's manager exposes GetCluster(name) but no
//     enumeration, so a downstream event can't fan out to siblings across
//     project clusters. That's the Lease watch's job.
func (r *IrohDNSReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

	downstreamSource := mcsource.Kind(
		&dnsv1alpha1.DNSRecordSet{},
		func(_ string, _ cluster.Cluster) handler.TypedEventHandler[*dnsv1alpha1.DNSRecordSet, mcreconcile.Request] {
			return handler.TypedEnqueueRequestsFromMapFunc(func(_ context.Context, drs *dnsv1alpha1.DNSRecordSet) []mcreconcile.Request {
				name := drs.Labels[irohDNSConnectorNameLabel]
				ns := drs.Labels[irohDNSConnectorNamespaceLabel]
				if name == "" || ns == "" {
					return nil
				}
				clusterName := decodeIrohClusterLabel(drs.Labels[irohDNSConnectorClusterLabel])
				return []mcreconcile.Request{{
					ClusterName: clusterName,
					Request:     ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: name}},
				}}
			})
		},
	)
	downstreamClusterSource, _, _ := downstreamSource.ForCluster("", r.Downstream)

	return mcbuilder.ControllerManagedBy(mgr).
		For(&networkingv1alpha1.Connector{}).
		Watches(
			&networkingv1alpha1.ConnectorClass{},
			func(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
				return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
					logger := log.FromContext(ctx)
					class, ok := obj.(*networkingv1alpha1.ConnectorClass)
					if !ok {
						return nil
					}
					var connectors networkingv1alpha1.ConnectorList
					if err := cl.GetClient().List(ctx, &connectors); err != nil {
						logger.Error(err, "list Connectors for ConnectorClass watch", "connectorClass", class.Name)
						return nil
					}
					var requests []mcreconcile.Request
					for i := range connectors.Items {
						c := &connectors.Items[i]
						if c.Spec.ConnectorClassName != class.Name {
							continue
						}
						requests = append(requests, mcreconcile.Request{
							ClusterName: clusterName,
							Request:     ctrl.Request{NamespacedName: client.ObjectKeyFromObject(c)},
						})
					}
					return requests
				})
			},
		).
		Watches(
			&coordinationv1.Lease{},
			mchandler.EnqueueRequestForOwner(&networkingv1alpha1.Connector{}, handler.OnlyControllerOwner()),
		).
		WatchesRawSource(downstreamClusterSource).
		Named("iroh-dns").
		Complete(r)
}
