// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	certificatesv1alpha1 "go.datum.net/network-services-operator/internal/certificates/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"

	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"
)

const testCanonicalHostname = "ruth-fourth-hrkgk.datumproxy.net"

type fakeDNS struct {
	cnames map[string]string
	addrs  map[string][]string
	calls  int
}

func (f *fakeDNS) observer(now time.Time) *routingObserver {
	return &routingObserver{
		lookupCNAME: func(_ context.Context, host string) (string, error) {
			f.calls++
			if target, ok := f.cnames[host]; ok {
				return target, nil
			}
			return "", errors.New("no such host")
		},
		lookupIP: func(_ context.Context, host string) ([]net.IPAddr, error) {
			f.calls++
			out := make([]net.IPAddr, 0, len(f.addrs[host]))
			for _, a := range f.addrs[host] {
				out = append(out, net.IPAddr{IP: net.ParseIP(a)})
			}
			if len(out) == 0 {
				return nil, errors.New("no such host")
			}
			return out, nil
		},
		now:   func() time.Time { return now },
		cache: map[string]routingObservation{},
	}
}

func dnsRecordsTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, gatewayv1.Install(s))
	require.NoError(t, networkingv1alpha.AddToScheme(s))
	require.NoError(t, certificatesv1alpha1.AddToScheme(s))
	require.NoError(t, dnsv1alpha1.AddToScheme(s))
	return s
}

func dnsRecordsGateway(hostnames ...string) *gatewayv1.Gateway {
	gw := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns", Name: "s3"}}
	for i, h := range hostnames {
		gw.Spec.Listeners = append(gw.Spec.Listeners,
			gatewayv1.Listener{Name: gatewayv1.SectionName("http-hostname-" + string(rune('0'+i))), Protocol: gatewayv1.HTTPProtocolType, Hostname: ptr.To(gatewayv1.Hostname(h))},
			gatewayv1.Listener{Name: gatewayv1.SectionName("https-hostname-" + string(rune('0'+i))), Protocol: gatewayv1.HTTPSProtocolType, Hostname: ptr.To(gatewayv1.Hostname(h))},
		)
	}
	return gw
}

