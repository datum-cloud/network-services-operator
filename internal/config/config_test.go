package config

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

func controlPlaneOnly() ControllersConfig {
	return ControllersConfig{Sets: []ControllerSet{ControllerSetControlPlane}}
}

func TestNetworkServicesOperator_Validate_IrohDisabled(t *testing.T) {
	// When DNSEnabled is false the rest of the iroh config is allowed to
	// be empty — nothing depends on it.
	cfg := &NetworkServicesOperator{Controllers: controlPlaneOnly()}
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
			cfg := &NetworkServicesOperator{
				Controllers: controlPlaneOnly(),
				Connector:   ConnectorConfig{Iroh: iroh},
			}
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
		Controllers: controlPlaneOnly(),
		Connector:   ConnectorConfig{Iroh: IrohConnectorConfig{DNSEnabled: true}},
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
	if got, want := iroh.TTLSeconds, int32(5); got != want {
		t.Errorf("TTLSeconds = %d, want %d", got, want)
	}
	if iroh.DownstreamKubeconfigPath != "" {
		t.Errorf("DownstreamKubeconfigPath should default to empty (in-cluster), got %q", iroh.DownstreamKubeconfigPath)
	}
	if iroh.DNSEnabled {
		t.Error("DNSEnabled should default to false")
	}
}

func TestGatewayConfig_ValidateLegacyTargetDomains(t *testing.T) {
	tests := []struct {
		name    string
		gateway GatewayConfig
		wantSub string
	}{
		{
			name:    "empty list",
			gateway: GatewayConfig{TargetDomain: "datumproxy.net"},
		},
		{
			name: "legacy domain set",
			gateway: GatewayConfig{
				TargetDomain:        "datumproxy.net",
				LegacyTargetDomains: []string{"prism.global.datum-dns.net"},
			},
		},
		{
			name: "empty entry",
			gateway: GatewayConfig{
				TargetDomain:        "datumproxy.net",
				LegacyTargetDomains: []string{""},
			},
			wantSub: "must not be empty",
		},
		{
			name: "leading dot",
			gateway: GatewayConfig{
				TargetDomain:        "datumproxy.net",
				LegacyTargetDomains: []string{".prism.global.datum-dns.net"},
			},
			wantSub: "must be a bare domain",
		},
		{
			name: "repeats target domain",
			gateway: GatewayConfig{
				TargetDomain:        "datumproxy.net",
				LegacyTargetDomains: []string{"datumproxy.net"},
			},
			wantSub: "must not repeat targetDomain",
		},
		{
			name: "duplicate entry",
			gateway: GatewayConfig{
				TargetDomain:        "datumproxy.net",
				LegacyTargetDomains: []string{"prism.global.datum-dns.net", "prism.global.datum-dns.net"},
			},
			wantSub: "duplicate entry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &NetworkServicesOperator{Controllers: controlPlaneOnly(), Gateway: tt.gateway}
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
				t.Fatalf("expected error containing %q, got %v", tt.wantSub, err)
			}
		})
	}
}

func TestGatewayConfig_ManagedTargetDomains(t *testing.T) {
	cfg := GatewayConfig{
		TargetDomain:        "datumproxy.net",
		LegacyTargetDomains: []string{"prism.global.datum-dns.net", ""},
	}

	got := cfg.ManagedTargetDomains()
	want := []string{"datumproxy.net", "prism.global.datum-dns.net"}

	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}

func TestSetDefaults_NetworkServicesOperator_ControllersEmptyRunsControlPlane(t *testing.T) {
	cfg := &NetworkServicesOperator{}
	SetObjectDefaults_NetworkServicesOperator(cfg)

	if !cfg.Enabled(ControllerSetControlPlane) {
		t.Errorf("expected control-plane to be enabled, got %v", cfg.Controllers.Sets)
	}
	if cfg.Enabled(ControllerSetCell) {
		t.Errorf("expected location to stay disabled, got %v", cfg.Controllers.Sets)
	}
}

