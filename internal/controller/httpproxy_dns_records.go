// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	certificatesv1alpha1 "go.datum.net/network-services-operator/internal/certificates/v1alpha1"
	dnsutil "go.datum.net/network-services-operator/internal/util/dns"

	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"
)

const (
	acmeChallengeLabel = "_acme-challenge"

	routingProbeLabel = "datum-routing-probe"

	routingObservationTTL = time.Minute

	routingRecheckInterval = 5 * time.Minute

	routingLookupTimeout = 2 * time.Second

	routingObservationCacheLimit = 10000
)

// routingObserver reports whether a hostname the user points at the platform
// resolves there, from public DNS, and remembers the answer briefly so a burst
// of reconciles costs one set of lookups.
type routingObserver struct {
	lookupCNAME func(ctx context.Context, host string) (string, error)
	lookupIP    func(ctx context.Context, host string) ([]net.IPAddr, error)
	now         func() time.Time

	mu    sync.Mutex
	cache map[string]routingObservation
}

type routingObservation struct {
	present bool
	expires time.Time
}

func newRoutingObserver() *routingObserver {
	return &routingObserver{
		lookupCNAME: net.DefaultResolver.LookupCNAME,
		lookupIP:    net.DefaultResolver.LookupIPAddr,
		now:         time.Now,
		cache:       map[string]routingObservation{},
	}
}

func (o *routingObserver) routesTo(ctx context.Context, hostname, canonical string) bool {
	key := hostname + "|" + canonical
	now := o.now()

	o.mu.Lock()
	if seen, ok := o.cache[key]; ok && now.Before(seen.expires) {
		o.mu.Unlock()
		return seen.present
	}
	o.mu.Unlock()

	present := o.lookup(ctx, hostname, canonical)

	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.cache) >= routingObservationCacheLimit {
		for k, v := range o.cache {
			if !now.Before(v.expires) {
				delete(o.cache, k)
			}
		}
	}
	o.cache[key] = routingObservation{present: present, expires: now.Add(routingObservationTTL)}
	return present
}

func (o *routingObserver) lookup(ctx context.Context, hostname, canonical string) bool {
	ctx, cancel := context.WithTimeout(ctx, routingLookupTimeout)
	defer cancel()

	probe := hostname
	if base, ok := strings.CutPrefix(hostname, "*."); ok {
		probe = routingProbeLabel + "." + base
	}

	if target, err := o.lookupCNAME(ctx, probe+"."); err == nil && strings.EqualFold(strings.TrimSuffix(target, "."), canonical) {
		return true
	}

	probeAddrs, err := o.lookupIP(ctx, probe+".")
	if err != nil || len(probeAddrs) == 0 {
		return false
	}
	canonicalAddrs, err := o.lookupIP(ctx, canonical+".")
	if err != nil {
		return false
	}
	for _, a := range probeAddrs {
		for _, b := range canonicalAddrs {
			if a.IP.Equal(b.IP) {
				return true
			}
		}
	}
	return false
}

// buildDNSRecordStatuses lists, for each custom hostname, the DNS records it
// depends on and whether each takes effect. It reports whether any record the
// user publishes for routing is still missing, since nothing but time tells
// the controller when one appears.
func (r *HTTPProxyReconciler) buildDNSRecordStatuses(
	ctx context.Context,
	cl client.Client,
	gateway *gatewayv1.Gateway,
	httpProxy *networkingv1alpha.HTTPProxy,
) (statuses []networkingv1alpha.HostnameStatus, recheckRouting bool) {
	if !r.Config.Gateway.CertificateService.Enabled {
		return nil, false
	}

	logger := log.FromContext(ctx)
	canonical := httpProxy.Status.CanonicalHostname

	var domains networkingv1alpha.DomainList
	if err := cl.List(ctx, &domains, client.InNamespace(httpProxy.Namespace)); err != nil {
		logger.Error(err, "failed to list domains for hostname DNS records")
		return nil, false
	}

	httpsListeners := map[string]gatewayv1.SectionName{}
	for _, l := range gateway.Spec.Listeners {
		if l.Protocol == gatewayv1.HTTPSProtocolType && l.Hostname != nil {
			httpsListeners[string(*l.Hostname)] = l.Name
		}
	}

	for _, h := range httpProxy.Spec.Hostnames {
		hostname := string(h)
		if r.underTargetDomain(hostname) {
			continue
		}

		var records []networkingv1alpha.HostnameDNSRecord

		if canonical != "" {
			routing := r.routingRecord(ctx, cl, gateway, hostname, canonical, domains.Items)
			if routing.ManagedBy == networkingv1alpha.HostnameDNSRecordManagedByUser && routing.State == networkingv1alpha.HostnameDNSRecordMissing {
				recheckRouting = true
			}
			records = append(records, routing)
		}

		if listener, ok := httpsListeners[hostname]; ok && isSingleLabelWildcard(hostname) {
			records = append(records, r.certificateRecords(ctx, cl, gateway, listener, hostname)...)
		}

		if ownership, ok := ownershipRecord(hostname, domains.Items); ok {
			records = append(records, ownership)
		}

		if len(records) > 0 {
			statuses = append(statuses, networkingv1alpha.HostnameStatus{Hostname: hostname, DNSRecords: records})
		}
	}

	return statuses, recheckRouting
}

