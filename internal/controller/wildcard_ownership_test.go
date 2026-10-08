// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
)

func domainProvenBy(name string, method string) networkingv1alpha.Domain {
	d := networkingv1alpha.Domain{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name},
		Spec:       networkingv1alpha.DomainSpec{DomainName: name},
		Status: networkingv1alpha.DomainStatus{Conditions: []metav1.Condition{{
			Type: networkingv1alpha.DomainConditionVerified, Status: metav1.ConditionTrue, Reason: networkingv1alpha.DomainReasonVerified,
		}}},
	}
	if method != "" {
		d.Status.Conditions = append(d.Status.Conditions, metav1.Condition{Type: method, Status: metav1.ConditionTrue, Reason: networkingv1alpha.DomainReasonVerified})
	}
	return d
}

func domainPending(name string) networkingv1alpha.Domain {
	return networkingv1alpha.Domain{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: name},
		Spec:       networkingv1alpha.DomainSpec{DomainName: name},
		Status: networkingv1alpha.DomainStatus{Conditions: []metav1.Condition{{
			Type: networkingv1alpha.DomainConditionVerified, Status: metav1.ConditionFalse, Reason: networkingv1alpha.DomainReasonPendingVerification,
		}}},
	}
}

func TestCheckWildcardOwnership(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		domains      []networkingv1alpha.Domain
		wantProven   bool
		wantMessage  []string
		wantCreation string
	}{
		{
			name:       "a parent verified by DNS TXT proves the wildcard",
			domains:    []networkingv1alpha.Domain{domainProvenBy("example.com", networkingv1alpha.DomainConditionVerifiedDNS)},
			wantProven: true,
		},
		{
			name:       "the base itself verified by DNS TXT proves the wildcard",
			domains:    []networkingv1alpha.Domain{domainProvenBy("s3.example.com", networkingv1alpha.DomainConditionVerifiedDNS)},
			wantProven: true,
		},
		{
			name:         "HTTP token verification is refused and a Domain for the base is offered",
			domains:      []networkingv1alpha.Domain{domainProvenBy("example.com", networkingv1alpha.DomainConditionVerifiedHTTP)},
			wantMessage:  []string{`Domain "example.com" was verified over HTTP`, `Publish the DNS TXT record shown on Domain "s3.example.com"`},
			wantCreation: "s3.example.com",
		},
		{
			name:         "a Datum DNS zone is refused until zone claims settle what it proves",
			domains:      []networkingv1alpha.Domain{domainProvenBy("example.com", networkingv1alpha.DomainConditionVerifiedDNSZone)},
			wantMessage:  []string{"through a Datum DNS zone, which does not yet count"},
			wantCreation: "s3.example.com",
		},
		{
			name:         "a Domain verified before the method was recorded is refused",
			domains:      []networkingv1alpha.Domain{domainProvenBy("example.com", "")},
			wantMessage:  []string{"before the platform recorded how"},
			wantCreation: "s3.example.com",
		},
		{
			name:        "a pending Domain is the one to verify",
			domains:     []networkingv1alpha.Domain{domainProvenBy("example.com", networkingv1alpha.DomainConditionVerifiedHTTP), domainPending("s3.example.com")},
			wantMessage: []string{`Publish the DNS TXT record shown on Domain "s3.example.com"; HTTP verification does not count`},
		},
		{
			name:        "a base verified over HTTP points at a parent",
			domains:     []networkingv1alpha.Domain{domainProvenBy("s3.example.com", networkingv1alpha.DomainConditionVerifiedHTTP)},
			wantMessage: []string{"Add a Domain for a parent of s3.example.com"},
		},
		{
			name:         "no Domain at all creates one for the registrable domain",
			wantMessage:  []string{`Domain "example.com"`},
			wantCreation: "example.com",
		},
		{
			name:         "a DNS-verified sibling proves nothing",
			domains:      []networkingv1alpha.Domain{domainProvenBy("s4.example.com", networkingv1alpha.DomainConditionVerifiedDNS), domainProvenBy("photos.s3.example.com", networkingv1alpha.DomainConditionVerifiedDNS)},
			wantCreation: "example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := checkWildcardOwnership("*.s3.example.com", tt.domains)
			assert.Equal(t, tt.wantProven, got.proven)
			assert.Equal(t, tt.wantCreation, got.createDomain)
			if tt.wantProven {
				return
			}
			assert.Equal(t, networkingv1alpha.HostnameVerifiedReasonDNSVerificationRequired, got.refusal.reason)
			for _, m := range tt.wantMessage {
				assert.Contains(t, got.refusal.message, m)
			}
		})
	}
}

