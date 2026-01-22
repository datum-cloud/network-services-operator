package config

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	multiclusterproviders "go.miloapis.com/milo/pkg/multicluster-runtime"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/registrydata"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +k8s:defaulter-gen=true

type NetworkServicesOperator struct {
	metav1.TypeMeta

	MetricsServer MetricsServerConfig `json:"metricsServer"`

	WebhookServer WebhookServerConfig `json:"webhookServer"`

	Gateway GatewayConfig `json:"gateway"`

	HTTPProxy HTTPProxyConfig `json:"httpProxy"`

	Connector ConnectorConfig `json:"connector"`

	Discovery DiscoveryConfig `json:"discovery"`

	DownstreamResourceManagement DownstreamResourceManagementConfig `json:"downstreamResourceManagement"`

	// Redis provides shared Redis connection settings.
	Redis RedisConfig `json:"redis"`

	DomainVerification DomainVerificationConfig `json:"domainVerificationConfig"`

	// DomainRegistration controls RDAP/WHOIS refresh behavior for Domain status.registration
	DomainRegistration DomainRegistrationConfig `json:"domainRegistration"`
}

// +k8s:deepcopy-gen=true
type ConnectorConfig struct {
	// LeaseDurationSeconds is the number of seconds the connector lease is valid for.
	//
	// Defaults to 30 seconds.
	// +default=30
	LeaseDurationSeconds int32 `json:"leaseDurationSeconds,omitempty"`
}

// +k8s:deepcopy-gen=true

type WebhookServerConfig struct {
	// Host is the address that the server will listen on.
	// Defaults to "" - all addresses.
	Host string `json:"host"`

	// Port is the port number that the server will serve.
	// It will be defaulted to 9443 if unspecified.
	Port int `json:"port"`

	// TLS is the TLS configuration for the webhook server, allowing configuration
	// of what path to find a certificate and key in, and what file names to use.
	TLS TLSConfig `json:"tls"`

	// ClientCAName is the CA certificate name which server used to verify remote(client)'s certificate.
	// Defaults to "", which means server does not verify client's certificate.
	ClientCAName string `json:"clientCAName"`
}

func (c *WebhookServerConfig) Options(ctx context.Context, secretsClient client.Client) webhook.Options {
	opts := webhook.Options{
		Host:     c.Host,
		Port:     c.Port,
		CertDir:  c.TLS.CertDir,
		CertName: c.TLS.CertName,
		KeyName:  c.TLS.KeyName,
	}

	if secretRef := c.TLS.SecretRef; secretRef != nil {
		opts.TLSOpts = c.TLS.Options(ctx, secretsClient)
	}

	return opts
}

// +k8s:deepcopy-gen=true

type MetricsServerConfig struct {
	// SecureServing enables serving metrics via https.
	// Per default metrics will be served via http.
	SecureServing *bool `json:"secureServing,omitempty"`

	// BindAddress is the bind address for the metrics server.
	// It will be defaulted to "0" if unspecified.
	// Use :8443 for HTTPS or :8080 for HTTP
	//
	// Set this to "0" to disable the metrics server.
	BindAddress string `json:"bindAddress"`

	// TLS is the TLS configuration for the metrics server, allowing configuration
	// of what path to find a certificate and key in, and what file names to use.
	TLS TLSConfig `json:"tls"`
}

func SetDefaults_MetricsServerConfig(obj *MetricsServerConfig) {
	if obj.SecureServing == nil {
		obj.SecureServing = ptr.To(true)
	}

	if obj.BindAddress == "" {
		obj.BindAddress = "0"
	}
}