func TestSetDefaults_NetworkServicesOperator_ControllersEmptyFollowsDeprecatedNetworkInterfaceEnabled(t *testing.T) {
	cfg := &NetworkServicesOperator{
		IPAM: IPAMConfig{KubeconfigPath: "/etc/ipam-cluster/kubeconfig"},
		NetworkInterface: NetworkInterfaceConfig{
			Enabled:  true,
			Location: LocationConfig{Name: "edge-1", Namespace: "datum-locations"},
		},
	}
	SetObjectDefaults_NetworkServicesOperator(cfg)

	for _, set := range AllControllerSets() {
		if !cfg.Enabled(set) {
			t.Errorf("expected set %q to be enabled, got %v", set, cfg.Controllers.Sets)
		}
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestSetDefaults_NetworkServicesOperator_ControllersKeepsExplicitSets(t *testing.T) {
	cfg := &NetworkServicesOperator{Controllers: controlPlaneOnly()}
	SetObjectDefaults_NetworkServicesOperator(cfg)

	if cfg.Enabled(ControllerSetCell) {
		t.Errorf("defaulting must not add sets, got %v", cfg.Controllers.Sets)
	}
	if !cfg.Enabled(ControllerSetControlPlane) {
		t.Errorf("expected control-plane to stay enabled, got %v", cfg.Controllers.Sets)
	}
}

func TestControllersConfig_Validate(t *testing.T) {
	location := LocationConfig{Name: "edge-1", Namespace: "datum-locations"}

	ipam := IPAMConfig{KubeconfigPath: "/etc/ipam-cluster/kubeconfig"}

	tests := []struct {
		name     string
		sets     []ControllerSet
		location LocationConfig
		ipam     IPAMConfig
		wantSubs []string
	}{
		{
			name:     "every set",
			sets:     AllControllerSets(),
			location: location,
			ipam:     ipam,
		},
		{
			name: "control-plane only needs no location or IPAM",
			sets: []ControllerSet{ControllerSetControlPlane},
		},
		{
			name:     "location only",
			sets:     []ControllerSet{ControllerSetCell},
			location: location,
			ipam:     ipam,
		},
		{
			name:     "empty",
			sets:     nil,
			wantSubs: []string{"sets must not be empty", "control-plane, cell"},
		},
		{
			name:     "unknown name",
			sets:     []ControllerSet{ControllerSetControlPlane, "region"},
			wantSubs: []string{`sets[1] is unknown controller set "region"`, "expected one of control-plane, cell"},
		},
		{
			name:     "duplicate name",
			sets:     []ControllerSet{ControllerSetControlPlane, ControllerSetControlPlane},
			wantSubs: []string{`sets[1] is a duplicate entry "control-plane"`},
		},
		{
			name:     "location without a location name or namespace",
			sets:     []ControllerSet{ControllerSetCell},
			ipam:     ipam,
			wantSubs: []string{"location.name is required", "location.namespace is required"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &NetworkServicesOperator{
				Controllers:      ControllersConfig{Sets: tt.sets},
				NetworkInterface: NetworkInterfaceConfig{Location: tt.location},
				IPAM:             tt.ipam,
			}
			err := cfg.Validate()

			if len(tt.wantSubs) == 0 {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected error containing %v, got nil", tt.wantSubs)
			}
			for _, want := range tt.wantSubs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("expected error to mention %q, got %q", want, err.Error())
				}
			}
		})
	}
}

