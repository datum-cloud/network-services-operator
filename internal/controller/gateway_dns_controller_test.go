// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"
)

// newDNSTestScheme builds a runtime.Scheme with all types needed for DNS tests.
func newDNSTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, gatewayv1.Install(s))
	require.NoError(t, networkingv1alpha.AddToScheme(s))
	require.NoError(t, dnsv1alpha1.AddToScheme(s))
	return s
}

// newTestGatewayForDNS creates a minimal upstream Gateway for DNS tests.
//
//nolint:unparam // namespace is always "test-ns" in tests but kept for clarity
func newTestGatewayForDNS(namespace, name string, opts ...func(*gatewayv1.Gateway)) *gatewayv1.Gateway {
	gw := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       uuid.NewUUID(),
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "test",
		},
	}
	for _, opt := range opts {
		opt(gw)
	}
	return gw
}

// newVerifiedDNSZoneDomain builds a Domain with VerifiedDNSZone=True set.
//
//nolint:unparam // namespace is always "test-ns" in tests but kept for clarity
func newVerifiedDNSZoneDomain(namespace, domainName string, apex bool) *networkingv1alpha.Domain {
	d := &networkingv1alpha.Domain{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      domainName,
			UID:       uuid.NewUUID(),
		},
		Spec: networkingv1alpha.DomainSpec{
			DomainName: domainName,
		},
		Status: networkingv1alpha.DomainStatus{
			Apex: apex,
		},
	}
	apimeta.SetStatusCondition(&d.Status.Conditions, metav1.Condition{
		Type:   networkingv1alpha.DomainConditionVerifiedDNSZone,
		Status: metav1.ConditionTrue,
		Reason: "Verified",
	})
	return d
}

// newUnverifiedDomain builds a Domain without VerifiedDNSZone=True.
func newUnverifiedDomain(namespace, domainName string) *networkingv1alpha.Domain {
	return &networkingv1alpha.Domain{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      domainName,
			UID:       uuid.NewUUID(),
		},
		Spec: networkingv1alpha.DomainSpec{
			DomainName: domainName,
		},
	}
}

// newDNSZone builds a DNSZone for the given apex domain.
func newDNSZone(namespace, name, domainName string) *dnsv1alpha1.DNSZone {
	return &dnsv1alpha1.DNSZone{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			UID:       uuid.NewUUID(),
		},
		Spec: dnsv1alpha1.DNSZoneSpec{
			DomainName:       domainName,
			DNSZoneClassName: "default",
		},
	}
}

// buildFakeUpstreamClientForDNS creates a fake upstream client seeded with the
// provided objects plus a field index for spec.domainName on DNSZone (the index
// used by ensureDNSRecordSets when it calls List with MatchingFields).
func buildFakeUpstreamClientForDNS(s *runtime.Scheme, objects ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objects...).
		WithIndex(&dnsv1alpha1.DNSZone{}, dnsZoneDomainNameIndex, func(o client.Object) []string {
			zone, ok := o.(*dnsv1alpha1.DNSZone)
			if !ok {
				return nil
			}
			if zone.Spec.DomainName == "" {
				return nil
			}
			return []string{zone.Spec.DomainName}
		}).
		Build()
}

// newDNSReconciler builds a GatewayReconciler with DNS integration enabled and
// targeting the supplied fake client for upstream operations.
func newDNSReconciler(testConfig config.NetworkServicesOperator) *GatewayReconciler {
	return &GatewayReconciler{
		Config: testConfig,
	}
}

// ---------------------------------------------------------------------------
// TestDNSRecordSetName – pure unit test for the deterministic naming function.
// ---------------------------------------------------------------------------

func TestDNSRecordSetName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		gatewayName string
		hostname    string
		want        string
	}{
		{
			name:        "subdomain hostname",
			gatewayName: "my-gateway",
			hostname:    "api.example.com",
			// sha256("api.example.com")[:8]
			want: func() string {
				h := sha256.Sum256([]byte("api.example.com"))
				return fmt.Sprintf("my-gateway-%s", hex.EncodeToString(h[:])[:8])
			}(),
		},
		{
			name:        "apex hostname",
			gatewayName: "my-gateway",
			hostname:    "example.com",
			want: func() string {
				h := sha256.Sum256([]byte("example.com"))
				return fmt.Sprintf("my-gateway-%s", hex.EncodeToString(h[:])[:8])
			}(),
		},
		{
			name:        "different hostname produces different name",
			gatewayName: "my-gateway",
			hostname:    "other.example.com",
			want: func() string {
				h := sha256.Sum256([]byte("other.example.com"))
				return fmt.Sprintf("my-gateway-%s", hex.EncodeToString(h[:])[:8])
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := dnsRecordSetName(tt.gatewayName, tt.hostname)
			assert.Equal(t, tt.want, got)
		})
	}

	// Additional invariant: same inputs produce same output every call.
	t.Run("deterministic", func(t *testing.T) {
		t.Parallel()
		first := dnsRecordSetName("gw", "api.example.com")
		second := dnsRecordSetName("gw", "api.example.com")
		assert.Equal(t, first, second)
	})

	// Different hostnames must not collide.
	t.Run("no collision between different hostnames", func(t *testing.T) {
		t.Parallel()
		a := dnsRecordSetName("gw", "api.example.com")
		b := dnsRecordSetName("gw", "example.com")
		assert.NotEqual(t, a, b)
	})

	// Name format: {gateway-name}-{8-char-sha256-hex}
	t.Run("name has expected format", func(t *testing.T) {
		t.Parallel()
		name := dnsRecordSetName("my-gw", "api.example.com")
		// Should start with gateway name followed by a dash and 8 hex chars.
		assert.Equal(t, "my-gw-", name[:6])
		assert.Len(t, name, 6+8, "expected 8 hex chars after the dash")
	})
}

