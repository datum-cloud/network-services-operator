package config

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestInternalDNSDefaultsRemainDisabled(t *testing.T) {
	cfg := &NetworkServicesOperator{}
	SetObjectDefaults_NetworkServicesOperator(cfg)
	if cfg.InternalDNS.Enabled {
		t.Fatal("internal DNS must require an explicit alpha feature gate")
	}
	if cfg.InternalDNS.Frontend != "fd53::53" || cfg.InternalDNS.ServicePrefix != "fd70:100::/64" {
		t.Fatalf("unexpected internal DNS defaults: %#v", cfg.InternalDNS)
	}
}

func TestInternalDNSValidation(t *testing.T) {
	cfg := InternalDNSConfig{
		Enabled: true, Frontend: "fd53::53", ServicePrefix: "fd70:100::/64",
		ProducerAttachmentSelector: map[string]string{"service.datum.net/internal-dns": "true"},
	}
	if err := cfg.validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

}

func TestInternalDNSResolvers(t *testing.T) {
	primary := map[string]string{"service.datum.net/internal-dns": "true", "internal-dns.datum.net/tier": "node"}
	secondary := map[string]string{"service.datum.net/internal-dns": "true", "internal-dns.datum.net/tier": "node-b"}

	single := InternalDNSConfig{ProducerAttachmentSelector: primary}
	SetDefaults_InternalDNSConfig(&single)
	if got := single.EffectiveResolvers(); len(got) != 1 || got[0].Frontend != "fd53::53" || got[0].ProducerAttachmentSelector["internal-dns.datum.net/tier"] != "node" {
		t.Fatalf("single resolver fields did not form a one-resolver list: %#v", got)
	}

	listed := InternalDNSConfig{Resolvers: []InternalDNSResolver{
		{Frontend: "fd53::53", ProducerAttachmentSelector: primary},
		{Frontend: "fd53::54", ProducerAttachmentSelector: secondary},
	}}
	SetDefaults_InternalDNSConfig(&listed)
	if listed.Frontend != "" {
		t.Fatalf("frontend defaulted alongside resolvers: %q", listed.Frontend)
	}
	if got := listed.Nameservers(); !slices.Equal(got, []string{"fd53::53", "fd53::54"}) {
		t.Fatalf("nameservers = %v, want resolvers in configuration order", got)
	}

	resolver := func(frontend string, selector map[string]string) InternalDNSResolver {
		return InternalDNSResolver{Frontend: frontend, ProducerAttachmentSelector: selector}
	}
	tests := []struct {
		name    string
		cfg     InternalDNSConfig
		wantSub string
	}{
		{
			name: "two resolvers",
			cfg:  InternalDNSConfig{Resolvers: []InternalDNSResolver{resolver("fd53::53", primary), resolver("fd53::54", secondary)}},
		},
		{
			name:    "resolvers with frontend",
			cfg:     InternalDNSConfig{Frontend: "fd53::53", Resolvers: []InternalDNSResolver{resolver("fd53::54", secondary)}},
			wantSub: "mutually exclusive",
		},
		{
			name:    "resolvers with producer selector",
			cfg:     InternalDNSConfig{ProducerAttachmentSelector: primary, Resolvers: []InternalDNSResolver{resolver("fd53::54", secondary)}},
			wantSub: "mutually exclusive",
		},
		{
			name: "more than three resolvers",
			cfg: InternalDNSConfig{Resolvers: []InternalDNSResolver{
				resolver("fd53::53", map[string]string{"tier": "a"}), resolver("fd53::54", map[string]string{"tier": "b"}),
				resolver("fd53::55", map[string]string{"tier": "c"}), resolver("fd53::56", map[string]string{"tier": "d"}),
			}},
			wantSub: "at most 3",
		},
		{
			name:    "repeated frontend in another spelling",
			cfg:     InternalDNSConfig{Resolvers: []InternalDNSResolver{resolver("fd53::54", primary), resolver("fd53:0::54", secondary)}},
			wantSub: "resolvers[1].frontend repeats",
		},
		{
			name:    "IPv4 frontend",
			cfg:     InternalDNSConfig{Resolvers: []InternalDNSResolver{resolver("10.0.0.53", primary)}},
			wantSub: "resolvers[0].frontend must be an IPv6 address",
		},
		{
			name:    "missing producer selector",
			cfg:     InternalDNSConfig{Resolvers: []InternalDNSResolver{resolver("fd53::53", primary), resolver("fd53::54", nil)}},
			wantSub: "resolvers[1].producerAttachmentSelector is required",
		},
		{
			name:    "same producer selector",
			cfg:     InternalDNSConfig{Resolvers: []InternalDNSResolver{resolver("fd53::53", primary), resolver("fd53::54", maps.Clone(primary))}},
			wantSub: "overlaps resolvers[0]",
		},
		{
			name: "producer selector contained in another",
			cfg: InternalDNSConfig{Resolvers: []InternalDNSResolver{
				resolver("fd53::53", primary), resolver("fd53::54", map[string]string{"service.datum.net/internal-dns": "true"}),
			}},
			wantSub: "overlaps resolvers[0]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			cfg.Enabled = true
			cfg.ServicePrefix = "fd70:100::/64"
			err := cfg.validate()
			if tt.wantSub == "" && err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
			if tt.wantSub != "" && (err == nil || !strings.Contains(err.Error(), tt.wantSub)) {
				t.Fatalf("expected an error containing %q, got %v", tt.wantSub, err)
			}
		})
	}
}

