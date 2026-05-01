// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	dnsv1alpha1 "go.miloapis.com/dns-operator/api/v1alpha1"

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
)

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
	if len(drs.Spec.Records) != 2 {
		t.Fatalf("Records count = %d, want 2 (relay + addr)", len(drs.Spec.Records))
	}
	gotContents := []string{drs.Spec.Records[0].TXT.Content, drs.Spec.Records[1].TXT.Content}
	wantContents := []string{
		"relay=https://relay.example.com",
		"addr=192.0.2.1:8080 [2001:db8::1]:9090",
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

func TestJoinIrohAddresses(t *testing.T) {
	tests := []struct {
		name  string
		addrs []networkingv1alpha1.PublicKeyConnectorAddress
		want  string
	}{
		{name: "empty", addrs: nil, want: ""},
		{
			name:  "single ipv4",
			addrs: []networkingv1alpha1.PublicKeyConnectorAddress{{Address: testIPv4, Port: 8080}},
			want:  "192.0.2.1:8080",
		},
		{
			name:  "single ipv6 — bracketed",
			addrs: []networkingv1alpha1.PublicKeyConnectorAddress{{Address: testIPv6, Port: 9090}},
			want:  "[2001:db8::1]:9090",
		},
		{
			name: "mixed ipv4 + ipv6",
			addrs: []networkingv1alpha1.PublicKeyConnectorAddress{
				{Address: testIPv4, Port: 8080},
				{Address: testIPv6, Port: 9090},
			},
			want: "192.0.2.1:8080 [2001:db8::1]:9090",
		},
		{
			name: "input order is normalized — agent may report in any order",
			addrs: []networkingv1alpha1.PublicKeyConnectorAddress{
				{Address: testIPv6, Port: 9090},
				{Address: testIPv4, Port: 8080},
			},
			want: "192.0.2.1:8080 [2001:db8::1]:9090",
		},
		{
			name: "same address different ports — sorted by port",
			addrs: []networkingv1alpha1.PublicKeyConnectorAddress{
				{Address: testIPv4, Port: 9090},
				{Address: testIPv4, Port: 8080},
			},
			want: "192.0.2.1:8080 192.0.2.1:9090",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinIrohAddresses(tt.addrs); got != tt.want {
				t.Errorf("joinIrohAddresses = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestJoinIrohAddresses_DoesNotMutateInput ensures we don't reorder the
// caller's slice — Connector.Status fields are shared with watchers and
// other reconciler passes.
func TestJoinIrohAddresses_DoesNotMutateInput(t *testing.T) {
	original := []networkingv1alpha1.PublicKeyConnectorAddress{
		{Address: testIPv6, Port: 9090},
		{Address: testIPv4, Port: 8080},
	}
	want := []networkingv1alpha1.PublicKeyConnectorAddress{
		{Address: testIPv6, Port: 9090},
		{Address: testIPv4, Port: 8080},
	}
	_ = joinIrohAddresses(original)
	for i := range want {
		if original[i] != want[i] {
			t.Fatalf("input was mutated at index %d: got %+v, want %+v", i, original[i], want[i])
		}
	}
}