func (r *HTTPProxyReconciler) underTargetDomain(hostname string) bool {
	target := r.Config.Gateway.TargetDomain
	return target != "" && (hostname == target || strings.HasSuffix(hostname, "."+target))
}

// routingRecord describes the record that points the hostname at the
// platform. A record the platform writes into a Datum DNS zone counts only
// while that zone is the one the domain's registry delegates to; any other
// record is judged by what public DNS answers.
func (r *HTTPProxyReconciler) routingRecord(
	ctx context.Context,
	cl client.Client,
	gateway *gatewayv1.Gateway,
	hostname, canonical string,
	domains []networkingv1alpha.Domain,
) networkingv1alpha.HostnameDNSRecord {
	record := networkingv1alpha.HostnameDNSRecord{
		Name:      hostname,
		Type:      string(dnsv1alpha1.RRTypeCNAME),
		Content:   canonical,
		Purpose:   networkingv1alpha.HostnameDNSRecordPurposeRouting,
		ManagedBy: networkingv1alpha.HostnameDNSRecordManagedByUser,
		State:     networkingv1alpha.HostnameDNSRecordMissing,
	}

	if r.Config.Gateway.EnableDNSIntegration {
		if present, rrType, managed := r.platformRoutingRecord(ctx, cl, gateway, hostname, domains); managed {
			record.ManagedBy = networkingv1alpha.HostnameDNSRecordManagedByPlatform
			record.Type = rrType
			if present {
				record.State = networkingv1alpha.HostnameDNSRecordPresent
			}
			return record
		}
	}

	for _, d := range domains {
		if d.Spec.DomainName == hostname && d.Status.Apex {
			record.Type = string(dnsv1alpha1.RRTypeALIAS)
			break
		}
	}

	if r.routing != nil && r.routing.routesTo(ctx, hostname, canonical) {
		record.State = networkingv1alpha.HostnameDNSRecordPresent
	}
	return record
}

func (r *HTTPProxyReconciler) platformRoutingRecord(
	ctx context.Context,
	cl client.Client,
	gateway *gatewayv1.Gateway,
	hostname string,
	domains []networkingv1alpha.Domain,
) (present bool, rrType string, managed bool) {
	var recordSet dnsv1alpha1.DNSRecordSet
	if err := cl.Get(ctx, client.ObjectKey{Namespace: gateway.Namespace, Name: dnsRecordSetName(gateway.Name, hostname)}, &recordSet); err != nil {
		return false, "", false
	}
	if recordSet.Labels[labelManagedBy] != labelManagedByValue || recordSet.Annotations[annotationDNSHostname] != hostname {
		return false, "", false
	}

	rrType = string(recordSet.Spec.RecordType)
	if !apimeta.IsStatusConditionTrue(recordSet.Status.Conditions, conditionTypeProgrammed) {
		return false, rrType, true
	}

	var zone dnsv1alpha1.DNSZone
	if err := cl.Get(ctx, client.ObjectKey{Namespace: gateway.Namespace, Name: recordSet.Spec.DNSZoneRef.Name}, &zone); err != nil {
		return false, rrType, true
	}
	domain, found := findDomainByName(domains, zone.Spec.DomainName)
	if !found {
		return false, rrType, true
	}
	return dnsutil.HasDNSAuthority(&domain, &zone), rrType, true
}