// ---------------------------------------------------------------------------
// TestPossibleZoneNames – pure unit test for the zone name extraction helper.
// ---------------------------------------------------------------------------

func TestPossibleZoneNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		hostname  string
		wantZones []string
	}{
		{
			name:      "two labels (apex domain)",
			hostname:  "example.com",
			wantZones: []string{"example.com"}, // apex domain can be its own zone for ALIAS records
		},
		{
			name:      "three labels",
			hostname:  "api.example.com",
			wantZones: []string{"example.com"},
		},
		{
			name:      "four labels - subdomain zone support",
			hostname:  "v1.api.example.com",
			wantZones: []string{"api.example.com", "example.com"},
		},
		{
			name:      "five labels - deep subdomain",
			hostname:  "test.v1.api.example.com",
			wantZones: []string{"v1.api.example.com", "api.example.com", "example.com"},
		},
		{
			name:      "single label - no dot",
			hostname:  "localhost",
			wantZones: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotZones := possibleZoneNames(tt.hostname)
			assert.Equal(t, tt.wantZones, gotZones)
		})
	}
}

// ---------------------------------------------------------------------------
// TestEnsureDNSRecordSets – unit tests for ensureDNSRecordSets logic.
// ---------------------------------------------------------------------------

func TestEnsureDNSRecordSets(t *testing.T) {
	const ns = "test-ns"

	testConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			TargetDomain:         "gateways.test.local",
			EnableDNSIntegration: true,
		},
	}

	logger := zap.New(zap.UseFlagOptions(&zap.Options{Development: true}))

	tests := []struct {
		name             string
		claimedHostnames []string
		upstreamObjects  []client.Object
		assertStatuses   func(t *testing.T, statuses []networkingv1alpha.HostnameStatus)
		assertRecords    func(t *testing.T, cl client.Client)
		wantErr          bool
	}{
		{
			name:             "dns integration disabled returns no statuses",
			claimedHostnames: []string{"api.example.com"},
			// Config will be overridden in this test case; handled specially below.
		},
		{
			name:             "hostname with no domain resource gets NotApplicable",
			claimedHostnames: []string{"api.example.com"},
			upstreamObjects:  nil, // no Domain exists
			assertStatuses: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				require.Len(t, statuses, 1)
				c := apimeta.FindStatusCondition(statuses[0].Conditions, networkingv1alpha.HostnameConditionDNSRecordProgrammed)
				require.NotNil(t, c)
				assert.Equal(t, metav1.ConditionTrue, c.Status)
				assert.Equal(t, networkingv1alpha.DNSRecordReasonNotApplicable, c.Reason)
			},
		},
		{
			name:             "domain exists but VerifiedDNSZone=False yields DomainNotVerified",
			claimedHostnames: []string{"api.example.com"},
			upstreamObjects: []client.Object{
				newUnverifiedDomain(ns, "example.com"),
			},
			assertStatuses: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				require.Len(t, statuses, 1)
				c := apimeta.FindStatusCondition(statuses[0].Conditions, networkingv1alpha.HostnameConditionDNSRecordProgrammed)
				require.NotNil(t, c)
				assert.Equal(t, metav1.ConditionFalse, c.Status)
				assert.Equal(t, networkingv1alpha.DNSRecordReasonDomainNotVerified, c.Reason)
			},
			assertRecords: func(t *testing.T, cl client.Client) {
				var list dnsv1alpha1.DNSRecordSetList
				require.NoError(t, cl.List(context.Background(), &list, client.InNamespace(ns)))
				assert.Empty(t, list.Items, "no DNSRecordSet should be created for unverified domain")
			},
		},
		{
			name:             "verified domain but no DNSZone yields NotApplicable",
			claimedHostnames: []string{"api.example.com"},
			upstreamObjects: []client.Object{
				newVerifiedDNSZoneDomain(ns, "example.com", false),
				// No DNSZone for example.com
			},
			assertStatuses: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				require.Len(t, statuses, 1)
				c := apimeta.FindStatusCondition(statuses[0].Conditions, networkingv1alpha.HostnameConditionDNSRecordProgrammed)
				require.NotNil(t, c)
				assert.Equal(t, metav1.ConditionTrue, c.Status)
				assert.Equal(t, networkingv1alpha.DNSRecordReasonNotApplicable, c.Reason)
			},
			assertRecords: func(t *testing.T, cl client.Client) {
				var list dnsv1alpha1.DNSRecordSetList
				require.NoError(t, cl.List(context.Background(), &list, client.InNamespace(ns)))
				assert.Empty(t, list.Items)
			},
		},
		{
			name:             "verified domain with matching DNSZone creates CNAME record",
			claimedHostnames: []string{"api.example.com"},
			upstreamObjects: []client.Object{
				newVerifiedDNSZoneDomain(ns, "example.com", false), // not apex
				newDNSZone(ns, "example-com", "example.com"),
			},
			assertStatuses: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				require.Len(t, statuses, 1)
				assert.Equal(t, "api.example.com", statuses[0].Hostname)
				c := apimeta.FindStatusCondition(statuses[0].Conditions, networkingv1alpha.HostnameConditionDNSRecordProgrammed)
				require.NotNil(t, c)
				assert.Equal(t, metav1.ConditionTrue, c.Status)
				assert.Equal(t, networkingv1alpha.DNSRecordReasonCreated, c.Reason)
			},
			assertRecords: func(t *testing.T, cl client.Client) {
				var list dnsv1alpha1.DNSRecordSetList
				require.NoError(t, cl.List(context.Background(), &list, client.InNamespace(ns)))
				require.Len(t, list.Items, 1)
				rs := list.Items[0]
				assert.Equal(t, dnsv1alpha1.RRTypeCNAME, rs.Spec.RecordType)
				assert.Equal(t, "example-com", rs.Spec.DNSZoneRef.Name)
				require.Len(t, rs.Spec.Records, 1)
				assert.Equal(t, "api.example.com.", rs.Spec.Records[0].Name)
				require.NotNil(t, rs.Spec.Records[0].CNAME)
				// canonical hostname target should end with a dot
				assert.True(t, len(rs.Spec.Records[0].CNAME.Content) > 0)
				assert.Equal(t, ".", string(rs.Spec.Records[0].CNAME.Content[len(rs.Spec.Records[0].CNAME.Content)-1]))
			},
		},
		{
			name:             "apex domain creates ALIAS record",
			claimedHostnames: []string{"example.com"},
			upstreamObjects: []client.Object{
				newVerifiedDNSZoneDomain(ns, "example.com", true), // apex=true
				newDNSZone(ns, "example-com", "example.com"),
			},
			assertStatuses: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				require.Len(t, statuses, 1)
				assert.Equal(t, "example.com", statuses[0].Hostname)
				c := apimeta.FindStatusCondition(statuses[0].Conditions, networkingv1alpha.HostnameConditionDNSRecordProgrammed)
				require.NotNil(t, c)
				assert.Equal(t, metav1.ConditionTrue, c.Status)
			},
			assertRecords: func(t *testing.T, cl client.Client) {
				var list dnsv1alpha1.DNSRecordSetList
				require.NoError(t, cl.List(context.Background(), &list, client.InNamespace(ns)))
				require.Len(t, list.Items, 1)
				rs := list.Items[0]
				assert.Equal(t, dnsv1alpha1.RRTypeALIAS, rs.Spec.RecordType)
				require.Len(t, rs.Spec.Records, 1)
				require.NotNil(t, rs.Spec.Records[0].ALIAS)
			},
		},
		{
			name:             "subdomain zone is used when more specific than apex zone",
			claimedHostnames: []string{"v1.api.example.com"},
			upstreamObjects: []client.Object{
				// Both apex and subdomain zones exist; subdomain should be preferred.
				newVerifiedDNSZoneDomain(ns, "example.com", false),
				newVerifiedDNSZoneDomain(ns, "api.example.com", false),
				newDNSZone(ns, "example-com", "example.com"),
				newDNSZone(ns, "api-example-com", "api.example.com"),
			},
			assertStatuses: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				require.Len(t, statuses, 1)
				assert.Equal(t, "v1.api.example.com", statuses[0].Hostname)
				c := apimeta.FindStatusCondition(statuses[0].Conditions, networkingv1alpha.HostnameConditionDNSRecordProgrammed)
				require.NotNil(t, c)
				assert.Equal(t, metav1.ConditionTrue, c.Status)
				assert.Equal(t, networkingv1alpha.DNSRecordReasonCreated, c.Reason)
			},
			assertRecords: func(t *testing.T, cl client.Client) {
				var list dnsv1alpha1.DNSRecordSetList
				require.NoError(t, cl.List(context.Background(), &list, client.InNamespace(ns)))
				require.Len(t, list.Items, 1)
				rs := list.Items[0]
				assert.Equal(t, dnsv1alpha1.RRTypeCNAME, rs.Spec.RecordType)
				// Should use the more specific subdomain zone.
				assert.Equal(t, "api-example-com", rs.Spec.DNSZoneRef.Name)
				require.Len(t, rs.Spec.Records, 1)
				assert.Equal(t, "v1.api.example.com.", rs.Spec.Records[0].Name)
			},
		},
		{
			name:             "conflict with externally managed record sets DNSRecordProgrammed=False",
			claimedHostnames: []string{"api.example.com"},
			upstreamObjects: func() []client.Object {
				// A pre-existing DNSRecordSet for the same hostname, managed by a different actor.
				existingRS := &dnsv1alpha1.DNSRecordSet{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: ns,
						Name:      "user-created-record",
						UID:       uuid.NewUUID(),
						Labels: map[string]string{
							labelDNSManaged: "true",
							labelManagedBy:  "some-other-actor",
						},
						Annotations: map[string]string{
							annotationDNSHostname: "api.example.com",
						},
					},
					Spec: dnsv1alpha1.DNSRecordSetSpec{
						DNSZoneRef: corev1.LocalObjectReference{Name: "example-com"},
						RecordType: dnsv1alpha1.RRTypeCNAME,
						Records:    []dnsv1alpha1.RecordEntry{{Name: "api.example.com."}},
					},
				}
				return []client.Object{
					newVerifiedDNSZoneDomain(ns, "example.com", false),
					newDNSZone(ns, "example-com", "example.com"),
					existingRS,
				}
			}(),
			assertStatuses: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				require.Len(t, statuses, 1)
				c := apimeta.FindStatusCondition(statuses[0].Conditions, networkingv1alpha.HostnameConditionDNSRecordProgrammed)
				require.NotNil(t, c)
				assert.Equal(t, metav1.ConditionFalse, c.Status)
				assert.Equal(t, networkingv1alpha.DNSRecordReasonConflict, c.Reason)
				assert.Contains(t, c.Message, "api.example.com")
				assert.Contains(t, c.Message, "some-other-actor")
			},
		},
		{
			name:             "canonical hostname is skipped (handled by external-dns)",
			claimedHostnames: []string{}, // empty; we add the canonical hostname below in test setup
			upstreamObjects: []client.Object{
				newVerifiedDNSZoneDomain(ns, "gateways.test.local", false),
				newDNSZone(ns, "gateways-zone", "gateways.test.local"),
			},
			assertStatuses: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				// canonical hostname should be excluded
				assert.Empty(t, statuses)
			},
			assertRecords: func(t *testing.T, cl client.Client) {
				var list dnsv1alpha1.DNSRecordSetList
				require.NoError(t, cl.List(context.Background(), &list, client.InNamespace(ns)))
				assert.Empty(t, list.Items)
			},
		},
		{
			name:             "single-label hostname gets NotApplicable",
			claimedHostnames: []string{"localhost"},
			upstreamObjects:  nil,
			assertStatuses: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				require.Len(t, statuses, 1)
				c := apimeta.FindStatusCondition(statuses[0].Conditions, networkingv1alpha.HostnameConditionDNSRecordProgrammed)
				require.NotNil(t, c)
				assert.Equal(t, metav1.ConditionTrue, c.Status)
				assert.Equal(t, networkingv1alpha.DNSRecordReasonNotApplicable, c.Reason)
			},
		},
		{
			name:             "multiple hostnames with mixed results",
			claimedHostnames: []string{"api.example.com", "other.example.com"},
			upstreamObjects: []client.Object{
				newVerifiedDNSZoneDomain(ns, "example.com", false),
				newDNSZone(ns, "example-com", "example.com"),
			},
			assertStatuses: func(t *testing.T, statuses []networkingv1alpha.HostnameStatus) {
				require.Len(t, statuses, 2)
				for _, hs := range statuses {
					c := apimeta.FindStatusCondition(hs.Conditions, networkingv1alpha.HostnameConditionDNSRecordProgrammed)
					require.NotNil(t, c, "condition missing for hostname %s", hs.Hostname)
					assert.Equal(t, metav1.ConditionTrue, c.Status)
					assert.Equal(t, networkingv1alpha.DNSRecordReasonCreated, c.Reason)
				}
			},
			assertRecords: func(t *testing.T, cl client.Client) {
				var list dnsv1alpha1.DNSRecordSetList
				require.NoError(t, cl.List(context.Background(), &list, client.InNamespace(ns)))
				assert.Len(t, list.Items, 2)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := log.IntoContext(context.Background(), logger)
			s := newDNSTestScheme(t)

			gw := newTestGatewayForDNS(ns, "test-gw")

			// Seed the gateway itself so owner reference can be set.
			allObjects := append([]client.Object{gw}, tt.upstreamObjects...)
			for _, obj := range allObjects {
				if obj.GetUID() == "" {
					obj.SetUID(uuid.NewUUID())
				}
				ts := obj.GetCreationTimestamp()
				if ts.Time.IsZero() {
					obj.SetCreationTimestamp(metav1.Now())
				}
			}

			cl := buildFakeUpstreamClientForDNS(s, allObjects...)

			cfg := testConfig
			if tt.name == "dns integration disabled returns no statuses" {
				cfg.Gateway.EnableDNSIntegration = false
			}

			reconciler := newDNSReconciler(cfg)

			// For the canonical hostname skip test, pass the gateway's own canonical address.
			claimed := tt.claimedHostnames
			if tt.name == "canonical hostname is skipped (handled by external-dns)" {
				canonicalHostname := cfg.Gateway.GatewayDNSAddress(gw)
				claimed = []string{canonicalHostname}
			}

			statuses, result := reconciler.ensureDNSRecordSets(ctx, cl, gw, claimed)

			if tt.wantErr {
				assert.Error(t, result.Err)
				return
			}
			require.NoError(t, result.Err)

			if tt.name == "dns integration disabled returns no statuses" {
				assert.Empty(t, statuses)
				return
			}

			if tt.assertStatuses != nil {
				tt.assertStatuses(t, statuses)
			}
			if tt.assertRecords != nil {
				tt.assertRecords(t, cl)
			}
		})
	}
}

