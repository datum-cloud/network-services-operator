// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"
)

// dnsPendingReason is the reason the Gateway's DNSRecordsProgrammed carries,
// with status False, while every hostname without a record waits on Datum
// alone. The ALB agent's catalog reads the condition as transient only while
// the string is exactly "Pending".
const dnsPendingReason = "Pending"

const settleNamespace = "test-ns"

// registrarNameservers are nameservers of a registrar that does not delegate
// to Datum DNS.
var registrarNameservers = []string{"ns1.registrar.test", "ns2.registrar.test"}

var settleConfig = config.NetworkServicesOperator{
	Gateway: config.GatewayConfig{
		TargetDomain:         "gateways.test.local",
		EnableDNSIntegration: true,
	},
}

func dnsTestCondition(conditionType string, status metav1.ConditionStatus, reason string) metav1.Condition {
	return metav1.Condition{Type: conditionType, Status: status, Reason: reason}
}

// The conditions dns-operator writes on a DNSZone, and the Domain controller
// on a Domain, while they settle.
var (
	zoneAccepted            = dnsTestCondition("Accepted", metav1.ConditionTrue, "Accepted")
	zoneHeldForVerification = dnsTestCondition("Accepted", metav1.ConditionFalse, dnsZoneReasonPendingDomainVerification)
	zoneRefusedInUse        = dnsTestCondition("Accepted", metav1.ConditionFalse, "DNSZoneInUse")
	zoneProgrammed          = dnsTestCondition("Programmed", metav1.ConditionTrue, "Programmed")
	zoneNotProgrammed       = dnsTestCondition("Programmed", metav1.ConditionFalse, "Pending")
	domainVerified          = dnsTestCondition(networkingv1alpha.DomainConditionVerified, metav1.ConditionTrue, networkingv1alpha.DomainReasonVerified)
	domainNotVerified       = dnsTestCondition(networkingv1alpha.DomainConditionVerified, metav1.ConditionFalse, networkingv1alpha.DomainReasonPendingVerification)
	verifiedByZone          = dnsTestCondition(networkingv1alpha.DomainConditionVerifiedDNSZone, metav1.ConditionTrue, networkingv1alpha.DomainReasonVerified)
	verifiedByTXT           = dnsTestCondition(networkingv1alpha.DomainConditionVerifiedDNS, metav1.ConditionTrue, networkingv1alpha.DomainReasonVerified)
	zoneCheckWaits          = dnsTestCondition(networkingv1alpha.DomainConditionVerifiedDNSZone, metav1.ConditionFalse, networkingv1alpha.DomainReasonPendingVerification)
	zoneCheckFindsNone      = dnsTestCondition(networkingv1alpha.DomainConditionVerifiedDNSZone, metav1.ConditionFalse, networkingv1alpha.DomainReasonDNSZoneNotFound)
	zoneCheckMismatch       = dnsTestCondition(networkingv1alpha.DomainConditionVerifiedDNSZone, metav1.ConditionFalse, networkingv1alpha.DomainReasonDNSZoneNameserverMismatch)
	zoneCheckZoneNotReady   = dnsTestCondition(networkingv1alpha.DomainConditionVerifiedDNSZone, metav1.ConditionFalse, networkingv1alpha.DomainReasonDNSZoneNotReady)
	txtRecordNotFound       = dnsTestCondition(networkingv1alpha.DomainConditionVerifiedDNS, metav1.ConditionFalse, networkingv1alpha.DomainReasonVerificationRecordNotFound)
	readyZoneConditions     = []metav1.Condition{zoneAccepted, zoneProgrammed}
	domainByZoneCondition   = []metav1.Condition{domainVerified, verifiedByZone}
)

// settlingZone builds the DNSZone for domainName as dns-operator has written
// it so far.
func settlingZone(domainName string, nameservers []string, conditions ...metav1.Condition) *dnsv1alpha1.DNSZone {
	return &dnsv1alpha1.DNSZone{
		ObjectMeta: metav1.ObjectMeta{Namespace: settleNamespace, Name: "zone-" + domainName, UID: uuid.NewUUID()},
		Spec:       dnsv1alpha1.DNSZoneSpec{DomainName: domainName, DNSZoneClassName: "default"},
		Status:     dnsv1alpha1.DNSZoneStatus{Nameservers: nameservers, Conditions: conditions},
	}
}