// certificateRecords lists the record that delegates the hostname's ACME DNS
// challenge, when its certificate issues over DNS. Only the challenge name for
// this hostname is taken from the certificate's status.
func (r *HTTPProxyReconciler) certificateRecords(
	ctx context.Context,
	cl client.Client,
	gateway *gatewayv1.Gateway,
	listener gatewayv1.SectionName,
	hostname string,
) []networkingv1alpha.HostnameDNSRecord {
	var cert certificatesv1alpha1.TLSCertificate
	if err := cl.Get(ctx, client.ObjectKey{Namespace: gateway.Namespace, Name: tlsCertificateName(gateway.Name, listener)}, &cert); err != nil {
		return nil
	}

	name := acmeChallengeLabel + "." + strings.TrimPrefix(hostname, "*.")
	content := ""
	for _, required := range cert.Status.RequiredDNSRecords {
		if required.Purpose == certificatesv1alpha1.DNSRecordPurposeCertificate &&
			strings.EqualFold(strings.TrimSuffix(required.Name, "."), name) &&
			strings.EqualFold(required.Type, string(dnsv1alpha1.RRTypeCNAME)) {
			content = required.Content
			break
		}
	}
	if content == "" && cert.Status.Issuance == certificatesv1alpha1.ChallengeTypeDNS01 {
		content = cert.Status.DelegationTarget
	}
	if content == "" {
		return nil
	}

	state := networkingv1alpha.HostnameDNSRecordMissing
	if delegation := apimeta.FindStatusCondition(cert.Status.Conditions, certificatesv1alpha1.ConditionDNSDelegationReady); delegation != nil &&
		delegation.Status == metav1.ConditionTrue {
		state = networkingv1alpha.HostnameDNSRecordPresent
	}

	return []networkingv1alpha.HostnameDNSRecord{{
		Name:      name,
		Type:      string(dnsv1alpha1.RRTypeCNAME),
		Content:   strings.TrimSuffix(content, "."),
		Purpose:   networkingv1alpha.HostnameDNSRecordPurposeCertificate,
		ManagedBy: networkingv1alpha.HostnameDNSRecordManagedByUser,
		State:     state,
	}}
}

// ownershipRecord returns the TXT record that would verify the most specific
// Domain covering the hostname, while no covering Domain is verified yet.
func ownershipRecord(hostname string, domains []networkingv1alpha.Domain) (networkingv1alpha.HostnameDNSRecord, bool) {
	var pending *networkingv1alpha.Domain
	for i := range domains {
		d := &domains[i]
		if !domainCoversHostname(d.Spec.DomainName, hostname) {
			continue
		}
		if apimeta.IsStatusConditionTrue(d.Status.Conditions, networkingv1alpha.DomainConditionVerified) {
			return networkingv1alpha.HostnameDNSRecord{}, false
		}
		if d.Status.Verification == nil || d.Status.Verification.DNSRecord.Name == "" {
			continue
		}
		if pending == nil || len(d.Spec.DomainName) > len(pending.Spec.DomainName) {
			pending = d
		}
	}
	if pending == nil {
		return networkingv1alpha.HostnameDNSRecord{}, false
	}

	record := pending.Status.Verification.DNSRecord
	return networkingv1alpha.HostnameDNSRecord{
		Name:      strings.TrimSuffix(record.Name, "."),
		Type:      record.Type,
		Content:   record.Content,
		Purpose:   networkingv1alpha.HostnameDNSRecordPurposeOwnership,
		ManagedBy: networkingv1alpha.HostnameDNSRecordManagedByUser,
		State:     networkingv1alpha.HostnameDNSRecordMissing,
	}, true
}

func domainCoversHostname(domainName, hostname string) bool {
	if domainName == "" {
		return false
	}
	return hostname == domainName || strings.HasSuffix(hostname, "."+domainName)
}