func dnsRecordsProxy(hostnames ...string) *networkingv1alpha.HTTPProxy {
	p := &networkingv1alpha.HTTPProxy{ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns", Name: "s3"}}
	for _, h := range hostnames {
		p.Spec.Hostnames = append(p.Spec.Hostnames, gatewayv1.Hostname(h))
	}
	p.Status.CanonicalHostname = testCanonicalHostname
	return p
}

func verifiedDomain(name string, mutate ...func(*networkingv1alpha.Domain)) *networkingv1alpha.Domain {
	d := &networkingv1alpha.Domain{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns", Name: name},
		Spec:       networkingv1alpha.DomainSpec{DomainName: name},
		Status: networkingv1alpha.DomainStatus{Conditions: []metav1.Condition{{
			Type: networkingv1alpha.DomainConditionVerified, Status: metav1.ConditionTrue, Reason: networkingv1alpha.DomainReasonVerified,
		}}},
	}
	for _, m := range mutate {
		m(d)
	}
	return d
}

func pendingDomain(name, token string) *networkingv1alpha.Domain {
	return &networkingv1alpha.Domain{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns", Name: name},
		Spec:       networkingv1alpha.DomainSpec{DomainName: name},
		Status: networkingv1alpha.DomainStatus{
			Conditions: []metav1.Condition{{Type: networkingv1alpha.DomainConditionVerified, Status: metav1.ConditionFalse, Reason: networkingv1alpha.DomainReasonPendingVerification}},
			Verification: &networkingv1alpha.DomainVerificationStatus{
				DNSRecord: networkingv1alpha.DNSVerificationRecord{Name: "datum-custom-hostname." + name, Type: "TXT", Content: token},
			},
		},
	}
}

func dns01Certificate(delegationReady bool, records ...certificatesv1alpha1.RequiredDNSRecord) *certificatesv1alpha1.TLSCertificate {
	status := metav1.ConditionFalse
	if delegationReady {
		status = metav1.ConditionTrue
	}
	return &certificatesv1alpha1.TLSCertificate{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns", Name: tlsCertificateName("s3", "https-hostname-0")},
		Status: certificatesv1alpha1.TLSCertificateStatus{
			Issuance:           certificatesv1alpha1.ChallengeTypeDNS01,
			DelegationTarget:   "k3f9q2x7.acme-dns.example.net",
			RequiredDNSRecords: records,
			Conditions:         []metav1.Condition{{Type: certificatesv1alpha1.ConditionDNSDelegationReady, Status: status, Reason: "Checked"}},
		},
	}
}

func recordsByPurpose(statuses []networkingv1alpha.HostnameStatus, hostname string) map[networkingv1alpha.HostnameDNSRecordPurpose]networkingv1alpha.HostnameDNSRecord {
	out := map[networkingv1alpha.HostnameDNSRecordPurpose]networkingv1alpha.HostnameDNSRecord{}
	for _, hs := range statuses {
		if hs.Hostname != hostname {
			continue
		}
		for _, r := range hs.DNSRecords {
			out[r.Purpose] = r
		}
	}
	return out
}

func TestBuildDNSRecordStatuses(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	datumZone := &dnsv1alpha1.DNSZone{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns", Name: "example-com"},
		Spec:       dnsv1alpha1.DNSZoneSpec{DomainName: "example.com"},
		Status: dnsv1alpha1.DNSZoneStatus{
			Nameservers: []string{"ns1.datumdomains.net"},
			Conditions: []metav1.Condition{
				{Type: "Accepted", Status: metav1.ConditionTrue, Reason: "Accepted"},
				{Type: "Programmed", Status: metav1.ConditionTrue, Reason: "Programmed"},
			},
		},
	}
	platformRecordSet := func(hostname string, programmed bool) *dnsv1alpha1.DNSRecordSet {
		status := metav1.ConditionFalse
		if programmed {
			status = metav1.ConditionTrue
		}
		return &dnsv1alpha1.DNSRecordSet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   "test-ns",
				Name:        dnsRecordSetName("s3", hostname),
				Labels:      map[string]string{labelManagedBy: labelManagedByValue},
				Annotations: map[string]string{annotationDNSHostname: hostname},
			},
			Spec: dnsv1alpha1.DNSRecordSetSpec{
				DNSZoneRef: corev1.LocalObjectReference{Name: "example-com"},
				RecordType: dnsv1alpha1.RRTypeCNAME,
			},
			Status: dnsv1alpha1.DNSRecordSetStatus{Conditions: []metav1.Condition{{Type: conditionTypeProgrammed, Status: status, Reason: "Test"}}},
		}
	}
	delegatedToDatum := func(d *networkingv1alpha.Domain) {
		d.Status.Nameservers = []networkingv1alpha.Nameserver{{Hostname: "ns1.datumdomains.net"}}
	}
	delegatedElsewhere := func(d *networkingv1alpha.Domain) {
		d.Status.Nameservers = []networkingv1alpha.Nameserver{{Hostname: "ns1.otherdns.example"}}
	}

	tests := []struct {
		name          string
		disabled      bool
		dnsIntegrated bool
		hostnames     []string
		objects       []client.Object
		dns           fakeDNS
		wantRecheck   bool
		assert        func(t *testing.T, statuses []networkingv1alpha.HostnameStatus)
	}{
		{
			name:      "nothing is published with the certificate service off",
			disabled:  true,
			hostnames: []string{"*.s3.example.com"},
			objects:   []client.Object{verifiedDomain("example.com")},
			assert: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				assert.Empty(t, statuses)
			},
		},
		{
			name:        "a routing record the user has not published is missing and rechecked",
			hostnames:   []string{"www.example.com"},
			objects:     []client.Object{verifiedDomain("example.com")},
			wantRecheck: true,
			assert: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				got := recordsByPurpose(statuses, "www.example.com")
				assert.Equal(t, networkingv1alpha.HostnameDNSRecord{
					Name: "www.example.com", Type: "CNAME", Content: testCanonicalHostname,
					Purpose: networkingv1alpha.HostnameDNSRecordPurposeRouting, ManagedBy: networkingv1alpha.HostnameDNSRecordManagedByUser, State: networkingv1alpha.HostnameDNSRecordMissing,
				}, got[networkingv1alpha.HostnameDNSRecordPurposeRouting])
				assert.NotContains(t, got, networkingv1alpha.HostnameDNSRecordPurposeCertificate)
				assert.NotContains(t, got, networkingv1alpha.HostnameDNSRecordPurposeOwnership)
			},
		},
		{
			name:        "an exact hostname never lists a certificate record, since cert-manager issues it",
			hostnames:   []string{"www.example.com"},
			objects:     []client.Object{verifiedDomain("example.com"), dns01Certificate(false)},
			wantRecheck: true,
			assert: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				assert.NotContains(t, recordsByPurpose(statuses, "www.example.com"), networkingv1alpha.HostnameDNSRecordPurposeCertificate)
			},
		},
		{
			name:      "a wildcard routes when a name beneath it resolves to the canonical hostname",
			hostnames: []string{"*.s3.example.com"},
			objects: []client.Object{
				verifiedDomain("example.com"),
				dns01Certificate(true),
			},
			dns: fakeDNS{cnames: map[string]string{"datum-routing-probe.s3.example.com.": testCanonicalHostname + "."}},
			assert: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				got := recordsByPurpose(statuses, "*.s3.example.com")
				assert.Equal(t, "*.s3.example.com", got[networkingv1alpha.HostnameDNSRecordPurposeRouting].Name)
				assert.Equal(t, networkingv1alpha.HostnameDNSRecordPresent, got[networkingv1alpha.HostnameDNSRecordPurposeRouting].State)
				assert.Equal(t, networkingv1alpha.HostnameDNSRecord{
					Name: "_acme-challenge.s3.example.com", Type: "CNAME", Content: "k3f9q2x7.acme-dns.example.net",
					Purpose: networkingv1alpha.HostnameDNSRecordPurposeCertificate, ManagedBy: networkingv1alpha.HostnameDNSRecordManagedByUser, State: networkingv1alpha.HostnameDNSRecordPresent,
				}, got[networkingv1alpha.HostnameDNSRecordPurposeCertificate])
			},
		},
		{
			name:      "a flattened apex record that resolves to the platform's addresses is present",
			hostnames: []string{"example.com"},
			objects:   []client.Object{verifiedDomain("example.com", func(d *networkingv1alpha.Domain) { d.Status.Apex = true })},
			dns: fakeDNS{addrs: map[string][]string{
				"example.com.":              {"203.0.113.10"},
				testCanonicalHostname + ".": {"203.0.113.10", "2001:db8::10"},
			}},
			assert: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				got := recordsByPurpose(statuses, "example.com")[networkingv1alpha.HostnameDNSRecordPurposeRouting]
				assert.Equal(t, "ALIAS", got.Type)
				assert.Equal(t, networkingv1alpha.HostnameDNSRecordPresent, got.State)
			},
		},
		{
			name:        "an address that is not the platform's leaves the routing record missing",
			hostnames:   []string{"www.example.com"},
			objects:     []client.Object{verifiedDomain("example.com")},
			dns:         fakeDNS{addrs: map[string][]string{"www.example.com.": {"198.51.100.7"}, testCanonicalHostname + ".": {"203.0.113.10"}}},
			wantRecheck: true,
			assert: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				assert.Equal(t, networkingv1alpha.HostnameDNSRecordMissing, recordsByPurpose(statuses, "www.example.com")[networkingv1alpha.HostnameDNSRecordPurposeRouting].State)
			},
		},
		{
			name:      "the certificate record is missing until the delegation resolves",
			hostnames: []string{"*.s3.example.com"},
			objects: []client.Object{
				verifiedDomain("example.com"),
				dns01Certificate(false, certificatesv1alpha1.RequiredDNSRecord{
					Name: "_acme-challenge.s3.example.com", Type: "CNAME", Content: "k3f9q2x7.acme-dns.example.net.", Purpose: certificatesv1alpha1.DNSRecordPurposeCertificate,
				}),
			},
			dns: fakeDNS{cnames: map[string]string{"datum-routing-probe.s3.example.com.": testCanonicalHostname + "."}},
			assert: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				got := recordsByPurpose(statuses, "*.s3.example.com")[networkingv1alpha.HostnameDNSRecordPurposeCertificate]
				assert.Equal(t, "k3f9q2x7.acme-dns.example.net", got.Content)
				assert.Equal(t, networkingv1alpha.HostnameDNSRecordMissing, got.State)
			},
		},
		{
			name:      "a required record for another name is not passed on",
			hostnames: []string{"www.example.com"},
			objects: []client.Object{
				verifiedDomain("example.com"),
				func() client.Object {
					c := dns01Certificate(false, certificatesv1alpha1.RequiredDNSRecord{
						Name: "_acme-challenge.victim.example.org", Type: "CNAME", Content: "attacker.example.net", Purpose: certificatesv1alpha1.DNSRecordPurposeCertificate,
					})
					c.Status.Issuance = certificatesv1alpha1.ChallengeTypeHTTP01
					c.Status.DelegationTarget = ""
					return c
				}(),
			},
			wantRecheck: true,
			assert: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				assert.NotContains(t, recordsByPurpose(statuses, "www.example.com"), networkingv1alpha.HostnameDNSRecordPurposeCertificate)
			},
		},
		{
			name:          "a platform record counts while Datum DNS serves the domain",
			dnsIntegrated: true,
			hostnames:     []string{"www.example.com"},
			objects:       []client.Object{verifiedDomain("example.com", delegatedToDatum), datumZone, platformRecordSet("www.example.com", true)},
			assert: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				got := recordsByPurpose(statuses, "www.example.com")[networkingv1alpha.HostnameDNSRecordPurposeRouting]
				assert.Equal(t, networkingv1alpha.HostnameDNSRecordManagedByPlatform, got.ManagedBy)
				assert.Equal(t, networkingv1alpha.HostnameDNSRecordPresent, got.State)
			},
		},
		{
			name:          "a platform record in a zone the registry does not delegate to is missing",
			dnsIntegrated: true,
			hostnames:     []string{"www.example.com"},
			objects:       []client.Object{verifiedDomain("example.com", delegatedElsewhere), datumZone, platformRecordSet("www.example.com", true)},
			assert: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				got := recordsByPurpose(statuses, "www.example.com")[networkingv1alpha.HostnameDNSRecordPurposeRouting]
				assert.Equal(t, networkingv1alpha.HostnameDNSRecordManagedByPlatform, got.ManagedBy)
				assert.Equal(t, networkingv1alpha.HostnameDNSRecordMissing, got.State)
			},
		},
		{
			name:          "a platform record not yet programmed is missing",
			dnsIntegrated: true,
			hostnames:     []string{"www.example.com"},
			objects:       []client.Object{verifiedDomain("example.com", delegatedToDatum), datumZone, platformRecordSet("www.example.com", false)},
			assert: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				assert.Equal(t, networkingv1alpha.HostnameDNSRecordMissing, recordsByPurpose(statuses, "www.example.com")[networkingv1alpha.HostnameDNSRecordPurposeRouting].State)
			},
		},
		{
			name:        "an unverified domain lists its ownership record on each hostname",
			hostnames:   []string{"www.example.com", "api.example.com"},
			objects:     []client.Object{pendingDomain("example.com", "4f1c-token")},
			wantRecheck: true,
			assert: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				want := networkingv1alpha.HostnameDNSRecord{
					Name: "datum-custom-hostname.example.com", Type: "TXT", Content: "4f1c-token",
					Purpose: networkingv1alpha.HostnameDNSRecordPurposeOwnership, ManagedBy: networkingv1alpha.HostnameDNSRecordManagedByUser, State: networkingv1alpha.HostnameDNSRecordMissing,
				}
				assert.Equal(t, want, recordsByPurpose(statuses, "www.example.com")[networkingv1alpha.HostnameDNSRecordPurposeOwnership])
				assert.Equal(t, want, recordsByPurpose(statuses, "api.example.com")[networkingv1alpha.HostnameDNSRecordPurposeOwnership])
			},
		},
		{
			name:        "a verified domain that does not cover the hostname leaves its ownership record listed",
			hostnames:   []string{"www.example.com"},
			objects:     []client.Object{verifiedDomain("example.org"), pendingDomain("example.com", "4f1c-token")},
			wantRecheck: true,
			assert: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				assert.Contains(t, recordsByPurpose(statuses, "www.example.com"), networkingv1alpha.HostnameDNSRecordPurposeOwnership)
			},
		},
		{
			name:      "platform hostnames need no records",
			hostnames: []string{"foo.datumproxy.net"},
			assert: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				assert.Empty(t, statuses)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cl := fake.NewClientBuilder().WithScheme(dnsRecordsTestScheme(t)).WithObjects(tt.objects...).Build()
			r := &HTTPProxyReconciler{
				Config: config.NetworkServicesOperator{Gateway: config.GatewayConfig{
					TargetDomain:         "datumproxy.net",
					EnableDNSIntegration: tt.dnsIntegrated,
					CertificateService:   config.CertificateServiceConfig{Enabled: !tt.disabled},
				}},
				routing: tt.dns.observer(now),
			}

			statuses, recheck := r.buildDNSRecordStatuses(context.Background(), cl, dnsRecordsGateway(tt.hostnames...), dnsRecordsProxy(tt.hostnames...))
			assert.Equal(t, tt.wantRecheck, recheck)
			tt.assert(t, statuses)
		})
	}
}

