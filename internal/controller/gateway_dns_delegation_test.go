// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	certificatesv1alpha1 "go.datum.net/network-services-operator/internal/certificates/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"
)

const (
	delegationHostname  = "*.s3.example.com"
	delegationChallenge = "_acme-challenge.s3.example.com"
	delegationTarget    = "k3f9q2x7.acme-dns.example.net."
)

func delegationConfig() config.NetworkServicesOperator {
	return config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			TargetDomain:         "datumproxy.net",
			EnableDNSIntegration: true,
			CertificateService:   config.CertificateServiceConfig{Enabled: true},
		},
	}
}

func delegationCertificate() *certificatesv1alpha1.TLSCertificate {
	return dns01Certificate(false, certificatesv1alpha1.RequiredDNSRecord{
		Name: delegationChallenge, Type: "CNAME", Content: delegationTarget, Purpose: certificatesv1alpha1.DNSRecordPurposeCertificate,
	})
}

func delegationFixture(t *testing.T, extra ...client.Object) (client.Client, *GatewayReconciler, *HTTPProxyReconciler, *gatewayv1.Gateway) {
	t.Helper()
	gw := dnsRecordsGateway(delegationHostname)
	gw.UID = uuid.NewUUID()
	objs := append([]client.Object{gw, delegationCertificate()}, extra...)
	cl := buildFakeUpstreamClientForDNS(dnsRecordsTestScheme(t), objs...)
	return cl, newDNSReconciler(delegationConfig()),
		&HTTPProxyReconciler{Config: delegationConfig(), routing: (&fakeDNS{}).observer(time.Now())},
		gw
}

func hostedZone() []client.Object {
	return []client.Object{newVerifiedDNSZoneDomain("test-ns", "example.com", true), newDNSZone("test-ns", "example-com", "example.com")}
}

func listRecordSets(t *testing.T, cl client.Client) []dnsv1alpha1.DNSRecordSet {
	t.Helper()
	var list dnsv1alpha1.DNSRecordSetList
	require.NoError(t, cl.List(context.Background(), &list, client.InNamespace("test-ns")))
	return list.Items
}

func certificateRecord(t *testing.T, r *HTTPProxyReconciler, cl client.Client, gw *gatewayv1.Gateway) networkingv1alpha.HostnameDNSRecord {
	t.Helper()
	statuses, _ := r.buildDNSRecordStatuses(context.Background(), cl, gw, dnsRecordsProxy(delegationHostname))
	return recordsByPurpose(statuses, delegationHostname)[networkingv1alpha.HostnameDNSRecordPurposeCertificate]
}

func ensure(t *testing.T, r *GatewayReconciler, cl client.Client, gw *gatewayv1.Gateway, claimed ...string) {
	t.Helper()
	ctx := log.IntoContext(context.Background(), zap.New())
	_, result := r.ensureDNSRecordSets(ctx, cl, gw, claimed)
	require.NoError(t, result.Err)
}

func programme(t *testing.T, cl client.Client, name string) {
	t.Helper()
	var rs dnsv1alpha1.DNSRecordSet
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: "test-ns", Name: name}, &rs))
	apimeta.SetStatusCondition(&rs.Status.Conditions, metav1.Condition{Type: conditionTypeProgrammed, Status: metav1.ConditionTrue, Reason: "Programmed"})
	require.NoError(t, cl.Update(context.Background(), &rs))
}

func TestDelegationRecordCreatedWhenDatumHostsZone(t *testing.T) {
	cl, gr, _, gw := delegationFixture(t, hostedZone()...)
	ensure(t, gr, cl, gw, delegationHostname)

	var rs dnsv1alpha1.DNSRecordSet
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: "test-ns", Name: dnsRecordSetName(gw.Name, delegationChallenge)}, &rs))
	assert.Equal(t, dnsv1alpha1.RRTypeCNAME, rs.Spec.RecordType)
	assert.Equal(t, "example-com", rs.Spec.DNSZoneRef.Name)
	require.Len(t, rs.Spec.Records, 1)
	assert.Equal(t, "_acme-challenge.s3", rs.Spec.Records[0].Name)
	assert.Equal(t, delegationTarget, rs.Spec.Records[0].CNAME.Content)
	assert.Equal(t, delegationChallenge, rs.Annotations[annotationDNSHostname])
	assert.Equal(t, labelManagedByValue, rs.Labels[labelManagedBy])
}

