// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"
)

// retryAfterConflict is the duration to wait before re-queuing a reconcile
// request after an optimistic-locking conflict (HTTP 409) from the API server.
const retryAfterConflict = 1 * time.Second

// Labels and annotations applied to DNSRecordSet resources managed by this controller.
const (
	labelManagedBy        = "app.kubernetes.io/managed-by"
	labelManagedByValue   = "networking.datumapis.com"
	labelDNSManaged       = "dns.datumapis.com/managed"
	labelDNSSourceKind    = "dns.datumapis.com/source-kind"
	labelDNSSourceName    = "dns.datumapis.com/source-name"
	labelDNSSourceNS      = "dns.datumapis.com/source-namespace"
	annotationDNSHostname = "dns.datumapis.com/hostname"
	annotationSyncStart   = "dns.datumapis.com/sync-started-at"
)

// +kubebuilder:rbac:groups=dns.networking.miloapis.com,resources=dnsrecordsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dns.networking.miloapis.com,resources=dnsrecordsets/status,verbs=get
// +kubebuilder:rbac:groups=dns.networking.miloapis.com,resources=dnszones,verbs=get;list;watch

// ensureDNSRecordSets creates or updates DNSRecordSet resources for all claimed
// hostnames on the upstream gateway whose apex domains are verified via Datum
// DNS. Hostnames whose domains are not managed by Datum DNS are skipped and
// marked NotApplicable. It returns per-hostname DNS status entries that reflect
// the result of each DNS programming attempt, and a Result that signals whether
// the caller should requeue.
func (r *GatewayReconciler) ensureDNSRecordSets(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	claimedHostnames []string,
) (hostnameStatuses []networkingv1alpha.HostnameStatus, result Result) {
	if !r.Config.Gateway.EnableDNSIntegration {
		return nil, result
	}

	logger := log.FromContext(ctx)
	logger.Info("ensuring DNS record sets", "claimed_hostname_count", len(claimedHostnames))

	canonicalHostname := r.Config.Gateway.GatewayDNSAddress(upstreamGateway)

	// List all Domains in the gateway namespace once; we query them per hostname below.
	var domainList networkingv1alpha.DomainList
	if err := upstreamClient.List(ctx, &domainList, client.InNamespace(upstreamGateway.Namespace)); err != nil {
		result.Err = fmt.Errorf("failed listing domains: %w", err)
		return nil, result
	}

	// Build a set of desired DNSRecordSet names so we can garbage-collect stale ones.
	desiredRecordSetNames := map[string]bool{}

	for _, hostname := range claimedHostnames {
		// Skip the platform-managed canonical hostname – it is handled by external-dns.
		if hostname == canonicalHostname {
			continue
		}

		hs := networkingv1alpha.HostnameStatus{Hostname: hostname}

		// Get all possible zone names from most specific to least specific.
		zoneNames := possibleZoneNames(hostname)
		if len(zoneNames) == 0 {
			logger.Info("skipping hostname with fewer than two domain labels", "hostname", hostname)
			apimeta.SetStatusCondition(&hs.Conditions, metav1.Condition{
				Type:               networkingv1alpha.HostnameConditionDNSRecordProgrammed,
				Status:             metav1.ConditionTrue,
				Reason:             networkingv1alpha.DNSRecordReasonNotApplicable,
				Message:            "Hostname does not have a resolvable domain",
				ObservedGeneration: upstreamGateway.Generation,
			})
			hostnameStatuses = append(hostnameStatuses, hs)
			continue
		}

		// Find the most specific Domain + DNSZone combination.
		var domain *networkingv1alpha.Domain
		var dnsZone *dnsv1alpha1.DNSZone
		var matchedZoneName string

		for _, zoneName := range zoneNames {
			// Check if a Domain exists for this zone name.
			d, found := findDomainByName(domainList.Items, zoneName)
			if !found {
				continue
			}

			// Check if the Domain has VerifiedDNSZone=True.
			if !apimeta.IsStatusConditionTrue(d.Status.Conditions, networkingv1alpha.DomainConditionVerifiedDNSZone) {
				// Domain exists but isn't verified; record this for potential error message
				// but keep looking for a more specific verified domain.
				continue
			}

			// Look up the DNSZone for this domain.
			var dnsZoneList dnsv1alpha1.DNSZoneList
			if err := upstreamClient.List(ctx, &dnsZoneList,
				client.InNamespace(upstreamGateway.Namespace),
				client.MatchingFields{dnsZoneDomainNameIndex: zoneName},
			); err != nil {
				result.Err = fmt.Errorf("failed listing DNSZones for domain %q: %w", zoneName, err)
				return nil, result
			}

			if len(dnsZoneList.Items) == 0 {
				continue
			}

			// Found a matching Domain + DNSZone.
			domain = &d
			dnsZone = &dnsZoneList.Items[0]
			matchedZoneName = zoneName
			break
		}

		// If no matching Domain + DNSZone found, check why and set appropriate status.
		if domain == nil || dnsZone == nil {
			// Try to provide a helpful message by checking what we found.
			var unverifiedDomain *networkingv1alpha.Domain
			for _, zoneName := range zoneNames {
				d, found := findDomainByName(domainList.Items, zoneName)
				if found {
					if !apimeta.IsStatusConditionTrue(d.Status.Conditions, networkingv1alpha.DomainConditionVerifiedDNSZone) {
						unverifiedDomain = &d
						break
					}
				}
			}

			if unverifiedDomain != nil {
				apimeta.SetStatusCondition(&hs.Conditions, metav1.Condition{
					Type:               networkingv1alpha.HostnameConditionDNSRecordProgrammed,
					Status:             metav1.ConditionFalse,
					Reason:             networkingv1alpha.DNSRecordReasonDomainNotVerified,
					Message:            fmt.Sprintf("Domain %q has not been verified via a DNSZone (VerifiedDNSZone condition is not True)", unverifiedDomain.Name),
					ObservedGeneration: upstreamGateway.Generation,
				})
			} else {
				apimeta.SetStatusCondition(&hs.Conditions, metav1.Condition{
					Type:               networkingv1alpha.HostnameConditionDNSRecordProgrammed,
					Status:             metav1.ConditionTrue,
					Reason:             networkingv1alpha.DNSRecordReasonNotApplicable,
					Message:            fmt.Sprintf("No Domain or DNSZone found for hostname %q", hostname),
					ObservedGeneration: upstreamGateway.Generation,
				})
			}
			hostnameStatuses = append(hostnameStatuses, hs)
			continue
		}

		logger.Info("found matching zone for hostname",
			"hostname", hostname,
			"zone", matchedZoneName,
			"domain", domain.Name,
		)

		// Determine the record type based on whether this hostname is an apex domain.
		rrType := dnsv1alpha1.RRTypeCNAME
		if domain.Status.Apex {
			rrType = dnsv1alpha1.RRTypeALIAS
		}

		recordSetName := dnsRecordSetName(upstreamGateway.Name, hostname)
		desiredRecordSetNames[recordSetName] = true

		// Conflict detection: list existing DNSRecordSets with the same
		// hostname annotation in this namespace that reference this zone.
		var existingList dnsv1alpha1.DNSRecordSetList
		if err := upstreamClient.List(ctx, &existingList,
			client.InNamespace(upstreamGateway.Namespace),
			client.MatchingLabels{labelDNSManaged: "true"},
		); err != nil {
			result.Err = fmt.Errorf("failed listing existing DNSRecordSets: %w", err)
			return nil, result
		}

		for _, existing := range existingList.Items {
			if existing.Name == recordSetName {
				// This is our own record; skip conflict check.
				continue
			}
			if existing.Annotations[annotationDNSHostname] == hostname &&
				existing.Spec.DNSZoneRef.Name == dnsZone.Name &&
				existing.Labels[labelManagedBy] != labelManagedByValue {
				// Conflict: a record for this hostname exists that we don't own.
				conflictMsg := fmt.Sprintf(
					"Existing DNSRecordSet %q for hostname %q is managed by %q",
					existing.Name, hostname, existing.Labels[labelManagedBy],
				)
				logger.Info("DNS record conflict detected",
					"hostname", hostname,
					"conflicting_record", existing.Name,
					"managed_by", existing.Labels[labelManagedBy],
				)
				apimeta.SetStatusCondition(&hs.Conditions, metav1.Condition{
					Type:               networkingv1alpha.HostnameConditionDNSRecordProgrammed,
					Status:             metav1.ConditionFalse,
					Reason:             networkingv1alpha.DNSRecordReasonConflict,
					Message:            conflictMsg,
					ObservedGeneration: upstreamGateway.Generation,
				})
				hostnameStatuses = append(hostnameStatuses, hs)
				goto nextHostname
			}
		}

		{
			desired := buildDesiredDNSRecordSet(upstreamGateway, recordSetName)

			operationResult, err := controllerutil.CreateOrUpdate(ctx, upstreamClient, desired, func() error {
				// If the record already exists and is managed by us, update the spec.
				// If it is managed by someone else, return an error so the caller can
				// surface a conflict condition.
				existingManagedBy := desired.Labels[labelManagedBy]
				if existingManagedBy != "" && existingManagedBy != labelManagedByValue {
					return fmt.Errorf("conflict: existing DNSRecordSet %q is managed by %q", desired.Name, existingManagedBy)
				}

				// Ensure owner reference is always set.
				if err := controllerutil.SetControllerReference(upstreamGateway, desired, upstreamClient.Scheme()); err != nil {
					return fmt.Errorf("failed to set owner reference on DNSRecordSet: %w", err)
				}

				// Ensure labels and annotations are set on both create and update.
				if desired.Labels == nil {
					desired.Labels = map[string]string{}
				}
				desired.Labels[labelManagedBy] = labelManagedByValue
				desired.Labels[labelDNSManaged] = "true"
				desired.Labels[labelDNSSourceKind] = "Gateway"
				desired.Labels[labelDNSSourceName] = upstreamGateway.Name
				desired.Labels[labelDNSSourceNS] = upstreamGateway.Namespace

				if desired.Annotations == nil {
					desired.Annotations = map[string]string{}
				}
				desired.Annotations[annotationDNSHostname] = hostname

				// Only write sync-started-at on creation (when creationTimestamp is zero).
				if desired.CreationTimestamp.IsZero() {
					desired.Annotations[annotationSyncStart] = metav1.Now().UTC().Format("2006-01-02T15:04:05Z")
				}

				desired.Spec = buildDesiredDNSRecordSetSpec(hostname, canonicalHostname, *dnsZone, rrType)
				return nil
			})
			if err != nil {
				if apierrors.IsConflict(err) {
					result.RequeueAfter = retryAfterConflict
					return hostnameStatuses, result
				}
				logger.Error(err, "failed to create or update DNSRecordSet",
					"hostname", hostname,
					"record_set_name", recordSetName,
				)
				apimeta.SetStatusCondition(&hs.Conditions, metav1.Condition{
					Type:               networkingv1alpha.HostnameConditionDNSRecordProgrammed,
					Status:             metav1.ConditionFalse,
					Reason:             networkingv1alpha.DNSRecordReasonFailed,
					Message:            fmt.Sprintf("Failed to create or update DNSRecordSet: %v", err),
					ObservedGeneration: upstreamGateway.Generation,
				})
				hostnameStatuses = append(hostnameStatuses, hs)
				continue
			}

			reason := networkingv1alpha.DNSRecordReasonCreated
			if operationResult == controllerutil.OperationResultUpdated {
				reason = networkingv1alpha.DNSRecordReasonUpdated
			}

			logger.Info("DNS record set processed",
				"hostname", hostname,
				"zone", dnsZone.Name,
				"record_type", string(rrType),
				"canonical_hostname", canonicalHostname,
				"operation_result", operationResult,
			)

			apimeta.SetStatusCondition(&hs.Conditions, metav1.Condition{
				Type:   networkingv1alpha.HostnameConditionDNSRecordProgrammed,
				Status: metav1.ConditionTrue,
				Reason: reason,
				Message: fmt.Sprintf("%s record %s in DNSZone %q",
					strings.ToLower(string(rrType)),
					operationResultVerb(operationResult),
					dnsZone.Name,
				),
				ObservedGeneration: upstreamGateway.Generation,
			})
		}

		hostnameStatuses = append(hostnameStatuses, hs)
	nextHostname:
	}

	// Garbage-collect stale DNSRecordSets that are no longer needed.
	gcResult := r.garbageCollectDNSRecordSets(ctx, upstreamClient, upstreamGateway, desiredRecordSetNames)
	if gcResult.ShouldReturn() {
		return hostnameStatuses, gcResult.Merge(result)
	}

	return hostnameStatuses, result
}

