// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	dnsutil "go.datum.net/network-services-operator/internal/util/dns"
	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"
)

// A hostname whose DNS record cannot be programmed either waits on Datum, while
// its DNSZone or Domain settles, or waits on the customer (a registrar, a TXT
// record, a zone another project holds). Its DNSRecordProgrammed is False
// either way, and the reason says which. Only a wait on Datum ends without
// anyone acting, so a Gateway whose hostnames wait on Datum alone reports its
// DNS records as pending, not failed.

// waitsOnDatum reports whether a hostname's DNSRecordProgrammed reason is a
// wait on Datum: its zone settles, or Datum has not checked its Domain or read
// its nameservers yet.
func waitsOnDatum(reason string) bool {
	return reason == networkingv1alpha.DNSRecordReasonZoneNotReady || reason == networkingv1alpha.DNSRecordReasonPending
}

// zoneSettling reports whether the DNS operator is still bringing a zone up:
// each of its Accepted and Programmed conditions is True, missing, or carries a
// reason the operator uses while it settles. A zone refused for another reason
// does not settle on its own.
func zoneSettling(zone *dnsv1alpha1.DNSZone) bool {
	for _, conditionType := range []string{conditionTypeAccepted, conditionTypeProgrammed} {
		c := apimeta.FindStatusCondition(zone.Status.Conditions, conditionType)
		if c != nil && c.Status != metav1.ConditionTrue && !zoneSettlingReason(c.Reason) {
			return false
		}
	}
	return true
}

func zoneSettlingReason(reason string) bool {
	return reason == dnsZoneReasonPending || reason == dnsZoneReasonPendingDomainVerification
}

// authorityReason is the reason a verified Domain lacks Datum DNS authority:
// DNSZoneNotReady while its zone settles or has no nameservers yet, Pending
// while Datum reads the Domain's nameservers, and DNSAuthorityMissing when the
// customer must act: a registrar that does not delegate to the zone, or a zone
// refused for another reason.
func authorityReason(domain *networkingv1alpha.Domain, zone *dnsv1alpha1.DNSZone) string {
	inputs := dnsutil.ReadZoneInputs(zone)
	switch {
	case !inputs.Ready():
		return notReadyZoneReason(zone)
	case len(inputs.Nameservers) == 0:
		return networkingv1alpha.DNSRecordReasonZoneNotReady
	case len(domain.Status.Nameservers) == 0:
		return networkingv1alpha.DNSRecordReasonPending
	}
	return networkingv1alpha.DNSRecordReasonDNSAuthorityMissing
}

func notReadyZoneReason(zone *dnsv1alpha1.DNSZone) string {
	if zoneSettling(zone) {
		return networkingv1alpha.DNSRecordReasonZoneNotReady
	}
	return networkingv1alpha.DNSRecordReasonDNSAuthorityMissing
}

// verificationReason is the reason of a hostname whose Domain is not verified:
// Pending while the Domain controller has not checked it yet or waits for
// nameservers Datum reads, DNSZoneNotReady while it waits for its zone to
// settle, and DomainNotVerified otherwise. A zone held until its Domain is
// verified cannot verify a subdomain, which then needs the customer's TXT
// record or HTTP token.
func verificationReason(domain *networkingv1alpha.Domain, zone *dnsv1alpha1.DNSZone) string {
	if apimeta.FindStatusCondition(domain.Status.Conditions, networkingv1alpha.DomainConditionVerified) == nil {
		return networkingv1alpha.DNSRecordReasonPending
	}
	byZone := apimeta.FindStatusCondition(domain.Status.Conditions, networkingv1alpha.DomainConditionVerifiedDNSZone)
	if byZone == nil {
		return networkingv1alpha.DNSRecordReasonDomainNotVerified
	}
	switch byZone.Reason {
	case networkingv1alpha.DomainReasonPendingVerification:
		return networkingv1alpha.DNSRecordReasonPending
	case networkingv1alpha.DomainReasonDNSZoneNotReady:
		if zoneWillVerify(zone) {
			return networkingv1alpha.DNSRecordReasonZoneNotReady
		}
	}
	return networkingv1alpha.DNSRecordReasonDomainNotVerified
}

// zoneWillVerify reports whether a Domain's zone settles toward verifying the
// Domain, rather than being held until something else verifies it.
func zoneWillVerify(zone *dnsv1alpha1.DNSZone) bool {
	return zone != nil && !zoneHeldForDomain(zone) && zoneSettling(zone)
}

func zoneHeldForDomain(zone *dnsv1alpha1.DNSZone) bool {
	accepted := apimeta.FindStatusCondition(zone.Status.Conditions, conditionTypeAccepted)
	return accepted != nil && accepted.Reason == dnsZoneReasonPendingDomainVerification
}

// unverifiedDomainReason is the DNSRecordProgrammed reason of a hostname whose
// Domain is not verified, read with the Domain's zone, if it has one. A failed
// read of the zones is returned, never taken for a Domain without a zone.
func unverifiedDomainReason(ctx context.Context, upstreamClient client.Client, domain *networkingv1alpha.Domain) (string, error) {
	var zones dnsv1alpha1.DNSZoneList
	if err := upstreamClient.List(ctx, &zones,
		client.InNamespace(domain.Namespace),
		client.MatchingFields{dnsZoneDomainNameIndex: domain.Spec.DomainName},
	); err != nil {
		return "", fmt.Errorf("failed listing DNSZones for domain %q: %w", domain.Spec.DomainName, err)
	}
	if len(zones.Items) == 0 {
		return verificationReason(domain, nil), nil
	}
	return verificationReason(domain, &zones.Items[0]), nil
}
