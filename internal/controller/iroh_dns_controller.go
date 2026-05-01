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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
	"go.datum.net/network-services-operator/internal/iroh"
)

const (
	irohDNSFinalizer    = "networking.datumapis.com/iroh-dns-cleanup"
	irohDNSFieldManager = "network-services-operator/iroh-dns"
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
// ConnectorClass and maintains a 1-1 DNSRecordSet in the configured
// downstream cluster carrying the iroh-format TXT record at
// "<recordPrefix>.<z32-endpoint-id>.<baseDomain>".
type IrohDNSReconciler struct {
	mgr        mcmanager.Manager
	Config     config.NetworkServicesOperator
	Downstream client.Client
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=connectors,verbs=get;list;watch;update;patch
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

	dnsName := r.dnsRecordSetName(&connector)

	if !connector.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&connector, irohDNSFinalizer) {
			return ctrl.Result{}, nil
		}
		if err := r.deleteRecordSet(ctx, dnsName); err != nil {
			return ctrl.Result{}, err
		}
		controllerutil.RemoveFinalizer(&connector, irohDNSFinalizer)
		if err := cl.GetClient().Update(ctx, &connector); err != nil {
			return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
		}
		return ctrl.Result{}, nil
	}

	matches, err := r.classRoutesToIroh(ctx, cl, &connector)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !matches {
		// Class doesn't route here. If we previously owned a record (finalizer
		// present), tear it down and release the Connector.
		if controllerutil.ContainsFinalizer(&connector, irohDNSFinalizer) {
			if err := r.deleteRecordSet(ctx, dnsName); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&connector, irohDNSFinalizer)
			if err := cl.GetClient().Update(ctx, &connector); err != nil {
				return ctrl.Result{}, fmt.Errorf("release finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(&connector, irohDNSFinalizer) {
		controllerutil.AddFinalizer(&connector, irohDNSFinalizer)
		if err := cl.GetClient().Update(ctx, &connector); err != nil {
			return ctrl.Result{}, fmt.Errorf("add finalizer: %w", err)
		}
		// Requeue via the inevitable update event; nothing more to do this pass.
		return ctrl.Result{}, nil
	}

	desired, ok, err := r.buildDesiredRecordSet(&connector)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !ok {
		// Status not yet populated by the agent. Tear down any prior record so
		// stale entries don't linger; reconcile fires again on status update.
		if err := r.deleteRecordSet(ctx, dnsName); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if err := r.applyRecordSet(ctx, desired); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
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

func (r *IrohDNSReconciler) dnsRecordSetName(connector *networkingv1alpha1.Connector) client.ObjectKey {
	return client.ObjectKey{
		Namespace: r.Config.Connector.Iroh.DNSZoneRef.Namespace,
		Name:      "iroh-" + string(connector.UID),
	}
}

// buildDesiredRecordSet builds the DNSRecordSet we want present in the
// downstream cluster. The second return value is false when the Connector
// status doesn't yet carry enough data to publish a useful record (no
// endpoint id at all, or neither relay nor any addresses).
func (r *IrohDNSReconciler) buildDesiredRecordSet(connector *networkingv1alpha1.Connector) (*dnsv1alpha1.DNSRecordSet, bool, error) {
	if connector.Status.ConnectionDetails == nil || connector.Status.ConnectionDetails.PublicKey == nil {
		return nil, false, nil
	}
	pk := connector.Status.ConnectionDetails.PublicKey
	if pk.Id == "" {
		return nil, false, nil
	}
	if pk.HomeRelay == "" && len(pk.Addresses) == 0 {
		return nil, false, nil
	}

	z32, err := iroh.EndpointHexToZ32(pk.Id)
	if err != nil {
		return nil, false, fmt.Errorf("encode endpoint id: %w", err)
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
	key := r.dnsRecordSetName(connector)

	var entries []dnsv1alpha1.RecordEntry
	if pk.HomeRelay != "" {
		entries = append(entries, dnsv1alpha1.RecordEntry{
			Name: recordName,
			TTL:  &ttl,
			TXT:  &dnsv1alpha1.TXTRecordSpec{Content: "relay=" + pk.HomeRelay},
		})
	}
	if len(pk.Addresses) > 0 {
		entries = append(entries, dnsv1alpha1.RecordEntry{
			Name: recordName,
			TTL:  &ttl,
			TXT:  &dnsv1alpha1.TXTRecordSpec{Content: "addr=" + joinIrohAddresses(pk.Addresses)},
		})
	}

	drs := &dnsv1alpha1.DNSRecordSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: dnsv1alpha1.GroupVersion.String(),
			Kind:       "DNSRecordSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      key.Name,
			Namespace: key.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":                 "network-services-operator",
				"networking.datumapis.com/connector-uid":       string(connector.UID),
				"networking.datumapis.com/connector-namespace": connector.Namespace,
				"networking.datumapis.com/connector-name":      connector.Name,
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

// joinIrohAddresses formats a list of (address, port) tuples for the
// `addr=` TXT value iroh expects: socket addresses separated by single
// spaces, IPv6 addresses in bracketed form (RFC 3986). The input is
// sorted by (address, port) before joining so that an agent reporting
// the same set of endpoints in different orders across heartbeats —
// iroh-base's iter-over-set is not stable — produces the same TXT
// content and avoids spurious server-side-apply writes.
func joinIrohAddresses(addrs []networkingv1alpha1.PublicKeyConnectorAddress) string {
	sorted := slices.Clone(addrs)
	slices.SortFunc(sorted, func(a, b networkingv1alpha1.PublicKeyConnectorAddress) int {
		return cmp.Or(
			cmp.Compare(a.Address, b.Address),
			cmp.Compare(a.Port, b.Port),
		)
	})
	parts := make([]string, 0, len(sorted))
	for _, a := range sorted {
		parts = append(parts, net.JoinHostPort(a.Address, strconv.Itoa(int(a.Port))))
	}
	return strings.Join(parts, " ")
}

func (r *IrohDNSReconciler) applyRecordSet(ctx context.Context, desired *dnsv1alpha1.DNSRecordSet) error {
	if err := r.Downstream.Patch(ctx, desired, client.Apply, client.FieldOwner(irohDNSFieldManager), client.ForceOwnership); err != nil {
		return fmt.Errorf("apply DNSRecordSet: %w", err)
	}
	return nil
}

func (r *IrohDNSReconciler) deleteRecordSet(ctx context.Context, key client.ObjectKey) error {
	drs := &dnsv1alpha1.DNSRecordSet{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}}
	if err := r.Downstream.Delete(ctx, drs); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete DNSRecordSet: %w", err)
	}
	return nil
}

// SetupWithManager wires the reconciler into the multicluster manager. Watches
// the upstream Connector and ConnectorClass; the downstream DNSRecordSet
// client is held directly on the reconciler since it lives in a different
// cluster than the manager.
func (r *IrohDNSReconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.mgr = mgr

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
		Named("iroh-dns").
		Complete(r)
}