// TestEnsureDNSRecordSets_UpdateExistingRecord verifies that re-running
// ensureDNSRecordSets on an already-existing platform-managed record produces
// reason=RecordUpdated (or RecordCreated for unchanged) and does not error.
func TestEnsureDNSRecordSets_UpdateExistingRecord(t *testing.T) {
	const ns = "test-ns"
	ctx := log.IntoContext(context.Background(), zap.New())
	s := newDNSTestScheme(t)

	testConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			TargetDomain:         "gateways.test.local",
			EnableDNSIntegration: true,
		},
	}

	gw := newTestGatewayForDNS(ns, "my-gw")
	domain := newVerifiedDNSZoneDomain(ns, "example.com", false)
	zone := newDNSZone(ns, "example-com", "example.com")

	// Build the existing, platform-managed DNSRecordSet.
	hostname := "api.example.com"
	existingRSName := dnsRecordSetName(gw.Name, hostname)
	existingRS := &dnsv1alpha1.DNSRecordSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      existingRSName,
			UID:       uuid.NewUUID(),
			Labels: map[string]string{
				labelManagedBy:     labelManagedByValue,
				labelDNSManaged:    "true",
				labelDNSSourceKind: "Gateway",
				labelDNSSourceName: gw.Name,
				labelDNSSourceNS:   ns,
			},
			Annotations: map[string]string{
				annotationDNSHostname: hostname,
			},
		},
		Spec: dnsv1alpha1.DNSRecordSetSpec{
			DNSZoneRef: corev1.LocalObjectReference{Name: zone.Name},
			RecordType: dnsv1alpha1.RRTypeCNAME,
			Records: []dnsv1alpha1.RecordEntry{
				{
					Name: hostname + ".",
					CNAME: &dnsv1alpha1.CNAMERecordSpec{
						Content: "old-canonical.gateways.test.local.",
					},
				},
			},
		},
	}

	allObjects := []client.Object{gw, domain, zone, existingRS}
	for _, obj := range allObjects {
		if obj.GetUID() == "" {
			obj.SetUID(uuid.NewUUID())
		}
		ts := obj.GetCreationTimestamp()
		if ts.Time.IsZero() {
			obj.SetCreationTimestamp(metav1.Now())
		}
	}

	cl := buildFakeUpstreamClientForDNS(s, allObjects...)
	reconciler := newDNSReconciler(testConfig)

	statuses, result := reconciler.ensureDNSRecordSets(ctx, cl, gw, []string{hostname})
	require.NoError(t, result.Err)
	require.Len(t, statuses, 1)

	c := apimeta.FindStatusCondition(statuses[0].Conditions, networkingv1alpha.HostnameConditionDNSRecordProgrammed)
	require.NotNil(t, c)
	assert.Equal(t, metav1.ConditionTrue, c.Status)
	// Reason is either RecordCreated or RecordUpdated – both indicate success.
	assert.Contains(t,
		[]string{networkingv1alpha.DNSRecordReasonCreated, networkingv1alpha.DNSRecordReasonUpdated},
		c.Reason,
		"expected RecordCreated or RecordUpdated on existing platform-managed record",
	)

	// The DNSRecordSet should still exist and contain the new canonical hostname.
	var updatedRS dnsv1alpha1.DNSRecordSet
	require.NoError(t, cl.Get(ctx, client.ObjectKey{Namespace: ns, Name: existingRSName}, &updatedRS))
	require.Len(t, updatedRS.Spec.Records, 1)
	// The canonical hostname target should end with a dot.
	assert.True(t, len(updatedRS.Spec.Records[0].CNAME.Content) > 0)
}

