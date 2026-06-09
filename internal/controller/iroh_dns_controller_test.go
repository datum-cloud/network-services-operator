// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
)

func newIrohTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, k8sscheme.AddToScheme(s))
	require.NoError(t, coordinationv1.AddToScheme(s))
	require.NoError(t, networkingv1alpha1.AddToScheme(s))
	require.NoError(t, dnsv1alpha1.AddToScheme(s))
	return s
}

// Real iroh public key from iroh-base/src/key.rs SecretKey.public, chosen
// because it has a known z32 form we can pin against.
const (
	testEndpointHex = "f120d52e42bfcee750508baf28900acac85ad3f397ab4bb653b32be505c32d39"
	testEndpointZ32 = "6ropkm1nz98qqwnotqz1tryk3mrfiw9u16iwzp1usci6kbqdfwho"

	// multicluster-runtime cluster names start with "/" — set the test
	// vector that way so the label encoding is exercised.
	testClusterName        = "/test-project-staging"
	testClusterNameEncoded = "cluster-_test-project-staging"
	testConnectorUID       = "00000000-0000-0000-0000-000000000abc"

	testRelayURL = "https://relay.example.com"
	testIPv4     = "192.0.2.1"
	testIPv6     = "2001:db8::1"
)

func newReconciler() *IrohDNSReconciler {
	return &IrohDNSReconciler{
		Config: config.NetworkServicesOperator{
			Connector: config.ConnectorConfig{
				Iroh: config.IrohConnectorConfig{
					DNSEnabled:   true,
					RecordPrefix: "_iroh",
					RecordSuffix: "connectors",
					TTLSeconds:   30,
					DNSZoneRef: config.IrohDNSZoneRef{
						Namespace: "datum-dns",
						Name:      "datumconnect-net",
					},
				},
			},
		},
	}
}

func newConnector(pk *networkingv1alpha1.ConnectorConnectionDetailsPublicKey) *networkingv1alpha1.Connector {
	c := &networkingv1alpha1.Connector{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "edge-1",
			Namespace: "default",
			UID:       types.UID(testConnectorUID),
		},
		Spec: networkingv1alpha1.ConnectorSpec{
			ConnectorClassName: "datum-connect",
		},
	}
	if pk != nil {
		c.Status.ConnectionDetails = &networkingv1alpha1.ConnectorConnectionDetails{
			Type:      networkingv1alpha1.PublicKeyConnectorConnectionType,
			PublicKey: pk,
		}
	}
	return c
}

func TestBuildDesiredRecordSet_StatusGating(t *testing.T) {
	tests := []struct {
		name string
		pk   *networkingv1alpha1.ConnectorConnectionDetailsPublicKey
		want bool
	}{
		{name: "no connection details", pk: nil, want: false},
		{name: "no public key data — empty struct", pk: &networkingv1alpha1.ConnectorConnectionDetailsPublicKey{}, want: false},
		{
			name: "id without relay or addresses",
			pk:   &networkingv1alpha1.ConnectorConnectionDetailsPublicKey{Id: testEndpointHex},
			want: false,
		},
		{
			name: "id with relay only — publishes",
			pk: &networkingv1alpha1.ConnectorConnectionDetailsPublicKey{
				Id:        testEndpointHex,
				HomeRelay: testRelayURL,
			},
			want: true,
		},
		{
			name: "id with addresses only — publishes",
			pk: &networkingv1alpha1.ConnectorConnectionDetailsPublicKey{
				Id:        testEndpointHex,
				Addresses: []networkingv1alpha1.PublicKeyConnectorAddress{{Address: testIPv4, Port: 8080}},
			},
			want: true,
		},
		{
			name: "id with both — publishes",
			pk: &networkingv1alpha1.ConnectorConnectionDetailsPublicKey{
				Id:        testEndpointHex,
				HomeRelay: testRelayURL,
				Addresses: []networkingv1alpha1.PublicKeyConnectorAddress{{Address: testIPv4, Port: 8080}},
			},
			want: true,
		},
	}

	r := newReconciler()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok, err := r.buildDesiredRecordSet(testClusterName, newConnector(tt.pk))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ok != tt.want {
				t.Fatalf("ok = %v, want %v", ok, tt.want)
			}
		})
	}
}