// settlingDomain builds the Domain for domainName as the Domain controller has
// written it so far.
func settlingDomain(domainName string, apex bool, nameservers []string, conditions ...metav1.Condition) *networkingv1alpha.Domain {
	d := &networkingv1alpha.Domain{
		ObjectMeta: metav1.ObjectMeta{Namespace: settleNamespace, Name: domainName, UID: uuid.NewUUID()},
		Spec:       networkingv1alpha.DomainSpec{DomainName: domainName},
		Status:     networkingv1alpha.DomainStatus{Apex: apex, Conditions: conditions},
	}
	for _, ns := range nameservers {
		d.Status.Nameservers = append(d.Status.Nameservers, networkingv1alpha.Nameserver{Hostname: ns})
	}
	return d
}

// readyDNS is a zone and Domain where Datum DNS has authority.
func readyDNS(domainName string) []client.Object {
	return []client.Object{
		settlingZone(domainName, testNameservers, readyZoneConditions...),
		settlingDomain(domainName, true, testNameservers, domainByZoneCondition...),
	}
}

// pendingDNS is a verified Domain whose zone dns-operator has not programmed yet.
func pendingDNS(domainName string) []client.Object {
	return []client.Object{
		settlingZone(domainName, testNameservers, zoneAccepted, zoneNotProgrammed),
		settlingDomain(domainName, true, testNameservers, domainByZoneCondition...),
	}
}

// foreignRecord is a record set another manager keeps for hostname in the zone
// of domainName, marked so the Gateway sees the conflict.
func foreignRecord(domainName, hostname string) *dnsv1alpha1.DNSRecordSet {
	return &dnsv1alpha1.DNSRecordSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   settleNamespace,
			Name:        "foreign-cname",
			Labels:      map[string]string{labelManagedBy: "another-manager", labelDNSManaged: labelValueTrue},
			Annotations: map[string]string{annotationDNSHostname: hostname},
		},
		Spec: dnsv1alpha1.DNSRecordSetSpec{DNSZoneRef: corev1.LocalObjectReference{Name: "zone-" + domainName}, RecordType: dnsv1alpha1.RRTypeCNAME},
	}
}

// refuseRecordSets refuses every DNSRecordSet create, as dns-operator's
// admission webhook vdnsrecordset.kb.io does when another record set already
// holds the name.
func refuseRecordSets(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
	if _, ok := obj.(*dnsv1alpha1.DNSRecordSet); ok {
		return errors.New(`admission webhook "vdnsrecordset.kb.io" denied the request: owner name is already claimed`)
	}
	return cl.Create(ctx, obj, opts...)
}

// failZoneLists fails every List of DNSZones, as a cache that cannot be read.
func failZoneLists(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*dnsv1alpha1.DNSZoneList); ok {
		return errors.New("the cache cannot list DNSZones")
	}
	return cl.List(ctx, list, opts...)
}

func settleClient(t *testing.T, refuse bool, objects ...client.Object) client.Client {
	t.Helper()
	var funcs interceptor.Funcs
	if refuse {
		funcs.Create = refuseRecordSets
	}
	return settleClientWith(t, funcs, objects...)
}

func settleClientWith(t *testing.T, funcs interceptor.Funcs, objects ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(newDNSTestScheme(t)).
		WithObjects(objects...).
		WithIndex(&dnsv1alpha1.DNSZone{}, dnsZoneDomainNameIndex, dnsZoneDomainNames).
		WithInterceptorFuncs(funcs).
		Build()
}