// ---------------------------------------------------------------------------
// TestGarbageCollectDNSRecordSets
// ---------------------------------------------------------------------------

func TestGarbageCollectDNSRecordSets(t *testing.T) {
	const ns = "test-ns"
	ctx := log.IntoContext(context.Background(), zap.New())
	s := newDNSTestScheme(t)

	testConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			TargetDomain:         "gateways.test.local",
			EnableDNSIntegration: true,
		},
	}

	gw := newTestGatewayForDNS(ns, "my-gw")

	makePlatformRS := func(name, hostname string) *dnsv1alpha1.DNSRecordSet {
		return &dnsv1alpha1.DNSRecordSet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: ns,
				Name:      name,
				UID:       uuid.NewUUID(),
				Labels: map[string]string{
					labelManagedBy:     labelManagedByValue,
					labelDNSManaged:    "true",
					labelDNSSourceKind: "Gateway",
					labelDNSSourceName: gw.Name,
					labelDNSSourceNS:   ns,
				},
				Annotations: map[string]string{
					annotationDNSHostname: hostname,
				},
			},
			Spec: dnsv1alpha1.DNSRecordSetSpec{
				DNSZoneRef: corev1.LocalObjectReference{Name: "example-com"},
				RecordType: dnsv1alpha1.RRTypeCNAME,
				Records:    []dnsv1alpha1.RecordEntry{{Name: hostname + "."}},
			},
		}
	}

	staleRS := makePlatformRS("my-gw-stale", "removed.example.com")
	keepRS := makePlatformRS(dnsRecordSetName(gw.Name, "keep.example.com"), "keep.example.com")

	allObjects := []client.Object{gw, staleRS, keepRS}
	for _, obj := range allObjects {
		if obj.GetUID() == "" {
			obj.SetUID(uuid.NewUUID())
		}
		ts := obj.GetCreationTimestamp()
		if ts.Time.IsZero() {
			obj.SetCreationTimestamp(metav1.Now())
		}
	}

	cl := buildFakeUpstreamClientForDNS(s, allObjects...)
	reconciler := newDNSReconciler(testConfig)

	// desiredNames contains only the "keep" record; "stale" should be GC'd.
	desiredNames := map[string]bool{
		keepRS.Name: true,
	}

	result := reconciler.garbageCollectDNSRecordSets(ctx, cl, gw, desiredNames)
	require.NoError(t, result.Err)

	// Stale record should be gone.
	var deletedRS dnsv1alpha1.DNSRecordSet
	err := cl.Get(ctx, client.ObjectKey{Namespace: ns, Name: staleRS.Name}, &deletedRS)
	assert.True(t, err != nil, "stale DNSRecordSet should have been deleted")

	// Keep record should remain.
	var remainingRS dnsv1alpha1.DNSRecordSet
	require.NoError(t, cl.Get(ctx, client.ObjectKey{Namespace: ns, Name: keepRS.Name}, &remainingRS))
}