func TestBuildDesiredRecordSet_RecordContents(t *testing.T) {
	r := newReconciler()
	conn := newConnector(&networkingv1alpha1.ConnectorConnectionDetailsPublicKey{
		Id:        testEndpointHex,
		HomeRelay: testRelayURL,
		Addresses: []networkingv1alpha1.PublicKeyConnectorAddress{
			{Address: testIPv4, Port: 8080},
			{Address: testIPv6, Port: 9090},
		},
	})

	drs, ok, err := r.buildDesiredRecordSet(testClusterName, conn)
	if err != nil || !ok {
		t.Fatalf("buildDesiredRecordSet failed: ok=%v err=%v", ok, err)
	}

	// DNSRecordSet name is keyed by z32 endpoint id (one record per
	// endpoint, not per Connector UID) — see claim-based ownership design.
	wantName := "iroh-" + testEndpointZ32
	if drs.Name != wantName {
		t.Errorf("Name = %q, want %q", drs.Name, wantName)
	}
	if drs.Namespace != "datum-dns" {
		t.Errorf("Namespace = %q, want %q", drs.Namespace, "datum-dns")
	}
	if drs.Spec.RecordType != dnsv1alpha1.RRTypeTXT {
		t.Errorf("RecordType = %q, want %q", drs.Spec.RecordType, dnsv1alpha1.RRTypeTXT)
	}
	if drs.Spec.DNSZoneRef.Name != "datumconnect-net" {
		t.Errorf("DNSZoneRef.Name = %q, want %q", drs.Spec.DNSZoneRef.Name, "datumconnect-net")
	}

	wantRecordName := "_iroh." + testEndpointZ32 + ".connectors"
	// One TXT entry per attribute: relay + one addr per direct address.
	// iroh's parser SocketAddr::from_str(value) drops anything that isn't
	// exactly one socket address, so we cannot pack multiple addrs into
	// one space-separated value.
	if len(drs.Spec.Records) != 3 {
		t.Fatalf("Records count = %d, want 3 (relay + 2× addr)", len(drs.Spec.Records))
	}
	gotContents := []string{
		drs.Spec.Records[0].TXT.Content,
		drs.Spec.Records[1].TXT.Content,
		drs.Spec.Records[2].TXT.Content,
	}
	wantContents := []string{
		"relay=https://relay.example.com",
		"addr=192.0.2.1:8080",
		"addr=[2001:db8::1]:9090",
	}
	for i := range gotContents {
		if gotContents[i] != wantContents[i] {
			t.Errorf("Records[%d].TXT.Content = %q, want %q", i, gotContents[i], wantContents[i])
		}
		if drs.Spec.Records[i].Name != wantRecordName {
			t.Errorf("Records[%d].Name = %q, want %q", i, drs.Spec.Records[i].Name, wantRecordName)
		}
		if drs.Spec.Records[i].TTL == nil || *drs.Spec.Records[i].TTL != 30 {
			t.Errorf("Records[%d].TTL = %v, want 30", i, drs.Spec.Records[i].TTL)
		}
	}

	// Labels track the claim and the owner Connector identity. The watch
	// on downstream DNSRecordSet uses these to enqueue the owner on changes.
	for k, v := range map[string]string{
		"app.kubernetes.io/managed-by": irohDNSManagedByLabelValue,
		irohDNSClaimedByUIDLabel:       testConnectorUID,
		irohDNSConnectorClusterLabel:   testClusterNameEncoded,
		irohDNSConnectorNamespaceLabel: conn.Namespace,
		irohDNSConnectorNameLabel:      conn.Name,
	} {
		if drs.Labels[k] != v {
			t.Errorf("label %q = %q, want %q", k, drs.Labels[k], v)
		}
	}
}

func TestEncodeDecodeIrohClusterLabel(t *testing.T) {
	tests := []string{
		"",
		"/test-project-staging",
		"/zachs-project-z5pegw",
		"plain-no-slashes",
		"/with/multiple/slashes",
	}
	for _, want := range tests {
		t.Run(want, func(t *testing.T) {
			got := decodeIrohClusterLabel(encodeIrohClusterLabel(want))
			if got != want {
				t.Errorf("round-trip mismatch: encode(%q) -> decode = %q", want, got)
			}
		})
	}
}