// reconcileDNSStatus computes and applies the aggregate DNSRecordsProgrammed
// condition on the upstream gateway from the per-hostname statuses produced by
// ensureDNSRecordSets. Hostnames marked NotApplicable are excluded from the
// count. The condition is removed entirely when no hostnames require
// Datum-managed DNS records.
func (r *GatewayReconciler) reconcileDNSStatus(
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	hostnameStatuses []networkingv1alpha.HostnameStatus,
) (result Result) {
	needed, programmed := 0, 0

	for _, hs := range hostnameStatuses {
		c := apimeta.FindStatusCondition(hs.Conditions, networkingv1alpha.HostnameConditionDNSRecordProgrammed)
		if c == nil {
			continue
		}
		if c.Reason == networkingv1alpha.DNSRecordReasonNotApplicable {
			continue
		}
		needed++
		if c.Status == metav1.ConditionTrue {
			programmed++
		}
	}

	switch {
	case needed == 0:
		apimeta.RemoveStatusCondition(&upstreamGateway.Status.Conditions, networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed)
	case programmed == needed:
		apimeta.SetStatusCondition(&upstreamGateway.Status.Conditions, metav1.Condition{
			Type:               networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed,
			Status:             metav1.ConditionTrue,
			Reason:             networkingv1alpha.DNSRecordsProgrammedReasonAllCreated,
			Message:            fmt.Sprintf("%d/%d hostnames have DNS records programmed", programmed, needed),
			ObservedGeneration: upstreamGateway.Generation,
		})
	default:
		apimeta.SetStatusCondition(&upstreamGateway.Status.Conditions, metav1.Condition{
			Type:               networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed,
			Status:             metav1.ConditionFalse,
			Reason:             networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure,
			Message:            fmt.Sprintf("%d/%d hostnames have DNS records programmed; see per-hostname conditions for details", programmed, needed),
			ObservedGeneration: upstreamGateway.Generation,
		})
	}

	result.AddStatusUpdate(upstreamClient, upstreamGateway)
	return result
}