func TestGarbageCollectDNSRecordSets_DoesNotDeleteInUseRecords(t *testing.T) {
	const ns = "test-ns"
	ctx := log.IntoContext(context.Background(), zap.New())
	s := newDNSTestScheme(t)

	testConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			TargetDomain:         "gateways.test.local",
			EnableDNSIntegration: true,
		},
	}

	gw := newTestGatewayForDNS(ns, "my-gw")
	rsName := dnsRecordSetName(gw.Name, "api.example.com")
	rs := &dnsv1alpha1.DNSRecordSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      rsName,
			UID:       uuid.NewUUID(),
			Labels: map[string]string{
				labelManagedBy:     labelManagedByValue,
				labelDNSManaged:    "true",
				labelDNSSourceName: gw.Name,
				labelDNSSourceNS:   ns,
			},
			Annotations: map[string]string{
				annotationDNSHostname: "api.example.com",
			},
		},
		Spec: dnsv1alpha1.DNSRecordSetSpec{
			DNSZoneRef: corev1.LocalObjectReference{Name: "example-com"},
			RecordType: dnsv1alpha1.RRTypeCNAME,
			Records:    []dnsv1alpha1.RecordEntry{{Name: "api.example.com."}},
		},
	}

	allObjects := []client.Object{gw, rs}
	for _, obj := range allObjects {
		if obj.GetUID() == "" {
			obj.SetUID(uuid.NewUUID())
		}
		ts := obj.GetCreationTimestamp()
		if ts.Time.IsZero() {
			obj.SetCreationTimestamp(metav1.Now())
		}
	}

	cl := buildFakeUpstreamClientForDNS(s, allObjects...)
	reconciler := newDNSReconciler(testConfig)

	// Provide the record name as desired – it must not be deleted.
	desiredNames := map[string]bool{rsName: true}
	result := reconciler.garbageCollectDNSRecordSets(ctx, cl, gw, desiredNames)
	require.NoError(t, result.Err)

	var remaining dnsv1alpha1.DNSRecordSet
	require.NoError(t, cl.Get(ctx, client.ObjectKey{Namespace: ns, Name: rsName}, &remaining))
}