func TestDecodeInternalDNSResolvers(t *testing.T) {
	data := []byte(`apiVersion: apiserver.config.datumapis.com/v1alpha1
kind: CellControllerManager
internalDNS:
  enabled: true
  resolvers:
  - frontend: fd53::53
    producerAttachmentSelector:
      internal-dns.datum.net/tier: node
  - frontend: fd53::54
    producerAttachmentSelector:
      internal-dns.datum.net/tier: node-b
`)
	var cfg CellControllerManager
	decodeInto(t, data, &cfg)
	if got := cfg.InternalDNS.Nameservers(); !slices.Equal(got, []string{"fd53::53", "fd53::54"}) {
		t.Fatalf("nameservers = %v", got)
	}
	if err := cfg.InternalDNS.validate(); err != nil {
		t.Fatalf("decoded resolvers rejected: %v", err)
	}
}

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
			cfg := &NetworkServicesOperator{
				Connector: ConnectorConfig{Iroh: iroh},
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
			cfg := &NetworkServicesOperator{Gateway: tt.gateway}
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

func TestGatewayConfig_ValidateCertificateService(t *testing.T) {
	tests := []struct {
		name    string
		gateway GatewayConfig
		wantSub string
	}{
		{
			name: "disabled by default validates",
		},
		{
			name:    "enabled with hostname verification validates",
			gateway: GatewayConfig{CertificateService: CertificateServiceConfig{Enabled: true, SecretNamespace: "certificates-system"}},
		},
		{
			name:    "enabled without a secret namespace is rejected",
			gateway: GatewayConfig{CertificateService: CertificateServiceConfig{Enabled: true}},
			wantSub: "certificateService.secretNamespace is required",
		},
		{
			name: "enabled without hostname verification is rejected",
			gateway: GatewayConfig{
				CertificateService:          CertificateServiceConfig{Enabled: true, SecretNamespace: "certificates-system"},
				DisableHostnameVerification: true,
			},
			wantSub: "certificateService.enabled requires hostname verification",
		},
		{
			name:    "a trusted roots file without chain verification is rejected",
			gateway: GatewayConfig{CertificateService: CertificateServiceConfig{Enabled: true, SecretNamespace: "certificates-system", TrustedRootsFile: "/roots.pem"}},
			wantSub: "certificateService.trustedRootsFile requires certificateService.verifyChain",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &NetworkServicesOperator{Gateway: tt.gateway}
			err := cfg.Validate()
			if tt.wantSub == "" {
				if err != nil {
					t.Fatalf("expected nil, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantSub, err)
			}
		})
	}
}

func TestSetObjectDefaults_CertificateService(t *testing.T) {
	cfg := &NetworkServicesOperator{}
	SetObjectDefaults_NetworkServicesOperator(cfg)
	if cfg.Gateway.CertificateService.Enabled {
		t.Error("certificateService.enabled should default to false")
	}
	if cfg.Gateway.CertificateService.KubeconfigPath != "" {
		t.Errorf("certificateService.kubeconfigPath should default to empty, got %q", cfg.Gateway.CertificateService.KubeconfigPath)
	}
	if got, want := cfg.Gateway.CertificateService.SecretNamespace, "certificates-system"; got != want {
		t.Errorf("certificateService.secretNamespace = %q, want %q", got, want)
	}
	if cfg.Gateway.CertificateService.VerifyChain {
		t.Error("certificateService.verifyChain should default to false")
	}
}

func TestCertificateServiceConfig_TrustedRoots(t *testing.T) {
	off := CertificateServiceConfig{TrustedRootsFile: "/does/not/exist"}
	if pool, err := off.TrustedRoots(); pool != nil || err != nil {
		t.Fatalf("verification off should load nothing, got %v, %v", pool, err)
	}

	system := CertificateServiceConfig{VerifyChain: true}
	if pool, err := system.TrustedRoots(); pool != nil || err != nil {
		t.Fatalf("no roots file means the system roots, got %v, %v", pool, err)
	}

	empty := filepath.Join(t.TempDir(), "empty.pem")
	if err := os.WriteFile(empty, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&CertificateServiceConfig{VerifyChain: true, TrustedRootsFile: empty}).TrustedRoots(); err == nil {
		t.Fatal("a roots file with no certificates should be refused")
	}
}

// A negative authority-loss period is refused rather than silently turning the
// hold off; zero, the documented off switch, is accepted.
func TestGatewayConfig_ValidateAuthorityLossGracePeriod(t *testing.T) {
	for period, wantErr := range map[time.Duration]bool{-time.Hour: true, 0: false, 48 * time.Hour: false} {
		cfg := GatewayConfig{TargetDomain: "datumproxy.net", DNSAuthorityLossGracePeriod: &metav1.Duration{Duration: period}}
		err := cfg.validate()
		if (err != nil) != wantErr {
			t.Errorf("period %s: validate() = %v, want an error: %v", period, err, wantErr)
		}
	}
}

// The estate's config file is decoded strictly, so the shape infra writes must
// decode as is: quantities as Kubernetes writes them, and http3 as an empty object.
func TestDecodeDownstreamClientTraffic(t *testing.T) {
	data := []byte(`apiVersion: apiserver.config.datumapis.com/v1alpha1
kind: NetworkServicesOperator
gateway:
  downstreamClientTraffic:
    http2:
      initialStreamWindowSize: 1Mi
      initialConnectionWindowSize: 16Mi
    http3: {}
    alpnProtocols: [h2, http/1.1]
`)
	var cfg NetworkServicesOperator
	decodeInto(t, data, &cfg)

	traffic := cfg.Gateway.DownstreamClientTraffic
	if traffic.IsEmpty() {
		t.Fatal("expected the configured fields to decode")
	}
	if got := traffic.HTTP2.InitialStreamWindowSize.Value(); got != 1<<20 {
		t.Errorf("expected a 1 MiB stream window, got %d", got)
	}
	if got := traffic.HTTP2.InitialConnectionWindowSize.Value(); got != 16<<20 {
		t.Errorf("expected a 16 MiB connection window, got %d", got)
	}
	if traffic.HTTP3 == nil {
		t.Error("expected http3: {} to turn HTTP/3 on")
	}
	want := []envoygatewayv1alpha1.ALPNProtocol{"h2", "http/1.1"}
	if !slices.Equal(traffic.ALPNProtocols, want) {
		t.Errorf("expected alpnProtocols %v, got %v", want, traffic.ALPNProtocols)
	}
}

func TestDownstreamClientTrafficUnsetIsEmpty(t *testing.T) {
	var cfg NetworkServicesOperator
	decodeInto(t, []byte("apiVersion: apiserver.config.datumapis.com/v1alpha1\nkind: NetworkServicesOperator\n"), &cfg)
	if !cfg.Gateway.DownstreamClientTraffic.IsEmpty() {
		t.Error("expected an estate that sets nothing to write no policy")
	}
}

// ALPN alone is enough to write a policy; an empty list is not, since it would
// turn ALPN off rather than leave the default.
func TestDownstreamClientTrafficALPNIsEmpty(t *testing.T) {
	alpn := DownstreamClientTrafficConfig{ALPNProtocols: []envoygatewayv1alpha1.ALPNProtocol{"h2", "http/1.1"}}
	if alpn.IsEmpty() {
		t.Error("expected listed ALPN protocols to write a policy")
	}
	if !(DownstreamClientTrafficConfig{ALPNProtocols: []envoygatewayv1alpha1.ALPNProtocol{}}).IsEmpty() {
		t.Error("expected an empty ALPN list to write nothing")
	}
}

// A protocol Envoy does not know is refused at start: the edge would negotiate
// without it, and nothing the operator reads would say so.
func TestGatewayConfig_ValidateALPNProtocols(t *testing.T) {
	for _, tt := range []struct {
		name    string
		alpn    []envoygatewayv1alpha1.ALPNProtocol
		wantSub string
	}{
		{name: "h2 and http/1.1", alpn: []envoygatewayv1alpha1.ALPNProtocol{"h2", "http/1.1"}},
		{name: "every known protocol", alpn: []envoygatewayv1alpha1.ALPNProtocol{"h2", "http/1.1", "http/1.0"}},
		{name: "a typo", alpn: []envoygatewayv1alpha1.ALPNProtocol{"h2", "http1.1"}, wantSub: `"http1.1"`},
		{name: "h3 belongs to http3", alpn: []envoygatewayv1alpha1.ALPNProtocol{"h3"}, wantSub: `"h3"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &NetworkServicesOperator{Gateway: GatewayConfig{DownstreamClientTraffic: DownstreamClientTrafficConfig{
				ALPNProtocols: tt.alpn,
			}}}
			err := cfg.Validate()
			if tt.wantSub == "" && err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
			if tt.wantSub != "" && (err == nil || !strings.Contains(err.Error(), tt.wantSub)) {
				t.Fatalf("expected an error containing %q, got %v", tt.wantSub, err)
			}
		})
	}
}

// An HTTP/2 window outside Envoy's range is refused at start: the edge would
// keep its defaults, and nothing the operator reads would say so.
func TestGatewayConfig_ValidateHTTP2Windows(t *testing.T) {
	tests := []struct {
		name, stream, connection, wantSub string
	}{
		{name: "the estate's values", stream: "1Mi", connection: "16Mi"},
		{name: "Envoy's bounds themselves", stream: "65535", connection: "2147483647"},
		{name: "a stream window below the bound", stream: "65534", connection: "16Mi", wantSub: "initialStreamWindowSize"},
		{name: "a connection window above the bound", stream: "1Mi", connection: "2Gi", wantSub: "initialConnectionWindowSize"},
		{name: "a fraction of a byte", stream: "1500m", connection: "16Mi", wantSub: "whole number of bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &NetworkServicesOperator{Gateway: GatewayConfig{DownstreamClientTraffic: DownstreamClientTrafficConfig{
				HTTP2: &envoygatewayv1alpha1.HTTP2Settings{
					InitialStreamWindowSize:     ptr.To(resource.MustParse(tt.stream)),
					InitialConnectionWindowSize: ptr.To(resource.MustParse(tt.connection)),
				},
			}}}
			err := cfg.Validate()
			if tt.wantSub == "" && err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
			if tt.wantSub != "" && (err == nil || !strings.Contains(err.Error(), tt.wantSub)) {
				t.Fatalf("expected an error containing %q, got %v", tt.wantSub, err)
			}
		})
	}
}