func TestWildcardVerification(t *testing.T) {
	t.Parallel()

	verify := func(t *testing.T, enabled bool, downstream *gatewayv1.Gateway, domains ...networkingv1alpha.Domain) ([]string, map[string]hostnameRefusal, client.Client) {
		t.Helper()
		objects := make([]client.Object, 0, len(domains))
		for i := range domains {
			objects = append(objects, &domains[i])
		}
		upstream := fake.NewClientBuilder().WithScheme(claimsTestScheme(t)).WithObjects(objects...).Build()
		r := &GatewayReconciler{Config: config.NetworkServicesOperator{Gateway: config.GatewayConfig{
			TargetDomain:       "datumproxy.net",
			CertificateService: config.CertificateServiceConfig{Enabled: enabled},
		}}}
		verified, refusals, err := r.ensureHostnameVerification(context.Background(), upstream, claimingGateway("*.s3.example.com", "www.example.com"), downstream)
		require.NoError(t, err)
		return verified, refusals, upstream
	}

	t.Run("HTTP token proof admits exact hostnames but not the wildcard", func(t *testing.T) {
		t.Parallel()
		verified, refusals, upstream := verify(t, true, &gatewayv1.Gateway{}, domainProvenBy("example.com", networkingv1alpha.DomainConditionVerifiedHTTP))

		assert.Contains(t, verified, "www.example.com")
		assert.NotContains(t, verified, "*.s3.example.com")
		assert.Equal(t, networkingv1alpha.HostnameVerifiedReasonDNSVerificationRequired, refusals["*.s3.example.com"].reason)

		var created networkingv1alpha.Domain
		require.NoError(t, upstream.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "s3.example.com"}, &created))
		assert.Equal(t, "s3.example.com", created.Spec.DomainName)
	})

	t.Run("zone proof does not admit the wildcard", func(t *testing.T) {
		t.Parallel()
		verified, refusals, _ := verify(t, true, &gatewayv1.Gateway{}, domainProvenBy("example.com", networkingv1alpha.DomainConditionVerifiedDNSZone))

		assert.NotContains(t, verified, "*.s3.example.com")
		assert.Contains(t, refusals["*.s3.example.com"].message, "Datum DNS zone")
	})

	t.Run("DNS TXT proof admits the wildcard", func(t *testing.T) {
		t.Parallel()
		verified, refusals, _ := verify(t, true, &gatewayv1.Gateway{}, domainProvenBy("example.com", networkingv1alpha.DomainConditionVerifiedDNS))

		assert.Contains(t, verified, "*.s3.example.com")
		assert.Empty(t, refusals)
	})

	t.Run("a wildcard already serving keeps no grace once its proof is gone", func(t *testing.T) {
		t.Parallel()
		serving := &gatewayv1.Gateway{Spec: gatewayv1.GatewaySpec{Listeners: []gatewayv1.Listener{{
			Name: "http-hostname-0", Hostname: ptr.To(gatewayv1.Hostname("*.s3.example.com")),
		}}}}
		verified, _, _ := verify(t, true, serving, domainProvenBy("example.com", networkingv1alpha.DomainConditionVerifiedHTTP))

		assert.NotContains(t, verified, "*.s3.example.com")
	})

	t.Run("wildcards are refused with the certificate service off", func(t *testing.T) {
		t.Parallel()
		verified, refusals, _ := verify(t, false, &gatewayv1.Gateway{}, domainProvenBy("example.com", networkingv1alpha.DomainConditionVerifiedDNS))

		assert.NotContains(t, verified, "*.s3.example.com")
		assert.Equal(t, networkingv1alpha.HostnameVerifiedReasonWildcardNotSupported, refusals["*.s3.example.com"].reason)
	})
}