// garbageCollectDNSRecordSets deletes DNSRecordSet resources that were
// previously created for the gateway but are no longer needed because the
// corresponding hostname was removed from the gateway listeners. Only records
// whose names are absent from desiredNames are deleted.
func (r *GatewayReconciler) garbageCollectDNSRecordSets(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	desiredNames map[string]bool,
) (result Result) {
	logger := log.FromContext(ctx)

	var existingList dnsv1alpha1.DNSRecordSetList
	if err := upstreamClient.List(ctx, &existingList,
		client.InNamespace(upstreamGateway.Namespace),
		client.MatchingLabels{
			labelDNSManaged:    "true",
			labelManagedBy:     labelManagedByValue,
			labelDNSSourceName: upstreamGateway.Name,
			labelDNSSourceNS:   upstreamGateway.Namespace,
		},
	); err != nil {
		result.Err = fmt.Errorf("failed listing DNSRecordSets for GC: %w", err)
		return result
	}

	for _, rs := range existingList.Items {
		if desiredNames[rs.Name] {
			continue
		}
		hostname := rs.Annotations[annotationDNSHostname]
		logger.Info("deleting stale DNSRecordSet",
			"name", rs.Name,
			"hostname", hostname,
		)
		if err := upstreamClient.Delete(ctx, &rs); err != nil && !apierrors.IsNotFound(err) {
			result.Err = fmt.Errorf("failed to delete stale DNSRecordSet %q: %w", rs.Name, err)
			return result
		}
	}

	return result
}