func TestRoutingObserverRemembersAnswers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	dns := &fakeDNS{cnames: map[string]string{"www.example.com.": testCanonicalHostname + "."}}
	o := dns.observer(now)

	assert.True(t, o.routesTo(context.Background(), "www.example.com", testCanonicalHostname))
	calls := dns.calls
	assert.True(t, o.routesTo(context.Background(), "www.example.com", testCanonicalHostname))
	assert.Equal(t, calls, dns.calls, "a second look inside the TTL must not hit DNS")

	o.now = func() time.Time { return now.Add(routingObservationTTL) }
	delete(dns.cnames, "www.example.com.")
	assert.False(t, o.routesTo(context.Background(), "www.example.com", testCanonicalHostname))
}

func TestMergeHostnameStatusesKeepsDNSRecords(t *testing.T) {
	t.Parallel()

	routing := networkingv1alpha.HostnameDNSRecord{Name: "a.example.com", Type: "CNAME", Content: testCanonicalHostname, Purpose: networkingv1alpha.HostnameDNSRecordPurposeRouting}
	merged := mergeHostnameStatuses(
		[]networkingv1alpha.HostnameStatus{{Hostname: "a.example.com", Conditions: []metav1.Condition{{Type: networkingv1alpha.HostnameConditionAvailable, Status: metav1.ConditionTrue}}}},
		[]networkingv1alpha.HostnameStatus{{Hostname: "a.example.com", DNSRecords: []networkingv1alpha.HostnameDNSRecord{routing}}},
	)

	require.Len(t, merged, 1)
	assert.Len(t, merged[0].Conditions, 1)
	assert.Equal(t, []networkingv1alpha.HostnameDNSRecord{routing}, merged[0].DNSRecords)
}
