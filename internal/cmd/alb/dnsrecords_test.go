// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
)

func record(name, rrType, content string, purpose networkingv1alpha.HostnameDNSRecordPurpose, by networkingv1alpha.HostnameDNSRecordManager, state networkingv1alpha.HostnameDNSRecordState) networkingv1alpha.HostnameDNSRecord {
	return networkingv1alpha.HostnameDNSRecord{Name: name, Type: rrType, Content: content, Purpose: purpose, ManagedBy: by, State: state}
}

func wildcardProxy() *networkingv1alpha.HTTPProxy {
	ownership := record("datum-custom-hostname.example.com", "TXT", "4f1c-token", networkingv1alpha.HostnameDNSRecordPurposeOwnership, networkingv1alpha.HostnameDNSRecordManagedByUser, networkingv1alpha.HostnameDNSRecordMissing)
	return &networkingv1alpha.HTTPProxy{
		Spec: networkingv1alpha.HTTPProxySpec{Hostnames: []gatewayv1.Hostname{"*.s3.example.com", "www.example.com"}},
		Status: networkingv1alpha.HTTPProxyStatus{HostnameStatuses: []networkingv1alpha.HostnameStatus{
			{
				Hostname: "*.s3.example.com",
				Conditions: []metav1.Condition{{
					Type: networkingv1alpha.HostnameConditionDNSRecordProgrammed, Status: metav1.ConditionFalse, Reason: networkingv1alpha.DNSRecordReasonDNSAuthorityMissing,
				}},
				DNSRecords: []networkingv1alpha.HostnameDNSRecord{
					record("*.s3.example.com", "CNAME", "ruth-fourth-hrkgk.datumproxy.net", networkingv1alpha.HostnameDNSRecordPurposeRouting, networkingv1alpha.HostnameDNSRecordManagedByUser, networkingv1alpha.HostnameDNSRecordMissing),
					record("_acme-challenge.s3.example.com", "CNAME", "k3f9q2x7.acme-dns.example.net", networkingv1alpha.HostnameDNSRecordPurposeCertificate, networkingv1alpha.HostnameDNSRecordManagedByUser, networkingv1alpha.HostnameDNSRecordMissing),
					ownership,
				},
			},
			{
				Hostname: "www.example.com",
				DNSRecords: []networkingv1alpha.HostnameDNSRecord{
					record("www.example.com", "CNAME", "ruth-fourth-hrkgk.datumproxy.net", networkingv1alpha.HostnameDNSRecordPurposeRouting, networkingv1alpha.HostnameDNSRecordManagedByUser, networkingv1alpha.HostnameDNSRecordPresent),
					ownership,
				},
			},
		}},
	}
}

func TestPendingRecordsListsWhatIsLeftOnce(t *testing.T) {
	proxy := wildcardProxy()
	var out bytes.Buffer
	writePendingRecords(&out, collectPendingRecords(proxy, spec.Hostnames(proxy)))
	got := out.String()

	assert.Contains(t, got, "DNS records to publish:")
	assert.Contains(t, got, "_acme-challenge.s3.example.com")
	assert.Contains(t, got, "k3f9q2x7.acme-dns.example.net")
	assert.Contains(t, got, "Certificate")
	assert.Equal(t, 1, bytes.Count(out.Bytes(), []byte("datum-custom-hostname.example.com")), "a record shared by two hostnames is listed once")
	assert.NotContains(t, got, "www.example.com  ", "a present record is not listed")
}

func TestDNSNotDelegatedPointsAtTheCertificateRecord(t *testing.T) {
	proxy := wildcardProxy()
	var out bytes.Buffer
	writePendingRecords(&out, collectPendingRecords(proxy, spec.Hostnames(proxy)))

	assert.Contains(t, out.String(), "DNS not delegated: Datum DNS does not serve *.s3.example.com yet. Publish the certificate record _acme-challenge.s3.example.com")
}

func TestDNSNotDelegatedWithoutACertificateRecord(t *testing.T) {
	proxy := wildcardProxy()
	proxy.Status.HostnameStatuses[0].DNSRecords = proxy.Status.HostnameStatuses[0].DNSRecords[:1]
	var out bytes.Buffer
	writePendingRecords(&out, collectPendingRecords(proxy, spec.Hostnames(proxy)))

	assert.Contains(t, out.String(), "see `datumctl dns`")
}

func TestPlatformRecordsAreNotTheUsersToPublish(t *testing.T) {
	proxy := &networkingv1alpha.HTTPProxy{
		Spec: networkingv1alpha.HTTPProxySpec{Hostnames: []gatewayv1.Hostname{"www.example.com"}},
		Status: networkingv1alpha.HTTPProxyStatus{HostnameStatuses: []networkingv1alpha.HostnameStatus{{
			Hostname: "www.example.com",
			DNSRecords: []networkingv1alpha.HostnameDNSRecord{
				record("www.example.com", "CNAME", "ruth-fourth-hrkgk.datumproxy.net", networkingv1alpha.HostnameDNSRecordPurposeRouting, networkingv1alpha.HostnameDNSRecordManagedByPlatform, networkingv1alpha.HostnameDNSRecordMissing),
			},
		}}},
	}
	var out bytes.Buffer
	writePendingRecords(&out, collectPendingRecords(proxy, spec.Hostnames(proxy)))

	assert.NotContains(t, out.String(), "DNS records to publish")
	assert.Contains(t, out.String(), "Waiting on Datum DNS:  www.example.com CNAME")
}

func TestHostnameProblemExplainsARefusedWildcard(t *testing.T) {
	hs := networkingv1alpha.HostnameStatus{Conditions: []metav1.Condition{{
		Type: networkingv1alpha.HostnameConditionVerified, Status: metav1.ConditionFalse,
		Reason: networkingv1alpha.HostnameVerifiedReasonDNSVerificationRequired, Message: `The wildcard "*.s3.example.com" needs DNS proof`,
	}}}
	assert.Equal(t, `The wildcard "*.s3.example.com" needs DNS proof`, hostnameProblem(hs))
	assert.Empty(t, hostnameProblem(networkingv1alpha.HostnameStatus{}))
}

func TestHostnameProblemExplainsAnUnentitledWildcard(t *testing.T) {
	hs := networkingv1alpha.HostnameStatus{Conditions: []metav1.Condition{{
		Type: networkingv1alpha.HostnameConditionCertificateReady, Status: metav1.ConditionFalse,
		Reason: networkingv1alpha.CertificateReadyReasonWildcardNotEntitled, Message: "internal wording",
	}}}
	got := hostnameProblem(hs)
	assert.Contains(t, got, "Wildcard hostnames are not enabled for this project")
	assert.Contains(t, got, "Contact Datum to enable them, or use an exact hostname.")
	assert.NotContains(t, got, "DNS")

	hs.Conditions[0].Reason = networkingv1alpha.CertificateReadyReasonPending
	assert.Empty(t, hostnameProblem(hs))
}

func TestHostnameAddAcceptsWildcards(t *testing.T) {
	updated, err := spec.AddHostname(&networkingv1alpha.HTTPProxy{ObjectMeta: metav1.ObjectMeta{Name: "s3"}}, "*.S3.example.com")
	if assert.NoError(t, err) {
		assert.Equal(t, []gatewayv1.Hostname{"*.s3.example.com"}, updated.Spec.Hostnames)
	}

	_, err = spec.AddHostname(&networkingv1alpha.HTTPProxy{ObjectMeta: metav1.ObjectMeta{Name: "s3"}}, "*.*.example.com")
	assert.Error(t, err)
}
