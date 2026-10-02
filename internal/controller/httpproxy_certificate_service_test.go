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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	certificatesv1alpha1 "go.datum.net/network-services-operator/internal/certificates/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
)

func TestBuildCertificateStatusesCertificateService(t *testing.T) {
	t.Parallel()

	testScheme := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(testScheme))
	require.NoError(t, gatewayv1.Install(testScheme))
	require.NoError(t, networkingv1alpha.AddToScheme(testScheme))
	require.NoError(t, certificatesv1alpha1.AddToScheme(testScheme))

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test-ns", UID: types.UID("ns-uid")}}
	downstreamNamespaceName := "ns-" + string(ns.UID)

	gateway := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: "my-proxy", Namespace: ns.Name},
		Spec: gatewayv1.GatewaySpec{Listeners: []gatewayv1.Listener{{
			Name:     "https-hostname-0",
			Protocol: gatewayv1.HTTPSProtocolType,
			Hostname: ptr.To(gatewayv1.Hostname("app.example.com")),
		}}},
	}
	httpProxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: "my-proxy", Namespace: ns.Name, Generation: 3},
		Spec:       networkingv1alpha.HTTPProxySpec{Hostnames: []gatewayv1.Hostname{"app.example.com"}},
	}
	certName := tlsCertificateName("my-proxy", "https-hostname-0")

	tlsCert := func(conditions ...metav1.Condition) *certificatesv1alpha1.TLSCertificate {
		return &certificatesv1alpha1.TLSCertificate{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns.Name, Name: certName},
			Status:     certificatesv1alpha1.TLSCertificateStatus{Conditions: conditions},
		}
	}
	legacyReady := func() *unstructured.Unstructured {
		cert := newUnstructuredForGVK(certificateGVK)
		cert.SetNamespace(downstreamNamespaceName)
		cert.SetName(listenerCertificateName("my-proxy", "https-hostname-0"))
		_ = unstructured.SetNestedSlice(cert.Object, []any{map[string]any{"type": "Ready", "status": "True"}}, "status", "conditions")
		return cert
	}

	crt, key := generateTLSKeyPair(t, "app.example.com", time.Now().Add(-time.Hour), time.Now().Add(30*24*time.Hour))
	servingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: downstreamNamespaceName, Name: listenerCertificateSecretName("my-proxy", "https-hostname-0")},
		Type:       corev1.SecretTypeTLS,
		Data:       map[string][]byte{"tls.crt": crt, "tls.key": key},
	}

	tests := []struct {
		name        string
		upstream    []client.Object
		mutate      func(*certificatesv1alpha1.TLSCertificate)
		downstream  []client.Object
		wantStatus  metav1.ConditionStatus
		wantReason  string
		wantMessage string
	}{
		{
			name:       "Ready maps to CertificateIssued",
			upstream:   []client.Object{tlsCert(metav1.Condition{Type: certificatesv1alpha1.ConditionReady, Status: metav1.ConditionTrue})},
			wantStatus: metav1.ConditionTrue,
			wantReason: networkingv1alpha.CertificateReadyReasonCertificateIssued,
		},
		{
			name: "Accepted=False maps to ProvisioningFailed with the service's message",
			upstream: []client.Object{tlsCert(metav1.Condition{
				Type: certificatesv1alpha1.ConditionAccepted, Status: metav1.ConditionFalse, Reason: "DeniedDomain", Message: "names under datum.net are denied",
			})},
			wantStatus:  metav1.ConditionFalse,
			wantReason:  networkingv1alpha.CertificateReadyReasonProvisioningFailed,
			wantMessage: "names under datum.net are denied",
		},
		{
			name: "Issuing maps to ChallengeInProgress",
			upstream: []client.Object{tlsCert(
				metav1.Condition{Type: certificatesv1alpha1.ConditionAccepted, Status: metav1.ConditionTrue},
				metav1.Condition{Type: certificatesv1alpha1.ConditionIssuing, Status: metav1.ConditionTrue, Reason: "OrderInFlight"},
			)},
			wantStatus: metav1.ConditionFalse,
			wantReason: networkingv1alpha.CertificateReadyReasonChallengeInProgress,
		},
		{
			name:       "accepted but not yet issuing is Pending",
			upstream:   []client.Object{tlsCert(metav1.Condition{Type: certificatesv1alpha1.ConditionAccepted, Status: metav1.ConditionTrue})},
			wantStatus: metav1.ConditionFalse,
			wantReason: networkingv1alpha.CertificateReadyReasonPending,
		},
		{
			name: "Issuing with required DNS records names them for the customer",
			upstream: []client.Object{tlsCert(
				metav1.Condition{Type: certificatesv1alpha1.ConditionIssuing, Status: metav1.ConditionTrue, Reason: "OrderInFlight"},
			), nil}[:1],
			mutate: func(c *certificatesv1alpha1.TLSCertificate) {
				c.Status.RequiredDNSRecords = []certificatesv1alpha1.RequiredDNSRecord{{Name: "_acme-challenge.app.example.com", Type: "CNAME", Content: "abc.acme-dns.example.net", Purpose: certificatesv1alpha1.DNSRecordPurposeCertificate}}
			},
			wantStatus:  metav1.ConditionFalse,
			wantReason:  networkingv1alpha.CertificateReadyReasonChallengeInProgress,
			wantMessage: certificateProvisioningMessage + ". Publish these DNS records to continue: _acme-challenge.app.example.com CNAME abc.acme-dns.example.net",
		},
		{
			name:        "not Ready yet but a serving downstream Secret keeps the hostname ready",
			upstream:    []client.Object{tlsCert(metav1.Condition{Type: certificatesv1alpha1.ConditionIssuing, Status: metav1.ConditionTrue})},
			downstream:  []client.Object{servingSecret},
			wantStatus:  metav1.ConditionTrue,
			wantReason:  networkingv1alpha.CertificateReadyReasonCertificateIssued,
			wantMessage: "Certificate is ready; a renewal is in progress",
		},
		{
			name:       "no TLSCertificate yet but a ready legacy Certificate answers",
			downstream: []client.Object{legacyReady()},
			wantStatus: metav1.ConditionTrue,
			wantReason: networkingv1alpha.CertificateReadyReasonCertificateIssued,
		},
		{
			name:        "nothing requested yet is Pending",
			wantStatus:  metav1.ConditionFalse,
			wantReason:  networkingv1alpha.CertificateReadyReasonPending,
			wantMessage: certificateProvisioningMessage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.mutate != nil {
				tt.mutate(tt.upstream[0].(*certificatesv1alpha1.TLSCertificate))
			}
			upstreamClient := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(ns).WithObjects(tt.upstream...).Build()
			downstreamClient := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(tt.downstream...).Build()

			r := &HTTPProxyReconciler{
				Config: config.NetworkServicesOperator{Gateway: config.GatewayConfig{
					TargetDomain:       "example.net",
					CertificateService: config.CertificateServiceConfig{Enabled: true},
				}},
				DownstreamCluster: &clusterWithClient{c: downstreamClient, scheme: testScheme},
			}

			got := r.buildCertificateStatuses(context.Background(), upstreamClient, "local", gateway, httpProxy)
			require.Len(t, got, 1)
			assert.Equal(t, "app.example.com", got[0].Hostname)
			cond := apimeta.FindStatusCondition(got[0].Conditions, networkingv1alpha.HostnameConditionCertificateReady)
			require.NotNil(t, cond)
			assert.Equal(t, tt.wantStatus, cond.Status)
			assert.Equal(t, tt.wantReason, cond.Reason)
			assert.Equal(t, int64(3), cond.ObservedGeneration)
			if tt.wantMessage != "" {
				assert.Equal(t, tt.wantMessage, cond.Message)
			}
		})
	}
}