// dnsRecordSetName produces the deterministic DNSRecordSet name for a given
// gateway name and hostname.
//
// Format: {gateway-name}-{first-8-hex-chars-of-sha256(hostname)}
func dnsRecordSetName(gatewayName, hostname string) string {
	h := sha256.Sum256([]byte(hostname))
	return fmt.Sprintf("%s-%s", gatewayName, hex.EncodeToString(h[:])[:8])
}

// possibleZoneNames returns all possible zone names for a hostname, ordered
// from most specific to least specific. For example, "v1.api.example.com"
// returns ["api.example.com", "example.com"]. For apex domains like
// "example.com", returns ["example.com"] to support ALIAS records at the apex.
// Returns nil when the hostname has fewer than two labels.
func possibleZoneNames(hostname string) []string {
	parts := strings.Split(hostname, ".")
	if len(parts) < 2 {
		return nil
	}

	var zones []string

	// Start from the second level (skip the first label which is the record name)
	// and work down to the minimum of 2 labels (apex domain).
	for i := 1; i <= len(parts)-2; i++ {
		zone := strings.Join(parts[i:], ".")
		zones = append(zones, zone)
	}

	// For apex domains (exactly 2 labels like "example.com"), the hostname
	// itself is the zone. This enables ALIAS records at the zone apex.
	if len(parts) == 2 {
		zones = append(zones, hostname)
	}

	return zones
}

// findDomainByName returns the Domain whose spec.domainName equals name, or
// the Domain whose spec.domainName is a suffix of hostname (for subdomains).
func findDomainByName(domains []networkingv1alpha.Domain, apexDomain string) (networkingv1alpha.Domain, bool) {
	for _, d := range domains {
		if d.Spec.DomainName == apexDomain {
			return d, true
		}
	}
	return networkingv1alpha.Domain{}, false
}