// gatewayDNSPass runs the Gateway's DNS step once and returns the hostnames'
// statuses and the Gateway's DNSRecordsProgrammed.
func gatewayDNSPass(t *testing.T, cl client.Client, gw *gatewayv1.Gateway, hostnames []string) ([]networkingv1alpha.HostnameStatus, *metav1.Condition) {
	t.Helper()
	r := newDNSReconciler(settleConfig)
	statuses, result, _ := r.ensureDNSRecordSets(context.Background(), cl, gw, hostnames)
	require.NoError(t, result.Err)
	r.reconcileDNSStatus(cl, gw, statuses)
	return statuses, apimeta.FindStatusCondition(gw.Status.Conditions, networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed)
}

// logHostnames records each hostname's own DNSRecordProgrammed, which the
// Gateway computes in memory and never stores.
func logHostnames(t *testing.T, step string, statuses []networkingv1alpha.HostnameStatus) {
	t.Helper()
	for _, hs := range statuses {
		c := apimeta.FindStatusCondition(hs.Conditions, networkingv1alpha.HostnameConditionDNSRecordProgrammed)
		t.Logf("%s: %s DNSRecordProgrammed=%s/%s %q", step, hs.Hostname, c.Status, c.Reason, c.Message)
	}
}

// assertHostnameReasons checks each hostname's own DNSRecordProgrammed reason,
// which says whether it waits on Datum or on the customer. An empty reason
// checks nothing.
func assertHostnameReasons(t *testing.T, statuses []networkingv1alpha.HostnameStatus, reason string) {
	t.Helper()
	if reason == "" {
		return
	}
	for _, hs := range statuses {
		c := apimeta.FindStatusCondition(hs.Conditions, networkingv1alpha.HostnameConditionDNSRecordProgrammed)
		if assert.NotNil(t, c, "hostname %s carries DNSRecordProgrammed", hs.Hostname) {
			assert.Equal(t, metav1.ConditionFalse, c.Status, "hostname %s status", hs.Hostname)
			assert.Equal(t, reason, c.Reason, "hostname %s reason", hs.Hostname)
		}
	}
}

func assertAggregate(t *testing.T, got *metav1.Condition, want metav1.ConditionStatus, reason string) {
	t.Helper()
	if !assert.NotNil(t, got, "the Gateway carries DNSRecordsProgrammed") {
		return
	}
	assert.Equal(t, want, got.Status, "status")
	assert.Equal(t, reason, got.Reason, "reason")
}

// settleStep is the state a per-run DNSZone and its Domain reach, in order.
type settleStep struct {
	name             string
	zoneNameservers  []string
	zoneConditions   []metav1.Condition
	domainNameserver []string
	domainConditions []metav1.Condition
	want             metav1.ConditionStatus
	reason           string
	hostnameReason   string // the hostname's own reason while it has no record
}

// TestGatewayDNSWhileZoneAndDomainSettle replays the order of infra's
// hostname-dns-reap test: a per-run DNSZone and the Domain made for it settle
// while a Gateway claims a hostname in the zone. Until the Domain is verified
// the hostname waits on Datum alone, so the Gateway must report False/Pending,
// not a failure (#537).
func TestGatewayDNSWhileZoneAndDomainSettle(t *testing.T) {
	const zoneDomain, hostname = "run.example.com", "app.run.example.com"
	cl := settleClient(t, false, settlingZone(zoneDomain, nil), settlingDomain(zoneDomain, false, nil))
	gw := newTestGatewayForDNS(settleNamespace, "proxy")

	for _, step := range []settleStep{
		{
			name: "zone and Domain created",
			want: metav1.ConditionFalse, reason: dnsPendingReason, hostnameReason: networkingv1alpha.DNSRecordReasonPending,
		},
		{
			name:            "zone accepted and programmed, Domain not reconciled yet",
			zoneNameservers: testNameservers, zoneConditions: readyZoneConditions,
			want: metav1.ConditionFalse, reason: dnsPendingReason, hostnameReason: networkingv1alpha.DNSRecordReasonPending,
		},
		{
			name:            "Domain waits for its nameservers before the zone check",
			zoneNameservers: testNameservers, zoneConditions: readyZoneConditions,
			domainConditions: []metav1.Condition{domainNotVerified, zoneCheckWaits},
			want:             metav1.ConditionFalse, reason: dnsPendingReason, hostnameReason: networkingv1alpha.DNSRecordReasonPending,
		},
		{
			name:            "Domain verified through the zone",
			zoneNameservers: testNameservers, zoneConditions: readyZoneConditions,
			domainNameserver: testNameservers, domainConditions: domainByZoneCondition,
			want: metav1.ConditionTrue, reason: networkingv1alpha.DNSRecordsProgrammedReasonAllCreated,
		},
	} {
		settleZoneAndDomain(t, cl, zoneDomain, step)
		statuses, got := gatewayDNSPass(t, cl, gw, []string{hostname})
		logHostnames(t, step.name, statuses)
		assertAggregate(t, got, step.want, step.reason)
		assertHostnameReasons(t, statuses, step.hostnameReason)
	}
}

