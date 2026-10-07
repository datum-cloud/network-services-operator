// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	certificatesv1alpha1 "go.datum.net/network-services-operator/internal/certificates/v1alpha1"
	dnsutil "go.datum.net/network-services-operator/internal/util/dns"
	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"
)

// ensureDelegationRecordSets writes the CNAME that delegates a wildcard
// hostname's ACME DNS challenge, for hostnames whose zone Datum DNS serves. A
// challenge name already holding records this controller did not write is left
// alone, so the customer keeps publishing that record. Every record set kept
// is added to desiredNames so only the routing cleanup pass decides what goes.
func (r *GatewayReconciler) ensureDelegationRecordSets(
	ctx context.Context,
	upstreamClient client.Client,
	upstreamGateway *gatewayv1.Gateway,
	claimedHostnames []string,
	domains []networkingv1alpha.Domain,
	desiredNames map[string]bool,
) error {
	if !r.Config.Gateway.CertificateService.Enabled {
		return nil
	}
	logger := log.FromContext(ctx)

	var existing dnsv1alpha1.DNSRecordSetList
	if err := upstreamClient.List(ctx, &existing, client.InNamespace(upstreamGateway.Namespace)); err != nil {
		return fmt.Errorf("failed listing DNSRecordSets: %w", err)
	}

	for _, listener := range upstreamGateway.Spec.Listeners {
		if listener.Protocol != gatewayv1.HTTPSProtocolType || listener.Hostname == nil {
			continue
		}
		hostname := string(*listener.Hostname)
		if !isSingleLabelWildcard(hostname) || !slices.Contains(claimedHostnames, hostname) {
			continue
		}

		var cert certificatesv1alpha1.TLSCertificate
		if err := upstreamClient.Get(ctx, client.ObjectKey{Namespace: upstreamGateway.Namespace, Name: tlsCertificateName(upstreamGateway.Name, listener.Name)}, &cert); err != nil {
			continue
		}
		challenge, content := delegationRecordFor(&cert, hostname)
		if content == "" {
			continue
		}

		zone, err := r.datumHostedZone(ctx, upstreamClient, upstreamGateway.Namespace, challenge, domains)
		if err != nil {
			return err
		}
		if zone == nil {
			continue
		}

		name := dnsRecordSetName(upstreamGateway.Name, challenge)
		owner := relativeOwnerName(challenge, zone.Spec.DomainName)
		if conflict := recordSetHoldingOwner(existing.Items, name, zone.Name, owner); conflict != "" {
			logger.Info("certificate delegation record left to the user, name is occupied",
				"hostname", hostname, "name", challenge, "conflicting_record", conflict)
			if !recordSetExists(existing.Items, name) {
				continue
			}
		}

		desired := buildDesiredDNSRecordSet(upstreamGateway, name)
		if _, err := controllerutil.CreateOrUpdate(ctx, upstreamClient, desired, func() error {
			if by := desired.Labels[labelManagedBy]; by != "" && by != labelManagedByValue {
				return fmt.Errorf("conflict: existing DNSRecordSet %q is managed by %q", desired.Name, by)
			}
			if err := controllerutil.SetOwnerReference(upstreamGateway, desired, upstreamClient.Scheme()); err != nil {
				return fmt.Errorf("failed to set owner reference on DNSRecordSet: %w", err)
			}
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
			desired.Annotations[annotationDNSHostname] = challenge
			if desired.CreationTimestamp.IsZero() {
				desired.Annotations[annotationSyncStart] = metav1.Now().UTC().Format("2006-01-02T15:04:05Z")
			}
			desired.Spec = buildDesiredDNSRecordSetSpec(challenge, content, *zone, dnsv1alpha1.RRTypeCNAME)
			desired.Spec.Records[0].TTL = ptr.To(int64(300))
			return nil
		}); err != nil {
			return fmt.Errorf("failed to write certificate delegation record %q: %w", challenge, err)
		}
		desiredNames[name] = true
	}
	return nil
}

func (r *GatewayReconciler) datumHostedZone(
	ctx context.Context,
	cl client.Client,
	namespace, hostname string,
	domains []networkingv1alpha.Domain,
) (*dnsv1alpha1.DNSZone, error) {
	for _, zoneName := range possibleZoneNames(hostname) {
		domain, found := findDomainByName(domains, zoneName)
		if !found || !apimeta.IsStatusConditionTrue(domain.Status.Conditions, networkingv1alpha.DomainConditionVerified) {
			continue
		}
		var zones dnsv1alpha1.DNSZoneList
		if err := cl.List(ctx, &zones,
			client.InNamespace(namespace),
			client.MatchingFields{dnsZoneDomainNameIndex: zoneName},
		); err != nil {
			return nil, fmt.Errorf("failed listing DNSZones for domain %q: %w", zoneName, err)
		}
		if len(zones.Items) == 0 {
			continue
		}
		if dnsutil.HasDNSAuthority(&domain, &zones.Items[0]) {
			return &zones.Items[0], nil
		}
	}
	return nil, nil
}

func recordSetHoldingOwner(sets []dnsv1alpha1.DNSRecordSet, ownName, zoneName, owner string) string {
	for _, rs := range sets {
		if rs.Name == ownName || rs.Spec.DNSZoneRef.Name != zoneName {
			continue
		}
		for _, entry := range rs.Spec.Records {
			if strings.EqualFold(entry.Name, owner) {
				return rs.Name
			}
		}
	}
	return ""
}

func recordSetExists(sets []dnsv1alpha1.DNSRecordSet, name string) bool {
	for _, rs := range sets {
		if rs.Name == name {
			return true
		}
	}
	return false
}