// buildDesiredDNSRecordSet constructs a DNSRecordSet shell with only Name and
// Namespace populated. Labels, Annotations, and Spec are intentionally omitted
// here and are applied inside the CreateOrUpdate mutate function so they are
// set consistently on both create and update operations.
func buildDesiredDNSRecordSet(
	upstreamGateway *gatewayv1.Gateway,
	name string,
) *dnsv1alpha1.DNSRecordSet {
	return &dnsv1alpha1.DNSRecordSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: upstreamGateway.Namespace,
			Name:      name,
		},
	}
}

// buildDesiredDNSRecordSetSpec constructs the DNSRecordSetSpec that points
// hostname at canonicalHostname. Both values are normalized to absolute FQDNs
// (trailing dot) before being written into the record entry. The record type
// is CNAME for non-apex hostnames and ALIAS for apex domains, as determined
// by the caller.
func buildDesiredDNSRecordSetSpec(
	hostname, canonicalHostname string,
	dnsZone dnsv1alpha1.DNSZone,
	rrType dnsv1alpha1.RRType,
) dnsv1alpha1.DNSRecordSetSpec {
	fqdnHostname := hostname
	if !strings.HasSuffix(fqdnHostname, ".") {
		fqdnHostname = fqdnHostname + "."
	}

	fqdnTarget := canonicalHostname
	if !strings.HasSuffix(fqdnTarget, ".") {
		fqdnTarget = fqdnTarget + "."
	}

	var entry dnsv1alpha1.RecordEntry
	entry.Name = fqdnHostname
	entry.TTL = ptr.To(int64(300))

	switch rrType {
	case dnsv1alpha1.RRTypeCNAME:
		entry.CNAME = &dnsv1alpha1.CNAMERecordSpec{Content: fqdnTarget}
	case dnsv1alpha1.RRTypeALIAS:
		entry.ALIAS = &dnsv1alpha1.ALIASRecordSpec{Content: fqdnTarget}
	}

	return dnsv1alpha1.DNSRecordSetSpec{
		DNSZoneRef: corev1.LocalObjectReference{Name: dnsZone.Name},
		RecordType: rrType,
		Records:    []dnsv1alpha1.RecordEntry{entry},
	}
}

// operationResultVerb converts a controllerutil.OperationResult to a past-tense
// verb suitable for use in a status condition message.
func operationResultVerb(result controllerutil.OperationResult) string {
	switch result {
	case controllerutil.OperationResultCreated:
		return "created"
	case controllerutil.OperationResultUpdated:
		return "updated"
	default:
		return "unchanged"
	}
}

// listGatewaysForDNSZoneFunc returns a TypedEventHandler that enqueues every
// Gateway in the same namespace whenever a DNSZone changes. This ensures the
// controller re-evaluates DNS record status when a zone becomes ready or is
// deleted.
func (r *GatewayReconciler) listGatewaysForDNSZoneFunc(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
	return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
		dnsZone := obj.(*dnsv1alpha1.DNSZone)
		logger := log.FromContext(ctx)

		var gatewayList gatewayv1.GatewayList
		if err := cl.GetClient().List(ctx, &gatewayList, client.InNamespace(dnsZone.Namespace)); err != nil {
			logger.Error(err, "failed to list Gateways for DNSZone change")
			return nil
		}

		var requests []mcreconcile.Request
		for _, gw := range gatewayList.Items {
			requests = append(requests, mcreconcile.Request{
				ClusterName: clusterName,
				Request: reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(&gw),
				},
			})
		}
		return requests
	})
}

// listGatewaysForDNSRecordSetFunc returns a TypedEventHandler that enqueues
// the owning Gateway whenever a DNSRecordSet changes. The owning gateway is
// identified via the dns.datumapis.com/source-name and
// dns.datumapis.com/source-namespace labels written by this controller.
func (r *GatewayReconciler) listGatewaysForDNSRecordSetFunc(clusterName string, _ cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
	return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []mcreconcile.Request {
		rs := obj.(*dnsv1alpha1.DNSRecordSet)
		logger := log.FromContext(ctx)

		gatewayName := rs.Labels[labelDNSSourceName]
		gatewayNS := rs.Labels[labelDNSSourceNS]
		if gatewayName == "" || gatewayNS == "" {
			return nil
		}

		logger.Info("enqueueing Gateway for DNSRecordSet change",
			"gateway_name", gatewayName,
			"gateway_namespace", gatewayNS,
		)

		return []mcreconcile.Request{
			{
				ClusterName: clusterName,
				Request: reconcile.Request{
					NamespacedName: client.ObjectKey{
						Namespace: gatewayNS,
						Name:      gatewayName,
					},
				},
			},
		}
	})
}