// settleZoneAndDomain writes one step's state over the stored zone and Domain.
func settleZoneAndDomain(t *testing.T, cl client.Client, zoneDomain string, step settleStep) {
	t.Helper()
	ctx := context.Background()
	var zone dnsv1alpha1.DNSZone
	require.NoError(t, cl.Get(ctx, client.ObjectKey{Namespace: settleNamespace, Name: "zone-" + zoneDomain}, &zone))
	zone.Status = settlingZone(zoneDomain, step.zoneNameservers, step.zoneConditions...).Status
	require.NoError(t, cl.Update(ctx, &zone))
	var domain networkingv1alpha.Domain
	require.NoError(t, cl.Get(ctx, client.ObjectKey{Namespace: settleNamespace, Name: zoneDomain}, &domain))
	domain.Status = settlingDomain(zoneDomain, false, step.domainNameserver, step.domainConditions...).Status
	require.NoError(t, cl.Update(ctx, &domain))
}

// dnsClassCase is one state of a Gateway's hostnames and what the Gateway's
// DNSRecordsProgrammed reads for it.
type dnsClassCase struct {
	name           string
	hostnames      []string
	objects        []client.Object
	refuse         bool
	want           metav1.ConditionStatus
	reason         string
	hostnameReason string // each hostname's own reason, when they share one
}

// TestGatewayDNSRecordsProgrammedByClass covers each class of hostname: one
// that waits on Datum (False/Pending), one that waits on the customer, and one
// that has failed (both False/PartialFailure). One hostname that waits on the
// customer or has failed makes the Gateway report a failure, because only the
// customer or an operator can change it.
func TestGatewayDNSRecordsProgrammedByClass(t *testing.T) {
	var cases []dnsClassCase
	for _, group := range [][]dnsClassCase{datumPendingCases(), customerCases(), failedCases(), mixedCases()} {
		cases = append(cases, group...)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gw := newTestGatewayForDNS(settleNamespace, "proxy")
			statuses, got := gatewayDNSPass(t, settleClient(t, tc.refuse, tc.objects...), gw, tc.hostnames)
			logHostnames(t, tc.name, statuses)
			assertAggregate(t, got, tc.want, tc.reason)
			assertHostnameReasons(t, statuses, tc.hostnameReason)
		})
	}
}

func datumPendingCases() []dnsClassCase {
	pending := func(name, hostnameReason string, objects ...client.Object) dnsClassCase {
		return dnsClassCase{name: name, hostnames: []string{"www.example.com"}, objects: objects,
			want: metav1.ConditionFalse, reason: dnsPendingReason, hostnameReason: hostnameReason}
	}
	return []dnsClassCase{
		pending("Datum: the Domain waits for its zone to be accepted and programmed", networkingv1alpha.DNSRecordReasonZoneNotReady,
			settlingZone("example.com", nil),
			settlingDomain("example.com", true, nil, domainNotVerified, zoneCheckZoneNotReady)),
		pending("Datum: the zone is not programmed yet", networkingv1alpha.DNSRecordReasonZoneNotReady, pendingDNS("example.com")...),
		pending("Datum: the zone has no nameservers yet", networkingv1alpha.DNSRecordReasonZoneNotReady,
			settlingZone("example.com", nil, readyZoneConditions...),
			settlingDomain("example.com", true, testNameservers, domainVerified, verifiedByTXT)),
		pending("Datum: the Domain has no nameservers yet", networkingv1alpha.DNSRecordReasonPending,
			settlingZone("example.com", testNameservers, readyZoneConditions...),
			settlingDomain("example.com", true, nil, domainVerified, verifiedByTXT)),
	}
}