func TestNetworkServicesOperator_Validate_LocationRequiresIPAMConnection(t *testing.T) {
	tests := []struct {
		name    string
		sets    []ControllerSet
		ipam    IPAMConfig
		wantSub string
	}{
		{
			name: "kubeconfig path",
			sets: []ControllerSet{ControllerSetCell},
			ipam: IPAMConfig{KubeconfigPath: "/etc/ipam-cluster/kubeconfig"},
		},
		{
			name: "in-cluster opt-in",
			sets: []ControllerSet{ControllerSetCell},
			ipam: IPAMConfig{InCluster: true},
		},
		{
			name:    "neither",
			sets:    []ControllerSet{ControllerSetCell},
			wantSub: "ipam: one of kubeconfigPath or inCluster is required",
		},
		{
			name:    "both",
			sets:    []ControllerSet{ControllerSetCell},
			ipam:    IPAMConfig{KubeconfigPath: "/etc/ipam-cluster/kubeconfig", InCluster: true},
			wantSub: "kubeconfigPath and inCluster are mutually exclusive",
		},
		{
			name: "control-plane keeps the in-cluster fallback",
			sets: []ControllerSet{ControllerSetControlPlane},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &NetworkServicesOperator{
				Controllers: ControllersConfig{Sets: tt.sets},
				NetworkInterface: NetworkInterfaceConfig{
					Location: LocationConfig{Name: "edge-1", Namespace: "datum-locations"},
				},
				IPAM: tt.ipam,
			}

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
				t.Errorf("expected error to mention %q, got %q", tt.wantSub, err.Error())
			}
		})
	}
}

func TestDecodeServerConfig_DeprecatedNetworkInterfaceEnabled(t *testing.T) {
	data := []byte(`apiVersion: apiserver.config.datumapis.com/v1alpha1
kind: NetworkServicesOperator
controllers:
  sets:
    - cell
ipam:
  kubeconfigPath: /etc/ipam-cluster/kubeconfig
networkInterface:
  enabled: true
  location:
    name: edge-1
    namespace: datum-locations
`)

	cfg := decodeServerConfig(t, data)

	if !cfg.NetworkInterface.Enabled {
		t.Error("expected the deprecated enabled field to decode")
	}
	if cfg.Enabled(ControllerSetControlPlane) {
		t.Errorf("expected control-plane to be disabled, got %v", cfg.Controllers.Sets)
	}
	if !cfg.Enabled(ControllerSetCell) {
		t.Errorf("expected location to be enabled, got %v", cfg.Controllers.Sets)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestDecodeServerConfig_ConfigWrittenBeforeControllerSets(t *testing.T) {
	data := []byte(`apiVersion: apiserver.config.datumapis.com/v1alpha1
kind: NetworkServicesOperator
gateway:
  targetDomain: prod.example.com
downstreamResourceManagement:
  kubeconfigPath: /etc/downstream-cluster/kubeconfig
`)

	cfg := decodeServerConfig(t, data)

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if !cfg.Enabled(ControllerSetControlPlane) {
		t.Errorf("expected control-plane to be enabled, got %v", cfg.Controllers.Sets)
	}
	if cfg.Enabled(ControllerSetCell) {
		t.Errorf("expected location to stay disabled, got %v", cfg.Controllers.Sets)
	}
}

func TestDecodeServerConfig_UnknownControllerSetRejectedByValidate(t *testing.T) {
	data := []byte(`apiVersion: apiserver.config.datumapis.com/v1alpha1
kind: NetworkServicesOperator
controllers:
  sets:
    - region
`)

	cfg := decodeServerConfig(t, data)

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), `"region"`) {
		t.Errorf("expected the offending set to be named, got %q", err.Error())
	}
}

func decodeServerConfig(t *testing.T, data []byte) *NetworkServicesOperator {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("unable to build scheme: %v", err)
	}
	if err := RegisterDefaults(scheme); err != nil {
		t.Fatalf("unable to register defaults: %v", err)
	}

	codecs := serializer.NewCodecFactory(scheme, serializer.EnableStrict)

	var cfg NetworkServicesOperator
	if err := runtime.DecodeInto(codecs.UniversalDecoder(), data, &cfg); err != nil {
		t.Fatalf("unable to decode config: %v", err)
	}
	return &cfg
}