func TestDelegationRecordReportedPresentOnlyOnceProgrammed(t *testing.T) {
	cl, gr, hr, gw := delegationFixture(t, hostedZone()...)
	ensure(t, gr, cl, gw, delegationHostname)

	got := certificateRecord(t, hr, cl, gw)
	assert.Equal(t, networkingv1alpha.HostnameDNSRecordManagedByPlatform, got.ManagedBy)
	assert.Equal(t, networkingv1alpha.HostnameDNSRecordMissing, got.State)
	assert.Equal(t, "k3f9q2x7.acme-dns.example.net", got.Content)

	programme(t, cl, dnsRecordSetName(gw.Name, delegationChallenge))

	got = certificateRecord(t, hr, cl, gw)
	assert.Equal(t, networkingv1alpha.HostnameDNSRecordManagedByPlatform, got.ManagedBy)
	assert.Equal(t, networkingv1alpha.HostnameDNSRecordPresent, got.State)
}

func TestDelegationRecordNotCreatedForExternalZone(t *testing.T) {
	cl, gr, hr, gw := delegationFixture(t)
	ensure(t, gr, cl, gw, delegationHostname)

	assert.Empty(t, listRecordSets(t, cl))
	got := certificateRecord(t, hr, cl, gw)
	assert.Equal(t, networkingv1alpha.HostnameDNSRecordManagedByUser, got.ManagedBy)
	assert.Equal(t, networkingv1alpha.HostnameDNSRecordMissing, got.State)
}

func TestDelegationRecordNotCreatedWithCertificateServiceOff(t *testing.T) {
	cl, gr, _, gw := delegationFixture(t, hostedZone()...)
	gr.Config.Gateway.CertificateService.Enabled = false
	ensure(t, gr, cl, gw, delegationHostname)

	for _, rs := range listRecordSets(t, cl) {
		assert.NotEqual(t, delegationChallenge, rs.Annotations[annotationDNSHostname])
	}
}

func TestDelegationRecordLeavesOccupiedNameAlone(t *testing.T) {
	occupant := &dnsv1alpha1.DNSRecordSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns", Name: "customer-txt"},
		Spec: dnsv1alpha1.DNSRecordSetSpec{
			DNSZoneRef: corev1.LocalObjectReference{Name: "example-com"},
			RecordType: dnsv1alpha1.RRTypeTXT,
			Records:    []dnsv1alpha1.RecordEntry{{Name: "_acme-challenge.s3"}},
		},
	}
	cl, gr, hr, gw := delegationFixture(t, append(hostedZone(), occupant)...)
	ensure(t, gr, cl, gw, delegationHostname)

	sets := listRecordSets(t, cl)
	for _, rs := range sets {
		assert.NotEqual(t, delegationChallenge, rs.Annotations[annotationDNSHostname])
	}
	var kept dnsv1alpha1.DNSRecordSet
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: "test-ns", Name: "customer-txt"}, &kept))
	assert.Equal(t, dnsv1alpha1.RRTypeTXT, kept.Spec.RecordType)

	got := certificateRecord(t, hr, cl, gw)
	assert.Equal(t, networkingv1alpha.HostnameDNSRecordManagedByUser, got.ManagedBy)
	assert.Equal(t, networkingv1alpha.HostnameDNSRecordMissing, got.State)
}

func TestDelegationRecordRemovedWithHostnameAndRoutingUntouched(t *testing.T) {
	cl, gr, _, gw := delegationFixture(t, hostedZone()...)
	ensure(t, gr, cl, gw, delegationHostname)
	require.Len(t, listRecordSets(t, cl), 2)

	var cert certificatesv1alpha1.TLSCertificate
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: "test-ns", Name: tlsCertificateName(gw.Name, "https-hostname-0")}, &cert))
	require.NoError(t, cl.Delete(context.Background(), &cert))
	ensure(t, gr, cl, gw, delegationHostname)

	sets := listRecordSets(t, cl)
	require.Len(t, sets, 1)
	assert.Equal(t, dnsRecordSetName(gw.Name, delegationHostname), sets[0].Name)

	ensure(t, gr, cl, gw)
	assert.Empty(t, listRecordSets(t, cl))
}

func TestRoutingCleanupKeepsDelegationRecord(t *testing.T) {
	cl, gr, _, gw := delegationFixture(t, hostedZone()...)
	ensure(t, gr, cl, gw, delegationHostname)
	require.NoError(t, cl.Delete(context.Background(), &dnsv1alpha1.DNSRecordSet{ObjectMeta: metav1.ObjectMeta{Namespace: "test-ns", Name: dnsRecordSetName(gw.Name, delegationHostname)}}))

	gr.garbageCollectDNSRecordSets(context.Background(), cl, gw, map[string]bool{dnsRecordSetName(gw.Name, delegationChallenge): true})
	assert.Len(t, listRecordSets(t, cl), 1)
}

func TestOwnershipRecordIsNeverCreated(t *testing.T) {
	cl, gr, _, gw := delegationFixture(t, hostedZone()...)
	ensure(t, gr, cl, gw, delegationHostname)

	for _, rs := range listRecordSets(t, cl) {
		assert.NotEqual(t, dnsv1alpha1.RRTypeTXT, rs.Spec.RecordType)
	}
}
