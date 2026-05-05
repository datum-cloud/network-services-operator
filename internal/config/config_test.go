package config

import (
	"strings"
	"testing"
)

func TestNetworkServicesOperator_Validate_IrohDisabled(t *testing.T) {
	// When DNSEnabled is false the rest of the iroh config is allowed to
	// be empty — nothing depends on it.
	cfg := &NetworkServicesOperator{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestNetworkServicesOperator_Validate_IrohEnabled(t *testing.T) {
	full := IrohConnectorConfig{
		DNSEnabled: true,
		DNSZoneRef: IrohDNSZoneRef{Namespace: "datum-dns", Name: "datumconnect-net"},
	}

	tests := []struct {
		name    string
		mutate  func(*IrohConnectorConfig)
		wantSub string
	}{
		{name: "all required fields set"},
		{
			name:    "missing dnsZoneRef.name",
			mutate:  func(c *IrohConnectorConfig) { c.DNSZoneRef.Name = "" },
			wantSub: "dnsZoneRef.name is required",
		},
		{
			name:    "missing dnsZoneRef.namespace",
			mutate:  func(c *IrohConnectorConfig) { c.DNSZoneRef.Namespace = "" },
			wantSub: "dnsZoneRef.namespace is required",
		},
		{
			name: "downstream kubeconfig path is optional (in-cluster fallback)",
			mutate: func(c *IrohConnectorConfig) {
				c.DownstreamKubeconfigPath = ""
			},
		},
		{
			name:   "recordSuffix is optional (records sit under zone root)",
			mutate: func(c *IrohConnectorConfig) { c.RecordSuffix = "" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			iroh := full
			if tt.mutate != nil {
				tt.mutate(&iroh)
			}
			cfg := &NetworkServicesOperator{Connector: ConnectorConfig{Iroh: iroh}}
			err := cfg.Validate()
			if tt.wantSub == "" {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantSub)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("expected error containing %q, got %q", tt.wantSub, err.Error())
			}
		})
	}
}

func TestNetworkServicesOperator_Validate_IrohEnabledAggregatesErrors(t *testing.T) {
	cfg := &NetworkServicesOperator{
		Connector: ConnectorConfig{Iroh: IrohConnectorConfig{DNSEnabled: true}},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// errors.Join joins distinct messages with newlines; both required
	// fields should be surfaced.
	for _, want := range []string{
		"dnsZoneRef.name is required",
		"dnsZoneRef.namespace is required",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected error to mention %q, got %q", want, err.Error())
		}
	}
}

func TestSetObjectDefaults_IrohConnectorConfig(t *testing.T) {
	cfg := &NetworkServicesOperator{}
	SetObjectDefaults_NetworkServicesOperator(cfg)

	iroh := cfg.Connector.Iroh
	if got, want := iroh.RecordPrefix, "_iroh"; got != want {
		t.Errorf("RecordPrefix = %q, want %q", got, want)
	}
	if got, want := iroh.TTLSeconds, int32(30); got != want {
		t.Errorf("TTLSeconds = %d, want %d", got, want)
	}
	if iroh.DownstreamKubeconfigPath != "" {
		t.Errorf("DownstreamKubeconfigPath should default to empty (in-cluster), got %q", iroh.DownstreamKubeconfigPath)
	}
	if iroh.DNSEnabled {
		t.Error("DNSEnabled should default to false")
	}
}