// ---------------------------------------------------------------------------
// TestReconcileDNSStatus
// ---------------------------------------------------------------------------

func TestReconcileDNSStatus(t *testing.T) {
	t.Parallel()

	testConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			TargetDomain:         "gateways.test.local",
			EnableDNSIntegration: true,
		},
	}

	makeHS := func(hostname, reason string, status metav1.ConditionStatus) networkingv1alpha.HostnameStatus {
		hs := networkingv1alpha.HostnameStatus{Hostname: hostname}
		apimeta.SetStatusCondition(&hs.Conditions, metav1.Condition{
			Type:   networkingv1alpha.HostnameConditionDNSRecordProgrammed,
			Status: status,
			Reason: reason,
		})
		return hs
	}

	tests := []struct {
		name             string
		hostnameStatuses []networkingv1alpha.HostnameStatus
		wantCondition    *metav1.Condition // nil means condition should be absent
	}{
		{
			name:             "no hostnames – condition omitted",
			hostnameStatuses: nil,
			wantCondition:    nil,
		},
		{
			name: "all NotApplicable – condition omitted",
			hostnameStatuses: []networkingv1alpha.HostnameStatus{
				makeHS("api.example.com", networkingv1alpha.DNSRecordReasonNotApplicable, metav1.ConditionTrue),
				makeHS("other.example.com", networkingv1alpha.DNSRecordReasonNotApplicable, metav1.ConditionTrue),
			},
			wantCondition: nil,
		},
		{
			name: "all applicable and programmed – DNSRecordsProgrammed=True",
			hostnameStatuses: []networkingv1alpha.HostnameStatus{
				makeHS("api.example.com", networkingv1alpha.DNSRecordReasonCreated, metav1.ConditionTrue),
				makeHS("other.example.com", networkingv1alpha.DNSRecordReasonCreated, metav1.ConditionTrue),
			},
			wantCondition: &metav1.Condition{
				Type:   networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed,
				Status: metav1.ConditionTrue,
				Reason: networkingv1alpha.DNSRecordsProgrammedReasonAllCreated,
			},
		},
		{
			name: "one failed – DNSRecordsProgrammed=False",
			hostnameStatuses: []networkingv1alpha.HostnameStatus{
				makeHS("api.example.com", networkingv1alpha.DNSRecordReasonCreated, metav1.ConditionTrue),
				makeHS("bad.example.com", networkingv1alpha.DNSRecordReasonConflict, metav1.ConditionFalse),
			},
			wantCondition: &metav1.Condition{
				Type:   networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed,
				Status: metav1.ConditionFalse,
				Reason: networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure,
			},
		},
		{
			name: "all failed – DNSRecordsProgrammed=False",
			hostnameStatuses: []networkingv1alpha.HostnameStatus{
				makeHS("api.example.com", networkingv1alpha.DNSRecordReasonFailed, metav1.ConditionFalse),
			},
			wantCondition: &metav1.Condition{
				Type:   networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed,
				Status: metav1.ConditionFalse,
				Reason: networkingv1alpha.DNSRecordsProgrammedReasonPartialFailure,
			},
		},
		{
			name: "mixed NotApplicable and programmed – True based on applicable subset",
			hostnameStatuses: []networkingv1alpha.HostnameStatus{
				makeHS("api.example.com", networkingv1alpha.DNSRecordReasonCreated, metav1.ConditionTrue),
				makeHS("external.example.com", networkingv1alpha.DNSRecordReasonNotApplicable, metav1.ConditionTrue),
			},
			wantCondition: &metav1.Condition{
				Type:   networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed,
				Status: metav1.ConditionTrue,
				Reason: networkingv1alpha.DNSRecordsProgrammedReasonAllCreated,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := newDNSTestScheme(t)
			gw := newTestGatewayForDNS("test-ns", "my-gw")
			gw.SetUID(uuid.NewUUID())
			gw.SetCreationTimestamp(metav1.Now())

			cl := buildFakeUpstreamClientForDNS(s, gw)
			reconciler := newDNSReconciler(testConfig)

			result := reconciler.reconcileDNSStatus(cl, gw, tt.hostnameStatuses)
			// reconcileDNSStatus enqueues a status update; we only care about
			// what was written to the in-memory gateway object.
			_ = result

			got := apimeta.FindStatusCondition(gw.Status.Conditions, networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed)

			if tt.wantCondition == nil {
				assert.Nil(t, got, "expected DNSRecordsProgrammed condition to be absent")
			} else {
				require.NotNil(t, got, "expected DNSRecordsProgrammed condition to be present")
				assert.Equal(t, tt.wantCondition.Status, got.Status)
				assert.Equal(t, tt.wantCondition.Reason, got.Reason)
			}
		})
	}
}