func TestBuildVerificationStatuses(t *testing.T) {
	t.Parallel()

	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Generation: 4},
		Spec:       networkingv1alpha.HTTPProxySpec{Hostnames: []gatewayv1.Hostname{"*.s3.example.com", "www.example.com", "new.example.com", "taken.example.com"}},
	}
	statuses := buildVerificationStatuses(proxy,
		sets.New[gatewayv1.Hostname]("www.example.com"),
		sets.New("taken.example.com"),
		map[string]metav1.Condition{
			"*.s3.example.com": {Reason: networkingv1alpha.HostnameVerifiedReasonDNSVerificationRequired, Message: "needs DNS proof"},
			"new.example.com":  {Reason: networkingv1alpha.UnverifiedHostnamesPresent, Message: "not verified"},
		},
	)

	byName := map[string]metav1.Condition{}
	for _, hs := range statuses {
		byName[hs.Hostname] = hs.Conditions[0]
	}
	assert.Equal(t, metav1.ConditionFalse, byName["*.s3.example.com"].Status)
	assert.Equal(t, networkingv1alpha.HostnameVerifiedReasonDNSVerificationRequired, byName["*.s3.example.com"].Reason)
	assert.Equal(t, "needs DNS proof", byName["*.s3.example.com"].Message)
	assert.Equal(t, networkingv1alpha.HostnameVerifiedReasonPendingVerification, byName["new.example.com"].Reason)
	assert.Equal(t, metav1.ConditionTrue, byName["www.example.com"].Status)
	assert.Equal(t, metav1.ConditionTrue, byName["taken.example.com"].Status)
}

func TestOwnershipRecordForWildcards(t *testing.T) {
	t.Parallel()

	pending := domainPending("s3.example.com")
	pending.Status.Verification = &networkingv1alpha.DomainVerificationStatus{
		DNSRecord: networkingv1alpha.DNSVerificationRecord{Name: "datum-custom-hostname.s3.example.com", Type: "TXT", Content: "token"},
	}
	httpVerified := domainProvenBy("example.com", networkingv1alpha.DomainConditionVerifiedHTTP)

	record, ok := ownershipRecord("*.s3.example.com", []networkingv1alpha.Domain{httpVerified, pending})
	require.True(t, ok, "a wildcard without DNS proof lists the TXT record that would prove it")
	assert.Equal(t, "datum-custom-hostname.s3.example.com", record.Name)

	_, ok = ownershipRecord("www.example.com", []networkingv1alpha.Domain{httpVerified, pending})
	assert.False(t, ok, "an exact hostname is satisfied by HTTP proof")

	_, ok = ownershipRecord("*.s3.example.com", []networkingv1alpha.Domain{domainProvenBy("example.com", networkingv1alpha.DomainConditionVerifiedDNS), pending})
	assert.False(t, ok)
}

func TestCheckWildcardOwnershipCreatesTheRegisteredDomain(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"*.s3.example.com":     "example.com",
		"*.s3.example.co.uk":   "example.co.uk",
		"*.example.co.uk":      "example.co.uk",
		"*.a.b.example.com.au": "example.com.au",
	}
	for hostname, want := range tests {
		assert.Equal(t, want, checkWildcardOwnership(hostname, nil).createDomain, hostname)
	}
}