func (c *MetricsServerConfig) Options(ctx context.Context, secretsClient client.Client) metricsserver.Options {
	opts := metricsserver.Options{
		SecureServing: *c.SecureServing,
		BindAddress:   c.BindAddress,
		CertDir:       c.TLS.CertDir,
		CertName:      c.TLS.CertName,
		KeyName:       c.TLS.KeyName,
	}

	if *c.SecureServing {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/metrics/filters#WithAuthenticationAndAuthorization
		opts.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	if secretRef := c.TLS.SecretRef; secretRef != nil {
		opts.TLSOpts = c.TLS.Options(ctx, secretsClient)
	}

	return opts
}

// +k8s:deepcopy-gen=true

type TLSConfig struct {
	// SecretRef is a reference to a secret that contains the server key and
	// certificate. If provided, CertDir will be ignored, and CertName and KeyName
	// will be used as key names in the secret data.
	//
	// Note: This option is not currently recommended for production, as the secret
	// will be read from the API on every request.
	SecretRef *corev1.ObjectReference `json:"secretRef,omitempty"`

	// CertDir is the directory that contains the server key and certificate. Defaults to
	// <temp-dir>/k8s-webhook-server/serving-certs.
	CertDir string `json:"certDir"`

	// CertName is the server certificate name. Defaults to tls.crt.
	//
	// Note: This option is only used when TLSOpts does not set GetCertificate.
	CertName string `json:"certName"`

	// KeyName is the server key name. Defaults to tls.key.
	//
	// Note: This option is only used when TLSOpts does not set GetCertificate.
	KeyName string `json:"keyName"`
}

func (c *TLSConfig) Options(ctx context.Context, secretsClient client.Client) []func(*tls.Config) {
	var tlsOpts []func(*tls.Config)

	if secretRef := c.SecretRef; secretRef != nil {
		tlsOpts = append(tlsOpts, func(c *tls.Config) {
			logger := ctrl.Log.WithName("webhook-tls-client")
			c.GetCertificate = func(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				logger.Info("getting certificate")

				// Look at https://github.com/cert-manager/cert-manager/blob/master/pkg/server/tls/dynamic_source.go

				// TODO(jreese) caching & background refresh

				var secret corev1.Secret
				secretObjectKey := types.NamespacedName{
					Name:      secretRef.Name,
					Namespace: secretRef.Namespace,
				}
				if err := secretsClient.Get(ctx, secretObjectKey, &secret); err != nil {
					return nil, fmt.Errorf("failed to get secret: %w", err)
				}

				cert, err := tls.X509KeyPair(secret.Data["tls.crt"], secret.Data["tls.key"])
				if err != nil {
					return nil, fmt.Errorf("failed to parse certificate: %w", err)
				}

				return &cert, nil
			}
		})
	}

	return tlsOpts
}

func SetDefaults_TLSConfig(obj *TLSConfig) {
	if len(obj.CertDir) == 0 {
		obj.CertDir = filepath.Join(os.TempDir(), "k8s-metrics-server", "serving-certs")
	}

	if len(obj.CertName) == 0 {
		obj.CertName = "tls.crt"
	}

	if len(obj.KeyName) == 0 {
		obj.KeyName = "tls.key"
	}
}

// +k8s:deepcopy-gen=true

type DownstreamResourceManagementConfig struct {
	// KubeconfigPath is the path to the kubeconfig file to use when managing
	// downstream resources. When not provided, the operator will use the
	// in-cluster config.
	KubeconfigPath string `json:"kubeconfigPath"`
}

func (c *DownstreamResourceManagementConfig) RestConfig() (*rest.Config, error) {
	if c.KubeconfigPath == "" {
		return ctrl.GetConfig()
	}

	return clientcmd.BuildConfigFromFlags("", c.KubeconfigPath)
}

// +k8s:deepcopy-gen=true

type DomainVerificationConfig struct {
	// Intervals to retry verification attempts, processed in the order they are
	// defined.
	//
	// If maxElapsed is not set on an interval, any amount of elapsed time will
	// fall into that interval.
	//
	// +default=[{"interval": "5s", "maxElapsed": "5m"}, {"interval": "1m", "maxElapsed": "15m"}, {"interval": "5m"}]
	RetryIntervals []RetryInterval `json:"retryIntervals"`

	// Maximum jitter factor to apply to retry intervals
	//
	// +default=0.25
	RetryJitterMaxFactor float64 `json:"retryJitterMaxFactor"`

	// Maximum number of domain verifications that can be processed concurrently.
	//
	// +default=20
	MaxConcurrentVerifications int `json:"maxConcurrentVerifications"`

	// Prefix to the DNS record used for verification. Will be suffixed by the
	// value in `spec.domainName` of a Domain resource.
	//
	// +default="datum-custom-hostname"
	DNSVerificationRecordPrefix string `json:"dnsVerificationRecordPrefix"`

	// Path for the HTTP token used for verification. Will be suffixed by the
	// UID of a Domain resource.
	//
	// +default=".well-known/datum-custom-hostname-challenge"
	HTTPVerificationTokenPath string `json:"httpVerificationTokenPath"`
}

// GetRetryInterval returns the interval to retry for a given amount of elapsed
// time. Returns 5 minutes if no matching retry interval was found.
func (c *DomainVerificationConfig) GetRetryInterval(elapsed time.Duration) time.Duration {
	for _, interval := range c.RetryIntervals {
		if interval.MaxElapsed == nil || elapsed <= interval.MaxElapsed.Duration {
			return interval.Interval.Duration
		}
	}

	return 5 * time.Minute
}

// +k8s:deepcopy-gen=true

type DomainRegistrationConfig struct {
	// RefreshInterval controls how often to refresh registration data
	// +default="24h"
	RefreshInterval *metav1.Duration `json:"refreshInterval"`

	// RetryBackoff controls retry delay after failures
	// +default="1h"
	RetryBackoff *metav1.Duration `json:"retryBackoff"`

	// JitterMaxFactor is max jitter factor when scheduling refreshes
	// +default=0.2
	JitterMaxFactor float64 `json:"jitterMaxFactor"`

	// LookupTimeout bounds RDAP/WHOIS single lookup time
	// +default="15s"
	LookupTimeout *metav1.Duration `json:"lookupTimeout"`

	// WhoisBootstrapHost is the WHOIS server used to bootstrap TLD refer hosts
	// +default="whois.iana.org"
	WhoisBootstrapHost string `json:"whoisBootstrapHost"`

	// RegistryData configures caching and rate limiting used by registry lookups.
	RegistryData RegistryDataConfig `json:"registryData"`
}

// +k8s:deepcopy-gen=true
type RedisConfig struct {
	// URL is a full redis connection URL, e.g.:
	// "redis://:password@network-services-operator-redis.datum-system.svc.cluster.local:6379/0"
	//
	// NOTE: This may contain credentials. Prefer referencing a Secret in your deployment
	// and injecting it via an env var instead of embedding secrets in config files.
	//
	// If the env var REDIS_URL is set, it will override this value at runtime.
	URL string `json:"url,omitempty"`

	// DialTimeout controls how long we wait when establishing a Redis connection.
	// +default="5s"
	DialTimeout *metav1.Duration `json:"dialTimeout,omitempty"`

	// ReadTimeout controls how long we wait for Redis reads.
	// +default="3s"
	ReadTimeout *metav1.Duration `json:"readTimeout,omitempty"`

	// WriteTimeout controls how long we wait for Redis writes.
	// +default="3s"
	WriteTimeout *metav1.Duration `json:"writeTimeout,omitempty"`
}

// +k8s:deepcopy-gen=true
type RegistryDataConfig struct {
	Cache      RegistryDataCacheConfig      `json:"cache"`
	CacheTTLs  RegistryDataCacheTTLsConfig  `json:"cacheTTLs"`
	RateLimits RegistryDataRateLimitsConfig `json:"rateLimits"`
}

// +k8s:deepcopy-gen=true
type RegistryDataCacheConfig struct {
	// Backend selects the cache backend for registrydata.
	// +default="memory"
	Backend registrydata.CacheBackend `json:"backend,omitempty"`

	// RedisKeyPrefix is used for keys when backend is "redis".
	// +default="network-services-operator:"
	RedisKeyPrefix string `json:"redisKeyPrefix,omitempty"`
}

// +k8s:deepcopy-gen=true
type RegistryDataCacheTTLsConfig struct {
	// Domain controls how long we cache domain registration results.
	// +default="15m"
	Domain *metav1.Duration `json:"domain,omitempty"`

	// Nameserver controls how long we cache nameserver IP results.
	// +default="5m"
	Nameserver *metav1.Duration `json:"nameserver,omitempty"`

	// IPRegistrant controls how long we cache IP registrant results.
	// +default="6h"
	IPRegistrant *metav1.Duration `json:"ipRegistrant,omitempty"`
}

// +k8s:deepcopy-gen=true
type RegistryDataRateLimitsConfig struct {
	// DefaultRatePerSec controls the default provider token rate.
	// +default=1.0
	DefaultRatePerSec float64 `json:"defaultRatePerSec,omitempty"`

	// DefaultBurst controls the default provider burst size.
	// +default=5
	DefaultBurst float64 `json:"defaultBurst,omitempty"`

	// DefaultBlock is how long we block a provider after a rate limit response.
	// +default="2s"
	DefaultBlock *metav1.Duration `json:"defaultBlock,omitempty"`
}

// +k8s:deepcopy-gen=true

type RetryInterval struct {
	// Interval is how often verification attempts should occur.
	Interval metav1.Duration `json:"interval"`

	// MaxElapsed is the maximum amount of time that has elapsed since the previous
	// verification attempt for this interval to apply. If left empty
	MaxElapsed *metav1.Duration `json:"maxElapsed,omitempty"`
}

// +k8s:deepcopy-gen=true

type GatewayConfig struct {
	// ControllerName is the value that must be present in a GatewayClass for
	// the operator to manage gateways for that class.
	//
	// Defaults to "gateway.networking.datumapis.com/external-global-proxy-controller"
	ControllerName gatewayv1.GatewayController `json:"controllerName"`

	// TargetDomain is the domain that the operator should use when creating
	// DNS endpoints for gateways.
	TargetDomain string `json:"targetDomain"`

	// IPFamilies defines the IP families that should be enabled on gateways
	// created by the operator.
	//
	// Defaults to [IPv4, IPv6]
	IPFamilies []networkingv1alpha.IPFamily `json:"ipFamilies,omitempty"`

	// DownstreamGatewayClassName is the name of the GatewayClass that should be
	// used when programming gateways in the downstream cluster.
	DownstreamGatewayClassName string `json:"downstreamGatewayClassName"`

	// DownstreamGatewayNamespace is the namespace in the downstream cluster
	// where resources applicable to Gateways or GatewayClasses should be created.
	DownstreamGatewayNamespace string `json:"downstreamGatewayNamespace"`

	// DownstreamHostnameAccountingNamespace is the name of the namespace which
	// will be used to track which hostnames have been programmed across gateway
	// resources.
	//
	// +default="datum-downstream-gateway-hostnames"
	DownstreamHostnameAccountingNamespace string `json:"downstreamHostnameAccountingNamespace"`

	// Disable verification processing. Only intended for supporting ftw tests.
	DisableHostnameVerification bool `json:"disableHostnameVerification,omitempty"`

	// PermittedTLSOptions is a map of TLS options that are permitted on gateway
	// listeners. The key is the option name and the value is a list of permitted
	// option values. An empty list of values means that any value is permitted for	//
	// Defaults to an empty map.
	PermittedTLSOptions map[string][]string `json:"permittedTLSOptions,omitempty"`

	// ClusterIssuerMap is a map of external cluster issuer names to internal
	// ClusterIssuer resource names. If no entry is found for a given external
	// issuer name, the operator will use the value as is.
	ClusterIssuerMap map[string]string `json:"clusterIssuerMap,omitempty"`

	// PerGatewayCertificateIssuer will result in the operator to expect a
	// cert-manager Issuer to exist with the same name as the gateway. Any value
	// provided for the "gateway.networking.datumapis.com/certificate-issuer"
	// option will be replaced with the gateway's name. The Issuer resources will
	// be managed by Kyverno policies, and not by this operator.
	//
	// TODO(jreese) Remove this once we've either implemented DNS validation,
	// found a path to attach cert-manager generated routes to the gateway they're
	// needed for, or implement our own ACME integration.
	PerGatewayCertificateIssuer bool `json:"perGatewayCertificateIssuer,omitempty"`

	// ListenerTLSOptions specifies the TLS options to program on generated
	// TLS listeners.
	// +default={"gateway.networking.datumapis.com/certificate-issuer": "auto"}
	ListenerTLSOptions map[gatewayv1.AnnotationKey]gatewayv1.AnnotationValue `json:"listenerTLSOptions"`

	// Coraza specifies configuration for the Coraza WAF.
	Coraza CorazaConfig `json:"coraza,omitempty"`

	// ValidPortNumbers is a list of port numbers that are permitted on gateway
	// listeners.
	//
	// +default=[80,443]
	ValidPortNumbers []int `json:"validPortNumbers,omitempty"`

	// ValidProtocolTypes is a list of protocol types that are permitted on
	// gateway listeners.
	//
	// +default={"80": ["HTTP"], "443": ["HTTPS"]}
	ValidProtocolTypes map[int][]gatewayv1.ProtocolType `json:"validProtocolTypes,omitempty"`

	// ExtensionAPIValidationOptions provides configuration for validation of
	// extension APIs used by the Gateway.
	ExtensionAPIValidationOptions ExtensionAPIValidationOptions `json:"extensionAPIValidationOptions,omitempty"`

	// HTTPRoutes provides validation configuration for core Gateway API
	// HTTPRoute resources.
	HTTPRoutes HTTPRouteValidationOptions `json:"httpRoutes,omitempty"`

	// ResourceReplicator provides configuration for the Gateway resource
	// replicator.
	ResourceReplicator GatewayResourceReplicatorConfig `json:"resourceReplicator"`

	// EnableDownstreamCertificateSolver enables the downstream certificate
	// solver controller, which inspects certificates and challenges in order to
	// attach routes for HTTP challenges to downstream gateways. This is only
	// needed when the downstream cluster is a federation control plane such as
	// Karmada.
	EnableDownstreamCertificateSolver bool `json:"enableDownstreamCertificateSolver,omitempty"`
}

func (c *GatewayConfig) GatewayDNSAddress(gateway *gatewayv1.Gateway) string {
	return fmt.Sprintf("%s.%s", strings.ReplaceAll(string(gateway.UID), "-", ""), c.TargetDomain)
}

// +k8s:deepcopy-gen=true

type CorazaConfig struct {
	// Disable TrafficProtectionPolicy programming for Coraza.
	Disabled bool `json:"disabled"`

	// Globally unique ID for a dynamic library file.
	//
	// +default="coraza-waf"
	LibraryID string `json:"libraryID,omitempty"`

	// Path to the Coraza dynamic library file.
	//
	// +default="/opt/coraza-waf/coraza-waf.so"
	LibraryPath string `json:"libraryPath,omitempty"`

	// Name of the filter to use in Envoy listener configurations
	//
	// +default="coraza-waf"
	FilterName string `json:"filterName,omitempty"`

	// Globally unique name of the Coraza plugin.
	//
	// +default="coraza-waf"
	PluginName string `json:"pluginName,omitempty"`

	// Directives to define on listener filters.
	//
	// +default=[]
	ListenerDirectives []string `json:"listenerDirectives,omitempty"`

	// Base directives to define on route filter configs.
	//
	// +default=["Include @crs-setup-conf", "Include @recommended-conf"]
	RouteBaseDirectives []string `json:"routeBaseDirectives,omitempty"`

	// TraceRouteMetadataExtractor is a CEL expression used to extract metadata
	// stored in Envoy routes to inject into trace span attributes. MUST return
	// a map of string keys to values.
	TraceRouteMetadataExtractor string `json:"traceRouteMetadataExtractor,omitempty"`
}

// +k8s:deepcopy-gen=true

type ExtensionAPIValidationOptions struct {
	// BackendTrafficPolicies specifies validation options for BackendTrafficPolicy resources.
	BackendTrafficPolicies BackendTrafficPolicyValidationOptions `json:"backendTrafficPolicies"`

	// HTTPRouteFilters specifies validation options for HTTPRouteFilter resources.
	HTTPRouteFilters HTTPRouteFilterValidationOptions `json:"httpRouteFilters"`

	// SecurityPolicies specifies validation options for SecurityPolicy resources.
	SecurityPolicies SecurityPolicyValidationOptions `json:"securityPolicies"`
}

// +k8s:deepcopy-gen=true

type HTTPRouteValidationOptions struct {
	// AllowServiceBackends enables referencing core/v1 Services directly from
	// HTTPRoute backendRefs. This is disabled by default as the operator is
	// typically deployed against a Datum control plane, which does not have the
	// Service type registered. Primarily useful for conformance-style testing
	// where upstream manifests rely on Services.
	AllowServiceBackends bool `json:"allowServiceBackends,omitempty"`
}

// +k8s:deepcopy-gen=true

type BackendTrafficPolicyValidationOptions struct {
	ClusterSettings ClusterSettingsValidationOptions
}

// +k8s:deepcopy-gen=true

type ClusterSettingsValidationOptions struct {
	// Minimum amount for the total number of unacknowledged probes to send before
	// deciding the connection is dead.
	//
	// +default=9
	TCPKeepaliveMinProbes uint32

	// Minimum amount for the duration a connection needs to be idle before
	// keep-alive probes start being sent.
	//
	// +default="5m"
	TCPKeepaliveMinIdleTime *metav1.Duration

	// Minimum amount for the duration between keep-alive probes.
	//
	// +default="30s"
	TCPKeepaliveMinInterval *metav1.Duration

	// Maximum time allowed for a connection timeout
	//
	// +default="10s"
	TCPMaxConnectionTimeout *metav1.Duration

	// Maximum amount for the duration a connection can be idle.
	//
	// +default="1h"
	HTTPMaxConnectionIdleTimeout *metav1.Duration

	// Maximum amount for the duration of a connection.
	//
	// +default="1h"
	HTTPMaxConnectionDuration *metav1.Duration

	// Maximum amount for the duration until an entire request is received by the
	// upstream.
	//
	// +default="1h"
	HTTPMaxRequestTimeout *metav1.Duration

	// Maximum size for upstream connection buffers
	//
	// +default="512Ki"
	ConnectionMaxBufferLimit *resource.Quantity

	// Minimum amount for the duration between DNS refreshes.
	//
	// +default="30s"
	DNSMinRefreshRate *metav1.Duration

	// Maximum size for the initial stream window size for HTTP/2 connections.
	//
	// +default="64Ki"
	HTTP2MaxInitialStreamWindowSize *resource.Quantity

	// Maximum size for the initial connection window size for HTTP/2 connections.
	//
	// +default="1Mi"
	HTTP2MaxInitialConnectionWindowSize *resource.Quantity

	// Maximum number of concurrent streams for HTTP/2 connections.
	//
	// +default=1024
	HTTP2MaxConcurrentStreams uint32
}

type HTTPRouteFilterValidationOptions struct {
	// MaxInlineBodySize is the maximum allowed size for an inline body in a
	// direct response filter.
	//
	// +default=1024
	MaxInlineBodySize int
}

// +k8s:deepcopy-gen=true

type SecurityPolicyValidationOptions struct {
	// APIKeyAuth specifies validation options for API key authentication
	APIKeyAuth APIKeyAuthValidationOptions

	// CORS specifies validation options for CORS
	CORS CORSValidationOptions

	// JWTProvider specifies validation options for JWT providers
	JWTProvider JWTProviderValidationOptions

	// OIDC specifies validation options for OIDC
	OIDC OIDCValidationOptions

	// Authorization specifies validation options for authorization
	Authorization AuthorizationValidationOptions

	// ClusterSettings specifies validation options for cluster settings used
	// within security policies.
	ClusterSettings ClusterSettingsValidationOptions
}

type APIKeyAuthValidationOptions struct {
	// MaxCredentialRefs is the maximum number of credential references per
	// SecurityPolicy.
	//
	// +default=5
	MaxCredentialRefs int

	// MaxExtractFrom is the maximum number of extractFrom entries per SecurityPolicy
	//
	// +default=5
	MaxExtractFrom int

	// MaxExtractFromFieldLength is the maximum length of each field in an
	// extractFrom entry.
	//
	// +default=10
	MaxExtractFromFieldLength int

	// MaxForwardClientIDHeaderLength is the maximum length for the name of the
	// header to use when forwarding the client identity to the upstream service.
	//
	// +default=256
	MaxForwardClientIDHeaderLength int
}

type CORSValidationOptions struct {
	// MaxFieldLength is the maximum length for each field in a CORS policy.
	//
	// +default=10
	MaxFieldLength int
}

type JWTProviderValidationOptions struct {
	// MaxClaimToHeaders is the maximum number of claim to header mappings per
	// JWT provider.
	//
	// +default=5
	MaxClaimToHeaders int

	// MaxExtractorLength is the maximum length of each extractor field.
	//
	// +default=5
	MaxExtractorLength int
}

// +k8s:deepcopy-gen=true

type OIDCValidationOptions struct {
	// MaxScopes is the maximum number of scopes per OIDC configuration.
	//
	// +default=5
	MaxScopes int

	// MaxResources is the maximum number of resources per OIDC configuration.
	//
	// +default=5
	MaxResources int

	// MinRefreshTokenTTL is the minimum allowed TTL for refresh tokens.
	//
	// +default="5m"
	MinRefreshTokenTTL *metav1.Duration
}

type AuthorizationValidationOptions struct {
	// MaxRules is the maximum number of authorization rules per SecurityPolicy.
	//
	// +default=20
	MaxRules int

	// MaxClientCIDRs is the maximum number of client CIDRs per authorization rule.
	//
	// +default=5
	MaxClientCIDRs int
}

// +k8s:deepcopy-gen=true

type GatewayResourceReplicatorConfig struct {
	// Resources lists the upstream resource types that should be mirrored into
	// the downstream control plane.
	Resources []ReplicatedResourceConfig `json:"resources"`
}

// +k8s:deepcopy-gen=true

type ReplicatedResourceConfig struct {
	// Group is the API group of the upstream resource to replicate.
	Group string `json:"group"`

	// Version is the API version of the upstream resource to replicate.
	Version string `json:"version"`

	// Kind is the API kind of the upstream resource to replicate.
	Kind string `json:"kind"`

	// LabelSelector limits which upstream objects are replicated in the
	// downstream control plane.
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`
}

// +k8s:deepcopy-gen=true

type HTTPProxyConfig struct {
	// GatewayClassName specifies which GatewayClass to use when programming the
	// underlying Gateway for an HTTPProxy.
	// +default="datum-external-global-proxy"
	GatewayClassName gatewayv1.ObjectName `json:"gatewayClassName"`
}

// +k8s:deepcopy-gen=true

type CustomHostnameAllowListEntry struct {
	// ClusterName is the name of the cluster that the hostname suffixes apply to.
	ClusterName string `json:"clusterName"`

	// Suffixes is a list of allowed hostname suffixes for the cluster.
	Suffixes []string `json:"suffixes"`
}

func SetDefaults_GatewayConfig(obj *GatewayConfig) {
	if obj.ControllerName == "" {
		obj.ControllerName = gatewayv1.GatewayController("gateway.networking.datumapis.com/external-global-proxy-controller")
	}

	if obj.IPFamilies == nil {
		obj.IPFamilies = []networkingv1alpha.IPFamily{
			networkingv1alpha.IPv4Protocol,
			networkingv1alpha.IPv6Protocol,
		}
	}
}

func SetDefaults_GatewayResourceReplicatorConfig(obj *GatewayResourceReplicatorConfig) {
	if len(obj.Resources) > 0 {
		return
	}

	obj.Resources = []ReplicatedResourceConfig{
		{Group: "", Version: "v1", Kind: "ConfigMap", LabelSelector: &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{
					Key:      "networking.datumapis.com/gateway-sync",
					Operator: metav1.LabelSelectorOpExists,
				},
			},
		}},
		{Group: "", Version: "v1", Kind: "Secret", LabelSelector: &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{
					Key:      "networking.datumapis.com/gateway-sync",
					Operator: metav1.LabelSelectorOpExists,
				},
			},
		}},
		{Group: "gateway.envoyproxy.io", Version: "v1alpha1", Kind: "Backend"},
		{Group: "gateway.envoyproxy.io", Version: "v1alpha1", Kind: "BackendTrafficPolicy"},
		{Group: "gateway.envoyproxy.io", Version: "v1alpha1", Kind: "SecurityPolicy"},
		{Group: "gateway.envoyproxy.io", Version: "v1alpha1", Kind: "HTTPRouteFilter"},
		// Propagate v1alpha3 until v1 is supported by Envoy Gateway
		{Group: "gateway.networking.k8s.io", Version: "v1alpha3", Kind: "BackendTLSPolicy"},
	}
}

func (c *GatewayConfig) IPv4Enabled() bool {
	return slices.Contains(c.IPFamilies, networkingv1alpha.IPv4Protocol)
}

func (c *GatewayConfig) IPv6Enabled() bool {
	return slices.Contains(c.IPFamilies, networkingv1alpha.IPv6Protocol)
}

// +k8s:deepcopy-gen=true

type DiscoveryConfig struct {
	// Mode is the mode that the operator should use to discover clusters.
	//
	// Defaults to "single"
	Mode multiclusterproviders.Provider `json:"mode"`

	// InternalServiceDiscovery will result in the operator to connect to internal
	// service addresses for projects.
	InternalServiceDiscovery bool `json:"internalServiceDiscovery"`

	// DiscoveryKubeconfigPath is the path to the kubeconfig file to use for
	// project discovery. When not provided, the operator will use the in-cluster
	// config.
	DiscoveryKubeconfigPath string `json:"discoveryKubeconfigPath"`

	// ProjectKubeconfigPath is the path to the kubeconfig file to use as a
	// template when connecting to project control planes. When not provided,
	// the operator will use the in-cluster config.
	ProjectKubeconfigPath string `json:"projectKubeconfigPath"`
}

func SetDefaults_DiscoveryConfig(obj *DiscoveryConfig) {
	if obj.Mode == "" {
		obj.Mode = multiclusterproviders.ProviderSingle
	}
}

func (c *DiscoveryConfig) DiscoveryRestConfig() (*rest.Config, error) {
	if c.DiscoveryKubeconfigPath == "" {
		return ctrl.GetConfig()
	}

	return clientcmd.BuildConfigFromFlags("", c.DiscoveryKubeconfigPath)
}

func (c *DiscoveryConfig) ProjectRestConfig() (*rest.Config, error) {
	if c.ProjectKubeconfigPath == "" {
		return ctrl.GetConfig()
	}

	return clientcmd.BuildConfigFromFlags("", c.ProjectKubeconfigPath)
}

func init() {
	SchemeBuilder.Register(&NetworkServicesOperator{})
}