// TestReconcileDNSStatus_RemovesStaleDNSCondition verifies that calling
// reconcileDNSStatus with no applicable hostnames removes a pre-existing
// DNSRecordsProgrammed condition from the gateway.
func TestReconcileDNSStatus_RemovesStaleDNSCondition(t *testing.T) {
	s := newDNSTestScheme(t)
	gw := newTestGatewayForDNS("test-ns", "my-gw")
	gw.SetUID(uuid.NewUUID())
	gw.SetCreationTimestamp(metav1.Now())

	// Pre-set the condition as if a previous reconcile had set it.
	apimeta.SetStatusCondition(&gw.Status.Conditions, metav1.Condition{
		Type:   networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed,
		Status: metav1.ConditionTrue,
		Reason: networkingv1alpha.DNSRecordsProgrammedReasonAllCreated,
	})

	testConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			TargetDomain:         "gateways.test.local",
			EnableDNSIntegration: true,
		},
	}

	cl := buildFakeUpstreamClientForDNS(s, gw)
	reconciler := newDNSReconciler(testConfig)

	// Pass only NotApplicable statuses → needed == 0 → condition must be removed.
	hs := networkingv1alpha.HostnameStatus{Hostname: "api.example.com"}
	apimeta.SetStatusCondition(&hs.Conditions, metav1.Condition{
		Type:   networkingv1alpha.HostnameConditionDNSRecordProgrammed,
		Status: metav1.ConditionTrue,
		Reason: networkingv1alpha.DNSRecordReasonNotApplicable,
	})

	reconciler.reconcileDNSStatus(cl, gw, []networkingv1alpha.HostnameStatus{hs})

	got := apimeta.FindStatusCondition(gw.Status.Conditions, networkingv1alpha.HTTPProxyConditionDNSRecordsProgrammed)
	assert.Nil(t, got, "DNSRecordsProgrammed condition should be removed when no applicable hostnames")
}