func customerCases() []dnsClassCase {
	customers := func(name, hostnameReason string, objects ...client.Object) dnsClassCase {
		return dnsClassCase{name: name, hostnames: []string{"www.example.com"}, objects: objects,
			want: metav1.ConditionFalse, reason: networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure, hostnameReason: hostnameReason}
	}
	return []dnsClassCase{
		customers("customer: the registrar does not delegate to the zone", networkingv1alpha.DNSRecordReasonDNSAuthorityMissing,
			settlingZone("example.com", testNameservers, readyZoneConditions...),
			settlingDomain("example.com", true, registrarNameservers, domainVerified, verifiedByTXT)),
		customers("customer: another project's zone holds the domain", networkingv1alpha.DNSRecordReasonDNSAuthorityMissing,
			settlingZone("example.com", testNameservers, zoneRefusedInUse, zoneNotProgrammed),
			settlingDomain("example.com", true, testNameservers, domainVerified, verifiedByTXT)),
		customers("customer: no zone, and no TXT record or HTTP token", networkingv1alpha.DNSRecordReasonDomainNotVerified,
			settlingDomain("example.com", true, nil, domainNotVerified, zoneCheckFindsNone, txtRecordNotFound)),
		customers("customer: a zone, and a registrar that does not delegate to it", networkingv1alpha.DNSRecordReasonDomainNotVerified,
			settlingZone("example.com", testNameservers, readyZoneConditions...),
			settlingDomain("example.com", true, registrarNameservers, domainNotVerified, zoneCheckMismatch, txtRecordNotFound)),
		{
			name: "customer: a subdomain zone held until a TXT record or HTTP token verifies it", hostnames: []string{"app.run.example.com"},
			objects: []client.Object{
				settlingZone("run.example.com", testNameservers, zoneHeldForVerification),
				settlingDomain("run.example.com", false, testNameservers, domainNotVerified, zoneCheckZoneNotReady, txtRecordNotFound),
			},
			want: metav1.ConditionFalse, reason: networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure,
			hostnameReason: networkingv1alpha.DNSRecordReasonDomainNotVerified,
		},
	}
}

func failedCases() []dnsClassCase {
	return []dnsClassCase{
		{
			name: "failed: another manager's record set holds the name", hostnames: []string{"www.example.com"},
			objects: append(readyDNS("example.com"), foreignRecord("example.com", "www.example.com")),
			want:    metav1.ConditionFalse, reason: networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure,
		},
		{
			name: "failed: dns-operator refuses the record set", hostnames: []string{"www.example.com"},
			objects: readyDNS("example.com"), refuse: true,
			want: metav1.ConditionFalse, reason: networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure,
		},
	}
}

func mixedCases() []dnsClassCase {
	two := []string{"www.example.com", "www.example.org"}
	return []dnsClassCase{
		{
			name: "mixed: one waits on Datum, one programmed", hostnames: two,
			objects: append(readyDNS("example.org"), pendingDNS("example.com")...),
			want:    metav1.ConditionFalse, reason: dnsPendingReason,
		},
		{
			name: "mixed: one waits on Datum, one on the customer", hostnames: two,
			objects: append(pendingDNS("example.org"),
				settlingZone("example.com", testNameservers, readyZoneConditions...),
				settlingDomain("example.com", true, registrarNameservers, domainVerified, verifiedByTXT)),
			want: metav1.ConditionFalse, reason: networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure,
		},
		{
			name: "mixed: one waits on Datum, one failed", hostnames: two,
			objects: append(append(readyDNS("example.com"), foreignRecord("example.com", "www.example.com")), pendingDNS("example.org")...),
			want:    metav1.ConditionFalse, reason: networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure,
		},
	}
}

