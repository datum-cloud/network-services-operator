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
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	dnsutil "go.datum.net/network-services-operator/internal/util/dns"
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

	// annotationDNSAuthorityMissingSince records, on a kept record set, the first
	// check that found no Datum DNS authority for its hostname's Domain.
	annotationDNSAuthorityMissingSince = "dns.datumapis.com/authority-missing-since"
)

// +kubebuilder:rbac:groups=dns.networking.miloapis.com,resources=dnsrecordsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dns.networking.miloapis.com,resources=dnsrecordsets/status,verbs=get
// +kubebuilder:rbac:groups=dns.networking.miloapis.com,resources=dnszones,verbs=get;list;watch

// ensureDNSRecordSets creates or updates DNSRecordSet resources for all claimed
// hostnames on the upstream gateway whose apex domains are verified via Datum
// DNS. Hostnames whose domains are not managed by Datum DNS are skipped and
// marked NotApplicable. It returns per-hostname DNS status entries that reflect
// the result of each DNS programming attempt, a Result that signals whether
// the caller should requeue, and how soon the earliest kept record set's
// authority wait ends (zero when none is kept).
func (r *GatewayReconciler) ensureDNSRecordSets(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	claimedHostnames []string,
) (hostnameStatuses []networkingv1alpha.HostnameStatus, result Result, holdRecheck time.Duration) {
	if !r.Config.Gateway.EnableDNSIntegration {
		return nil, result, 0
	}

	logger := log.FromContext(ctx)
	logger.V(1).Info("ensuring DNS record sets", "claimed_hostname_count", len(claimedHostnames))

	canonicalHostname := r.gatewayCanonicalHostname(upstreamGateway)

	// List all Domains in the gateway namespace once; we query them per hostname below.
	var domainList networkingv1alpha.DomainList
	if err := upstreamClient.List(ctx, &domainList, client.InNamespace(upstreamGateway.Namespace)); err != nil {
		result.Err = fmt.Errorf("failed listing domains: %w", err)
		return nil, result, 0
	}

	// Build a set of desired DNSRecordSet names so we can garbage-collect stale ones.
	desiredRecordSetNames := map[string]bool{}

	for _, hostname := range claimedHostnames {
		if r.isDatumManagedGatewayHostname(upstreamGateway, hostname) {
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

		// Find the most specific Domain + DNSZone combination where Datum DNS has authority.
		var domain *networkingv1alpha.Domain
		var dnsZone *dnsv1alpha1.DNSZone
		var matchedZoneName string

		for _, zoneName := range zoneNames {
			// Check if a Domain exists for this zone name.
			d, found := findDomainByName(domainList.Items, zoneName)
			if !found {
				continue
			}

			// Check if the Domain is verified (ownership proven via any method).
			if !apimeta.IsStatusConditionTrue(d.Status.Conditions, networkingv1alpha.DomainConditionVerified) {
				// Domain exists but isn't verified; keep looking.
				continue
			}

			// Look up the DNSZone for this domain.
			var dnsZoneList dnsv1alpha1.DNSZoneList
			if err := upstreamClient.List(ctx, &dnsZoneList,
				client.InNamespace(upstreamGateway.Namespace),
				client.MatchingFields{dnsZoneDomainNameIndex: zoneName},
			); err != nil {
				result.Err = fmt.Errorf("failed listing DNSZones for domain %q: %w", zoneName, err)
				return nil, result, 0
			}

			if len(dnsZoneList.Items) == 0 {
				continue
			}

			// Check if Datum DNS has authority (DNSZone ready + nameservers match).
			zone := &dnsZoneList.Items[0]
			if !dnsutil.HasDNSAuthority(&d, zone) {
				// Domain verified but Datum DNS doesn't have authority yet.
				continue
			}

			// Found a matching Domain + DNSZone where Datum DNS has authority.
			domain = &d
			dnsZone = zone
			matchedZoneName = zoneName
			break
		}

		// If no matching Domain + DNSZone found, check why and set appropriate status.
		if domain == nil || dnsZone == nil {
			// Try to provide a helpful message by checking what we found.
			var (
				unverifiedDomain  *networkingv1alpha.Domain
				noAuthorityDomain *networkingv1alpha.Domain
				noAuthorityZone   *dnsv1alpha1.DNSZone
			)
			for _, zoneName := range zoneNames {
				d, found := findDomainByName(domainList.Items, zoneName)
				if !found {
					continue
				}

				// Check if domain is verified
				if !apimeta.IsStatusConditionTrue(d.Status.Conditions, networkingv1alpha.DomainConditionVerified) {
					unverifiedDomain = &d
					break
				}

				// Domain is verified; check if there's a DNSZone
				var dnsZoneList dnsv1alpha1.DNSZoneList
				if err := upstreamClient.List(ctx, &dnsZoneList,
					client.InNamespace(upstreamGateway.Namespace),
					client.MatchingFields{dnsZoneDomainNameIndex: zoneName},
				); err != nil {
					continue
				}
				if len(dnsZoneList.Items) == 0 {
					continue
				}

				// DNSZone exists but Datum DNS doesn't have authority
				noAuthorityDomain = &d
				noAuthorityZone = &dnsZoneList.Items[0]
				break
			}

			switch {
			case unverifiedDomain != nil:
				apimeta.SetStatusCondition(&hs.Conditions, metav1.Condition{
					Type:               networkingv1alpha.HostnameConditionDNSRecordProgrammed,
					Status:             metav1.ConditionFalse,
					Reason:             networkingv1alpha.DNSRecordReasonDomainNotVerified,
					Message:            fmt.Sprintf("Domain %q ownership has not been verified", unverifiedDomain.Name),
					ObservedGeneration: upstreamGateway.Generation,
				})
			case noAuthorityDomain != nil:
				msg := authorityMissingMessage(noAuthorityDomain, noAuthorityZone)
				note, err := r.keepRecordSetWithoutAuthority(ctx, upstreamClient, upstreamGateway, hostname, desiredRecordSetNames, &holdRecheck)
				if err != nil {
					result.Err = err
					return nil, result, 0
				}
				apimeta.SetStatusCondition(&hs.Conditions, metav1.Condition{
					Type:               networkingv1alpha.HostnameConditionDNSRecordProgrammed,
					Status:             metav1.ConditionFalse,
					Reason:             networkingv1alpha.DNSRecordReasonDNSAuthorityMissing,
					Message:            msg + note,
					ObservedGeneration: upstreamGateway.Generation,
				})
			default:
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

		logger.V(1).Info("found matching zone for hostname",
			"hostname", hostname,
			"zone", matchedZoneName,
			"domain", domain.Name,
		)

		// Determine the record type based on whether this hostname is an apex domain.
		rrType := dnsv1alpha1.RRTypeCNAME
		if domain.Status.Apex && !isWildcardHostname(hostname) {
			rrType = dnsv1alpha1.RRTypeALIAS
		}

		recordSetName := dnsRecordSetName(upstreamGateway.Name, hostname)
		desiredRecordSetNames[recordSetName] = true

		// Conflict detection: list existing DNSRecordSets with the same
		// hostname annotation in this namespace that reference this zone.
		var existingList dnsv1alpha1.DNSRecordSetList
		if err := upstreamClient.List(ctx, &existingList,
			client.InNamespace(upstreamGateway.Namespace),
			client.MatchingLabels{labelDNSManaged: labelValueTrue},
		); err != nil {
			result.Err = fmt.Errorf("failed listing existing DNSRecordSets: %w", err)
			return nil, result, 0
		}

		if existing := userRecordConflict(existingList.Items, recordSetName, hostname, dnsZone.Name); existing != nil {
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
			if err := endAuthorityHold(ctx, upstreamClient, client.ObjectKey{Namespace: upstreamGateway.Namespace, Name: recordSetName}); err != nil {
				result.Err = err
				return nil, result, 0
			}
			continue
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

				// Set owner reference for garbage collection, but NOT as controller.
				// The dns-operator's dnsrecordset-replicator expects to be the controller
				// of DNSRecordSets it manages. Using SetOwnerReference (not SetControllerReference)
				// allows the Gateway to own the record for GC while letting dns-operator manage it.
				if err := controllerutil.SetOwnerReference(upstreamGateway, desired, upstreamClient.Scheme()); err != nil {
					return fmt.Errorf("failed to set owner reference on DNSRecordSet: %w", err)
				}

				// Ensure labels and annotations are set on both create and update.
				if desired.Labels == nil {
					desired.Labels = map[string]string{}
				}
				desired.Labels[labelManagedBy] = labelManagedByValue
				desired.Labels[labelDNSManaged] = labelValueTrue
				desired.Labels[labelDNSSourceKind] = KindGateway
				desired.Labels[labelDNSSourceName] = upstreamGateway.Name
				desired.Labels[labelDNSSourceNS] = upstreamGateway.Namespace

				if desired.Annotations == nil {
					desired.Annotations = map[string]string{}
				}
				desired.Annotations[annotationDNSHostname] = hostname
				// Authority is back, so any wait it started is over.
				delete(desired.Annotations, annotationDNSAuthorityMissingSince)

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
					return hostnameStatuses, result, 0
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
	}

	// Garbage-collect stale DNSRecordSets that are no longer needed.
	gcResult := r.garbageCollectDNSRecordSets(ctx, upstreamClient, upstreamGateway, desiredRecordSetNames)
	if gcResult.ShouldReturn() {
		return hostnameStatuses, gcResult.Merge(result), holdRecheck
	}

	return hostnameStatuses, result, holdRecheck
}

// userRecordConflict returns a record set that another manager keeps for the
// hostname in its zone, or nil. The gateway's own record set, recordSetName, is
// never a conflict.
func userRecordConflict(existing []dnsv1alpha1.DNSRecordSet, recordSetName, hostname, zoneName string) *dnsv1alpha1.DNSRecordSet {
	for i := range existing {
		rs := &existing[i]
		if rs.Name != recordSetName &&
			rs.Annotations[annotationDNSHostname] == hostname &&
			rs.Spec.DNSZoneRef.Name == zoneName &&
			rs.Labels[labelManagedBy] != labelManagedByValue {
			return rs
		}
	}
	return nil
}

// authorityMissingMessage says why Datum DNS has no authority for a verified
// Domain, from the most specific fact the DNSZone shows.
func authorityMissingMessage(domain *networkingv1alpha.Domain, zone *dnsv1alpha1.DNSZone) string {
	switch {
	case zone == nil:
		return fmt.Sprintf("Domain %q is verified but Datum DNS does not have authority", domain.Name)
	case !apimeta.IsStatusConditionTrue(zone.Status.Conditions, conditionTypeAccepted) ||
		!apimeta.IsStatusConditionTrue(zone.Status.Conditions, conditionTypeProgrammed):
		return fmt.Sprintf("DNSZone %q is not ready (waiting for Accepted and Programmed conditions)", zone.Name)
	case len(zone.Status.Nameservers) == 0:
		return fmt.Sprintf("DNSZone %q has no nameservers assigned yet", zone.Name)
	default:
		return fmt.Sprintf("Domain %q nameservers do not include DNSZone %q nameservers; update your registrar's NS records", domain.Name, zone.Name)
	}
}

// keepRecordSetWithoutAuthority keeps the existing record set of a claimed
// hostname whose Domain has lost Datum DNS authority, for at most the configured
// period, so that one wrong nameserver answer does not delete a record the
// gateway still wants. It adds the record set to the desired set, lowers recheck
// to when the wait ends, and returns the note the hostname's status carries.
func (r *GatewayReconciler) keepRecordSetWithoutAuthority(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	hostname string,
	desired map[string]bool,
	recheck *time.Duration,
) (string, error) {
	name := dnsRecordSetName(upstreamGateway.Name, hostname)
	// One reading of the clock for the stamp, the deadline and the wait, so they cannot disagree.
	now := time.Now()
	until, kept, err := r.authorityHoldDeadline(ctx, upstreamClient, client.ObjectKey{Namespace: upstreamGateway.Namespace, Name: name}, now)
	if err != nil || !kept {
		return "", err
	}
	desired[name] = true
	if wait := until.Sub(now); *recheck == 0 || wait < *recheck {
		*recheck = wait
	}
	return fmt.Sprintf("; the existing record is kept until %s", until.UTC().Format(time.RFC3339)), nil
}

// authorityHoldDeadline returns when the wait on a record set without authority
// ends, stamping the record set at the first check. The stamp lives on the
// object rather than in memory, so a restart or a change of leader neither
// restarts the wait nor skips it. It reports false when there is nothing to
// keep: no record set of ours, a zero period, or a wait that is over.
func (r *GatewayReconciler) authorityHoldDeadline(ctx context.Context, upstreamClient client.Client, key client.ObjectKey, now time.Time) (time.Time, bool, error) {
	period := r.Config.Gateway.AuthorityLossGracePeriod()
	if period <= 0 {
		return time.Time{}, false, nil
	}
	rs, err := ownRecordSet(ctx, upstreamClient, key)
	if rs == nil || err != nil {
		return time.Time{}, false, err
	}
	since, ok := authorityMissingSince(ctx, rs)
	if !ok {
		since = now.UTC().Truncate(time.Second)
		if err := stampAuthorityMissing(ctx, upstreamClient, rs, since); err != nil {
			return time.Time{}, false, err
		}
	}
	until := since.Add(period)
	return until, now.Before(until), nil
}

// endAuthorityHold removes the stamp from a record set whose Domain has Datum
// DNS authority again, on the path that leaves the record set otherwise as it
// is: a record of another manager holds the hostname. A stamp left there would
// count from the old loss at the next one. The programming path removes the
// stamp in its own update.
func endAuthorityHold(ctx context.Context, upstreamClient client.Client, key client.ObjectKey) error {
	rs, err := ownRecordSet(ctx, upstreamClient, key)
	if rs == nil || err != nil {
		return err
	}
	if _, stamped := rs.Annotations[annotationDNSAuthorityMissingSince]; !stamped {
		return nil
	}
	patch := client.MergeFrom(rs.DeepCopy())
	delete(rs.Annotations, annotationDNSAuthorityMissingSince)
	if err := upstreamClient.Patch(ctx, rs, patch); err != nil {
		return fmt.Errorf("failed removing the authority-missing stamp from DNSRecordSet %q: %w", rs.Name, err)
	}
	return nil
}

// ownRecordSet reads the record set Gateway DNS manages under key. It returns
// nil when there is none, or when another manager owns that name: the hold
// never writes a record set it does not manage.
func ownRecordSet(ctx context.Context, upstreamClient client.Client, key client.ObjectKey) (*dnsv1alpha1.DNSRecordSet, error) {
	var rs dnsv1alpha1.DNSRecordSet
	if err := upstreamClient.Get(ctx, key, &rs); err != nil {
		return nil, client.IgnoreNotFound(err)
	}
	if rs.Labels[labelManagedBy] != labelManagedByValue {
		return nil, nil
	}
	return &rs, nil
}

// authorityMissingSince reads a record set's stamp. An unreadable stamp is
// treated as none: the wait restarts rather than a record set being deleted on
// the strength of a value nothing can read.
func authorityMissingSince(ctx context.Context, rs *dnsv1alpha1.DNSRecordSet) (time.Time, bool) {
	stamp := rs.Annotations[annotationDNSAuthorityMissingSince]
	if stamp == "" {
		return time.Time{}, false
	}
	since, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		log.FromContext(ctx).Info("DNS record set carries an unreadable authority-missing-since stamp, restarting the wait",
			"record_set_name", rs.Name, "stamp", stamp)
		return time.Time{}, false
	}
	return since, true
}

func stampAuthorityMissing(ctx context.Context, upstreamClient client.Client, rs *dnsv1alpha1.DNSRecordSet, since time.Time) error {
	patch := client.MergeFrom(rs.DeepCopy())
	if rs.Annotations == nil {
		rs.Annotations = map[string]string{}
	}
	rs.Annotations[annotationDNSAuthorityMissingSince] = since.Format(time.RFC3339)
	if err := upstreamClient.Patch(ctx, rs, patch); err != nil {
		return fmt.Errorf("failed recording when DNSRecordSet %q lost DNS authority: %w", rs.Name, err)
	}
	return nil
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

	result.AddStatusPatch(upstreamClient, upstreamGateway, fieldOwner(gatewayControllerName))
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
			labelDNSManaged:    labelValueTrue,
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
			jsonKeyName, rs.Name,
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

func isWildcardHostname(hostname string) bool {
	return strings.HasPrefix(hostname, "*.")
}

// relativeOwnerName returns the DNS owner name for hostname relative to
// zoneDomain. Trailing dots are stripped before comparison. Apex (hostname
// equals the zone domain) yields "@". Multi-label prefixes are preserved with
// their original casing (e.g. "a.b.example.com" in "example.com" → "a.b").
// If hostname is not under the zone, the absolute hostname without a trailing
// dot is returned as a safe fallback.
func relativeOwnerName(hostname, zoneDomain string) string {
	h := strings.TrimSuffix(hostname, ".")
	z := strings.TrimSuffix(zoneDomain, ".")
	if h == "" || z == "" {
		return h
	}
	if strings.EqualFold(h, z) {
		return "@"
	}
	// Case-insensitive suffix match on "."+zoneDomain, but keep original labels.
	if len(h) > len(z)+1 && h[len(h)-len(z)-1] == '.' && strings.EqualFold(h[len(h)-len(z):], z) {
		return h[:len(h)-len(z)-1]
	}
	return h
}

// buildDesiredDNSRecordSetSpec constructs the DNSRecordSetSpec that points
// hostname at canonicalHostname. The owner name is written relative to
// dnsZone.Spec.DomainName (e.g. "api", "@"); CNAME/ALIAS content is
// normalized to an absolute FQDN with a trailing dot. The record type is
// CNAME for non-apex hostnames and ALIAS for apex domains, as determined by
// the caller.
func buildDesiredDNSRecordSetSpec(
	hostname, canonicalHostname string,
	dnsZone dnsv1alpha1.DNSZone,
	rrType dnsv1alpha1.RRType,
) dnsv1alpha1.DNSRecordSetSpec {
	fqdnTarget := canonicalHostname
	if !strings.HasSuffix(fqdnTarget, ".") {
		fqdnTarget = fqdnTarget + "."
	}

	var entry dnsv1alpha1.RecordEntry
	entry.Name = relativeOwnerName(hostname, dnsZone.Spec.DomainName)
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
func (r *GatewayReconciler) listGatewaysForDNSZoneFunc(clusterName multicluster.ClusterName, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
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
// the owning Gateway whenever a DNSRecordSet changes.
func (r *GatewayReconciler) listGatewaysForDNSRecordSetFunc(clusterName multicluster.ClusterName, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
	return enqueueDNSRecordSetSource(clusterName, cl)
}

// enqueueDNSRecordSetSource returns a TypedEventHandler that enqueues the
// Gateway a DNSRecordSet was written for, named by the
// dns.datumapis.com/source-name and dns.datumapis.com/source-namespace labels
// this operator writes. An HTTPProxy's Gateway carries the HTTPProxy's name, so
// the same request also reaches the HTTPProxy controller.
func enqueueDNSRecordSetSource(clusterName multicluster.ClusterName, _ cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
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