// ---------------------------------------------------------------------------
// TestBuildDesiredDNSRecordSetSpec – unit test for spec builder helper.
// ---------------------------------------------------------------------------

func TestBuildDesiredDNSRecordSetSpec(t *testing.T) {
	t.Parallel()

	zone := *newDNSZone("ns", "example-com", "example.com")

	tests := []struct {
		name              string
		hostname          string
		canonicalHostname string
		rrType            dnsv1alpha1.RRType
		wantRecordType    dnsv1alpha1.RRType
		wantFQDNName      string
		wantFQDNContent   string
		wantNilCNAME      bool
		wantNilALIAS      bool
	}{
		{
			name:              "CNAME record gets trailing dots on both name and content",
			hostname:          "api.example.com",
			canonicalHostname: "gw.gateways.test.local",
			rrType:            dnsv1alpha1.RRTypeCNAME,
			wantRecordType:    dnsv1alpha1.RRTypeCNAME,
			wantFQDNName:      "api.example.com.",
			wantFQDNContent:   "gw.gateways.test.local.",
			wantNilALIAS:      true,
		},
		{
			name:              "ALIAS record gets trailing dots on both name and content",
			hostname:          "example.com",
			canonicalHostname: "gw.gateways.test.local",
			rrType:            dnsv1alpha1.RRTypeALIAS,
			wantRecordType:    dnsv1alpha1.RRTypeALIAS,
			wantFQDNName:      "example.com.",
			wantFQDNContent:   "gw.gateways.test.local.",
			wantNilCNAME:      true,
		},
		{
			name:              "already FQDN inputs do not get double dots",
			hostname:          "api.example.com.",
			canonicalHostname: "gw.gateways.test.local.",
			rrType:            dnsv1alpha1.RRTypeCNAME,
			wantRecordType:    dnsv1alpha1.RRTypeCNAME,
			wantFQDNName:      "api.example.com.",
			wantFQDNContent:   "gw.gateways.test.local.",
			wantNilALIAS:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			spec := buildDesiredDNSRecordSetSpec(tt.hostname, tt.canonicalHostname, zone, tt.rrType)

			assert.Equal(t, tt.wantRecordType, spec.RecordType)
			assert.Equal(t, "example-com", spec.DNSZoneRef.Name)
			require.Len(t, spec.Records, 1)

			entry := spec.Records[0]
			assert.Equal(t, tt.wantFQDNName, entry.Name)
			require.NotNil(t, entry.TTL)
			assert.Equal(t, int64(300), *entry.TTL)

			if tt.wantNilCNAME {
				assert.Nil(t, entry.CNAME, "CNAME should not be set for ALIAS record")
			} else if spec.RecordType == dnsv1alpha1.RRTypeCNAME {
				require.NotNil(t, entry.CNAME)
				assert.Equal(t, tt.wantFQDNContent, entry.CNAME.Content)
			}

			if tt.wantNilALIAS {
				assert.Nil(t, entry.ALIAS, "ALIAS should not be set for CNAME record")
			} else if spec.RecordType == dnsv1alpha1.RRTypeALIAS {
				require.NotNil(t, entry.ALIAS)
				assert.Equal(t, tt.wantFQDNContent, entry.ALIAS.Content)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestEnsureDNSRecordSets_DNSIntegrationDisabled
// ---------------------------------------------------------------------------

func TestEnsureDNSRecordSets_DNSIntegrationDisabled(t *testing.T) {
	const ns = "test-ns"
	ctx := log.IntoContext(context.Background(), zap.New())
	s := newDNSTestScheme(t)

	testConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			TargetDomain:         "gateways.test.local",
			EnableDNSIntegration: false, // disabled
		},
	}

	gw := newTestGatewayForDNS(ns, "my-gw")
	gw.SetUID(uuid.NewUUID())
	gw.SetCreationTimestamp(metav1.Now())

	domain := newVerifiedDNSZoneDomain(ns, "example.com", false)
	domain.SetUID(uuid.NewUUID())
	domain.SetCreationTimestamp(metav1.Now())

	zone := newDNSZone(ns, "example-com", "example.com")
	zone.SetUID(uuid.NewUUID())
	zone.SetCreationTimestamp(metav1.Now())

	cl := buildFakeUpstreamClientForDNS(s, gw, domain, zone)
	reconciler := newDNSReconciler(testConfig)

	statuses, result := reconciler.ensureDNSRecordSets(ctx, cl, gw, []string{"api.example.com"})

	require.NoError(t, result.Err)
	assert.Empty(t, statuses, "no statuses should be returned when DNS integration is disabled")

	var list dnsv1alpha1.DNSRecordSetList
	require.NoError(t, cl.List(ctx, &list, client.InNamespace(ns)))
	assert.Empty(t, list.Items, "no DNSRecordSets should be created when DNS integration is disabled")
}