// TestGatewayDNSReturnsAFailedZoneRead covers a hostname whose Domain is not
// verified while the read of its zones fails. The Gateway returns the error to
// be retried. It never takes the failure for a Domain without a zone, which
// would read as a wait on the customer.
func TestGatewayDNSReturnsAFailedZoneRead(t *testing.T) {
	cl := settleClientWith(t, interceptor.Funcs{List: failZoneLists},
		settlingZone("example.com", nil),
		settlingDomain("example.com", true, nil, domainNotVerified, zoneCheckZoneNotReady))
	gw := newTestGatewayForDNS(settleNamespace, "proxy")
	_, result, _ := newDNSReconciler(settleConfig).ensureDNSRecordSets(context.Background(), cl, gw, []string{"www.example.com"})
	assert.ErrorContains(t, result.Err, "the cache cannot list DNSZones")
}

// TestGatewayWithoutDatumDNSHostnameCarriesNoDNSRecordsProgrammed covers a
// Gateway whose only hostnames are its own Datum-managed addresses. It has
// nothing for Datum DNS to program, so it must not keep a DNSRecordsProgrammed
// written for an earlier state, which its HTTPProxy would report.
func TestGatewayWithoutDatumDNSHostnameCarriesNoDNSRecordsProgrammed(t *testing.T) {
	rig := newClaimRig(t, func(defaults []gatewayv1.Listener) []gatewayv1.Listener { return defaults })
	rig.reconciler.Config.Gateway.EnableDNSIntegration = true

	var gw gatewayv1.Gateway
	require.NoError(t, rig.upstream.Get(rig.ctx, rig.gateway, &gw))
	apimeta.SetStatusCondition(&gw.Status.Conditions, metav1.Condition{
		Type:    networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed,
		Status:  metav1.ConditionFalse,
		Reason:  networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure,
		Message: "0/2 hostnames have DNS records programmed; see per-hostname conditions for details",
	})
	require.NoError(t, rig.upstream.Status().Update(rig.ctx, &gw))

	rig.reconcileOnce()

	require.NoError(t, rig.upstream.Get(rig.ctx, rig.gateway, &gw))
	assert.Nil(t, apimeta.FindStatusCondition(gw.Status.Conditions, networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed))
}

// dnsRecords is an HTTPProxy's DNSRecordsProgrammed as carried from its
// Gateway.
func dnsRecords(status metav1.ConditionStatus, reason string) []metav1.Condition {
	return []metav1.Condition{{Type: networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed, Status: status, Reason: reason}}
}

// TestDNSRecordFailedWarnsAsCertificateFailedDoes pins the DNSRecordFailed
// Warning to the rule of its sibling certificateFailed: it fires when the
// stored condition is absent or holds anything other than the same failure.
// A pending aggregate is not a failure, so it never fires.
func TestDNSRecordFailedWarnsAsCertificateFailedDoes(t *testing.T) {
	failed := dnsRecords(metav1.ConditionFalse, networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure)
	pending := dnsRecords(metav1.ConditionFalse, dnsPendingReason)
	for _, step := range []struct {
		name              string
		previous, current []metav1.Condition
		warns             bool
	}{
		{name: "absent, then a failure", current: failed, warns: true},
		{name: "a failure, then the same failure", previous: failed, current: failed},
		{name: "not read yet, then a failure", previous: dnsRecords(metav1.ConditionUnknown, dnsPendingReason), current: failed, warns: true},
		{name: "pending, then a failure", previous: pending, current: failed, warns: true},
		{name: "absent, then pending", current: pending},
		{name: "a failure, then pending", previous: failed, current: pending},
	} {
		assert.Equal(t, step.warns, dnsRecordFailed(step.previous, step.current), step.name)
	}
}