func TestBuildDesiredRecordSet_RelayOnlyOmitsAddrEntry(t *testing.T) {
	r := newReconciler()
	conn := newConnector(&networkingv1alpha1.ConnectorConnectionDetailsPublicKey{
		Id:        testEndpointHex,
		HomeRelay: testRelayURL,
	})

	drs, _, err := r.buildDesiredRecordSet(testClusterName, conn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(drs.Spec.Records) != 1 {
		t.Fatalf("Records count = %d, want 1 (relay only)", len(drs.Spec.Records))
	}
	if drs.Spec.Records[0].TXT.Content != "relay=https://relay.example.com" {
		t.Errorf("Content = %q", drs.Spec.Records[0].TXT.Content)
	}
}

func TestBuildDesiredRecordSet_EmptySuffixPutsRecordsUnderZoneRoot(t *testing.T) {
	r := newReconciler()
	r.Config.Connector.Iroh.RecordSuffix = ""
	conn := newConnector(&networkingv1alpha1.ConnectorConnectionDetailsPublicKey{
		Id:        testEndpointHex,
		HomeRelay: testRelayURL,
	})

	drs, _, err := r.buildDesiredRecordSet(testClusterName, conn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "_iroh." + testEndpointZ32
	if drs.Spec.Records[0].Name != want {
		t.Errorf("record name = %q, want %q (no trailing suffix)", drs.Spec.Records[0].Name, want)
	}
}

func TestBuildDesiredRecordSet_InvalidEndpointId(t *testing.T) {
	r := newReconciler()
	conn := newConnector(&networkingv1alpha1.ConnectorConnectionDetailsPublicKey{
		Id:        "not-hex",
		HomeRelay: testRelayURL,
	})
	if _, _, err := r.buildDesiredRecordSet(testClusterName, conn); err == nil {
		t.Fatal("expected error for non-hex endpoint id, got nil")
	}
}

// TestBuildDesiredRecordSet_TwoConnectorsSameKeyProduceSameName verifies the
// load-bearing claim-based property: two distinct Connectors that share an
// iroh keypair compute the same DNSRecordSet name. This is what lets the
// claim-based reconciler dedupe them.
func TestBuildDesiredRecordSet_TwoConnectorsSameKeyProduceSameName(t *testing.T) {
	r := newReconciler()
	pk := &networkingv1alpha1.ConnectorConnectionDetailsPublicKey{
		Id:        testEndpointHex,
		HomeRelay: testRelayURL,
	}

	a := newConnector(pk)
	a.UID = types.UID("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	a.Name = "edge-a"

	b := newConnector(pk)
	b.UID = types.UID("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	b.Name = "edge-b"

	drsA, _, err := r.buildDesiredRecordSet("cluster-a", a)
	if err != nil {
		t.Fatalf("buildDesiredRecordSet(a): %v", err)
	}
	drsB, _, err := r.buildDesiredRecordSet("cluster-b", b)
	if err != nil {
		t.Fatalf("buildDesiredRecordSet(b): %v", err)
	}

	if drsA.Name != drsB.Name {
		t.Errorf("expected matching DNSRecordSet name, got A=%q B=%q", drsA.Name, drsB.Name)
	}
	// Each Connector still stamps its own claim and identity labels — the
	// reconciler uses these to detect "is this DNSRecordSet mine".
	if drsA.Labels[irohDNSClaimedByUIDLabel] == drsB.Labels[irohDNSClaimedByUIDLabel] {
		t.Errorf("expected distinct claim labels, got both = %q", drsA.Labels[irohDNSClaimedByUIDLabel])
	}
	if drsA.Labels[irohDNSConnectorClusterLabel] == drsB.Labels[irohDNSConnectorClusterLabel] {
		t.Errorf("expected distinct cluster labels, got both = %q", drsA.Labels[irohDNSConnectorClusterLabel])
	}
}

func TestSortIrohAddresses(t *testing.T) {
	tests := []struct {
		name  string
		addrs []networkingv1alpha1.PublicKeyConnectorAddress
		want  []networkingv1alpha1.PublicKeyConnectorAddress
	}{
		{name: "empty", addrs: nil, want: []networkingv1alpha1.PublicKeyConnectorAddress{}},
		{
			name:  "single ipv4",
			addrs: []networkingv1alpha1.PublicKeyConnectorAddress{{Address: testIPv4, Port: 8080}},
			want:  []networkingv1alpha1.PublicKeyConnectorAddress{{Address: testIPv4, Port: 8080}},
		},
		{
			name: "input order is normalized — agent may report in any order",
			addrs: []networkingv1alpha1.PublicKeyConnectorAddress{
				{Address: testIPv6, Port: 9090},
				{Address: testIPv4, Port: 8080},
			},
			want: []networkingv1alpha1.PublicKeyConnectorAddress{
				{Address: testIPv4, Port: 8080},
				{Address: testIPv6, Port: 9090},
			},
		},
		{
			name: "same address different ports — sorted by port",
			addrs: []networkingv1alpha1.PublicKeyConnectorAddress{
				{Address: testIPv4, Port: 9090},
				{Address: testIPv4, Port: 8080},
			},
			want: []networkingv1alpha1.PublicKeyConnectorAddress{
				{Address: testIPv4, Port: 8080},
				{Address: testIPv4, Port: 9090},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sortIrohAddresses(tt.addrs)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("sortIrohAddresses[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestSortIrohAddresses_DoesNotMutateInput ensures we don't reorder the
// caller's slice — Connector.Status fields are shared with watchers and
// other reconciler passes.
func TestSortIrohAddresses_DoesNotMutateInput(t *testing.T) {
	original := []networkingv1alpha1.PublicKeyConnectorAddress{
		{Address: testIPv6, Port: 9090},
		{Address: testIPv4, Port: 8080},
	}
	want := []networkingv1alpha1.PublicKeyConnectorAddress{
		{Address: testIPv6, Port: 9090},
		{Address: testIPv4, Port: 8080},
	}
	_ = sortIrohAddresses(original)
	for i := range want {
		if original[i] != want[i] {
			t.Fatalf("input was mutated at index %d: got %+v, want %+v", i, original[i], want[i])
		}
	}
}

func TestIrohDNSReconcile_ClaimArbitration(t *testing.T) {
	log.SetLogger(zap.New(zap.UseDevMode(true)))

	const (
		ourUID     = types.UID("aaaaaaaa-0000-0000-0000-000000000001")
		foreignUID = "bbbbbbbb-0000-0000-0000-000000000002"

		dnsNamespace = "datum-dns"
	)

	recordName := "iroh-" + testEndpointZ32

	// irohConnectorClass routes "datum-connect" connectors to this controller.
	irohConnectorClass := &networkingv1alpha1.ConnectorClass{
		ObjectMeta: metav1.ObjectMeta{Name: "datum-connect"},
		Spec:       networkingv1alpha1.ConnectorClassSpec{ControllerName: "networking.datumapis.com/datum-connect"},
	}

	// makeConnector returns a Connector with the iroh finalizer already set and
	// valid connection details, so the first Reconcile reaches applyClaim without
	// an extra add-finalizer round-trip.
	makeConnector := func() *networkingv1alpha1.Connector {
		return &networkingv1alpha1.Connector{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "my-connector",
				Namespace:  "default",
				UID:        ourUID,
				Finalizers: []string{irohDNSFinalizer},
			},
			Spec: networkingv1alpha1.ConnectorSpec{ConnectorClassName: "datum-connect"},
			Status: networkingv1alpha1.ConnectorStatus{
				ConnectionDetails: &networkingv1alpha1.ConnectorConnectionDetails{
					Type: networkingv1alpha1.PublicKeyConnectorConnectionType,
					PublicKey: &networkingv1alpha1.ConnectorConnectionDetailsPublicKey{
						Id:        testEndpointHex,
						HomeRelay: testRelayURL,
					},
				},
			},
		}
	}

	// makeExistingRecord returns a DNSRecordSet whose claim label points at the
	// foreign connector — i.e., the pre-existing state before our connector reconciles.
	makeExistingRecord := func() *dnsv1alpha1.DNSRecordSet {
		return &dnsv1alpha1.DNSRecordSet{
			TypeMeta: metav1.TypeMeta{
				APIVersion: dnsv1alpha1.GroupVersion.String(),
				Kind:       "DNSRecordSet",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      recordName,
				Namespace: dnsNamespace,
				Labels: map[string]string{
					irohDNSClaimedByUIDLabel:       foreignUID,
					irohDNSConnectorClusterLabel:   encodeIrohClusterLabel("/foreign-project"),
					irohDNSConnectorNamespaceLabel: "default",
					irohDNSConnectorNameLabel:      "foreign-connector",
				},
			},
		}
	}

	dur30 := int32(30)

	// makeExpiredLease returns a downstream claim Lease for the record whose
	// RenewTime is far in the past — simulating an agent that stopped heartbeating.
	makeExpiredLease := func() *coordinationv1.Lease {
		return &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{Name: recordName, Namespace: dnsNamespace},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity:       ptr.To(foreignUID),
				LeaseDurationSeconds: &dur30,
				RenewTime:            &metav1.MicroTime{Time: time.Now().Add(-5 * time.Minute)},
			},
		}
	}

	// makeActiveLease returns a downstream claim Lease renewed just now —
	// simulating an owner whose agent is still alive.
	makeActiveLease := func() *coordinationv1.Lease {
		return &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{Name: recordName, Namespace: dnsNamespace},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity:       ptr.To(foreignUID),
				LeaseDurationSeconds: &dur30,
				RenewTime:            &metav1.MicroTime{Time: time.Now()},
			},
		}
	}

	tests := []struct {
		name               string
		existingRecord     *dnsv1alpha1.DNSRecordSet
		existingClaimLease *coordinationv1.Lease
		wantStatus         metav1.ConditionStatus
		wantReason         string
		wantOwnerUID       string // expected irohDNSClaimedByUIDLabel after reconcile; "" = don't assert record
	}{
		{
			name:         "no existing record — connector creates and owns it",
			wantStatus:   metav1.ConditionTrue,
			wantReason:   connectorReasonIrohOwner,
			wantOwnerUID: string(ourUID),
		},
		{
			name:           "the owner connector was deleted",
			existingRecord: makeExistingRecord(),
			wantStatus:     metav1.ConditionTrue,
			wantReason:     connectorReasonIrohOwner,
			wantOwnerUID:   string(ourUID),
		},
		{
			name:               "cross-project owner whose agent stopped heartbeating",
			existingRecord:     makeExistingRecord(),
			existingClaimLease: makeExpiredLease(),
			wantStatus:         metav1.ConditionTrue,
			wantReason:         connectorReasonIrohOwner,
			wantOwnerUID:       string(ourUID),
		},
		{
			name:               "owner is alive and active",
			existingRecord:     makeExistingRecord(),
			existingClaimLease: makeActiveLease(),
			wantStatus:         metav1.ConditionFalse,
			wantReason:         connectorReasonIrohDeferredToOwner,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testScheme := newIrohTestScheme(t)
			connector := makeConnector()

			upstreamCl := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(connector, irohConnectorClass).
				WithStatusSubresource(connector).
				Build()

			downstreamBuilder := fake.NewClientBuilder().WithScheme(testScheme)
			if tt.existingRecord != nil {
				downstreamBuilder = downstreamBuilder.WithObjects(tt.existingRecord)
			}
			if tt.existingClaimLease != nil {
				downstreamBuilder = downstreamBuilder.WithObjects(tt.existingClaimLease)
			}
			downstreamCl := downstreamBuilder.Build()

			reconciler := &IrohDNSReconciler{
				mgr:        &fakeMockManager{cl: upstreamCl},
				Downstream: &clusterWithClient{c: downstreamCl, scheme: testScheme},
				Config:     newReconciler().Config,
			}

			ctx := context.Background()
			_, err := reconciler.Reconcile(ctx, mcreconcile.Request{
				ClusterName: testClusterName,
				Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(connector)},
			})
			require.NoError(t, err)

			// Verify the IrohDNSPublished condition on the Connector.
			var updated networkingv1alpha1.Connector
			require.NoError(t, upstreamCl.Get(ctx, client.ObjectKeyFromObject(connector), &updated))

			cond := apimeta.FindStatusCondition(updated.Status.Conditions, connectorConditionIrohDNSPublished)
			require.NotNil(t, cond, "IrohDNSPublished condition must be set")
			assert.Equal(t, tt.wantStatus, cond.Status)
			assert.Equal(t, tt.wantReason, cond.Reason)

			// For ownership cases, verify the DNSRecordSet claim label and claim Lease.
			if tt.wantOwnerUID != "" {
				var record dnsv1alpha1.DNSRecordSet
				require.NoError(t, downstreamCl.Get(ctx, client.ObjectKey{Namespace: dnsNamespace, Name: recordName}, &record))
				assert.Equal(t, tt.wantOwnerUID, record.Labels[irohDNSClaimedByUIDLabel], "DNSRecordSet claim label")

				var claimLease coordinationv1.Lease
				require.NoError(t, downstreamCl.Get(ctx, client.ObjectKey{Namespace: dnsNamespace, Name: recordName}, &claimLease))
				assert.Equal(t, tt.wantOwnerUID, ptr.Deref(claimLease.Spec.HolderIdentity, ""), "claim Lease holder identity")
			}
		})
	}
}
