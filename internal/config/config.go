package config

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	corev1 "k8s.io/api/core/v1"
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

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/providers"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +k8s:defaulter-gen=true

type NetworkServicesOperator struct {
	metav1.TypeMeta

	MetricsServer MetricsServerConfig `json:"metricsServer"`

	WebhookServer WebhookServerConfig `json:"webhookServer"`

	Gateway GatewayConfig `json:"gateway"`

	Discovery DiscoveryConfig `json:"discovery"`

	DownstreamResourceManagement DownstreamResourceManagementConfig `json:"downstreamResourceManagement"`
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

	// Expand environment variables in the kubeconfig path
	expandedPath := os.ExpandEnv(c.KubeconfigPath)

	return clientcmd.BuildConfigFromFlags("", expandedPath)
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

	// ValidPortNumbers is a list of port numbers that are permitted on gateway
	// listeners.
	//
	// Defaults to [80, 443]
	ValidPortNumbers []int `json:"validPortNumbers,omitempty"`

	// ValidProtocolTypes is a list of protocol types that are permitted on
	// gateway listeners.
	//
	// Defaults to [HTTP, HTTPS]
	ValidProtocolTypes []gatewayv1.ProtocolType `json:"validProtocolTypes,omitempty"`

	// CustomHostnameAllowList is a list of allowed hostname suffixes for specific
	// clusters. Hostnames on gateways in a cluster must be a subdomain of one of
	// the suffixes in this list for that cluster.
	CustomHostnameAllowList []CustomHostnameAllowListEntry `json:"customHostnameAllowList,omitempty"`
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

	if len(obj.ValidPortNumbers) == 0 {
		obj.ValidPortNumbers = []int{80, 443}
	}

	if len(obj.ValidProtocolTypes) == 0 {
		obj.ValidProtocolTypes = []gatewayv1.ProtocolType{
			gatewayv1.HTTPProtocolType,
			gatewayv1.HTTPSProtocolType,
		}
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
	Mode providers.Provider `json:"mode"`

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
		obj.Mode = providers.ProviderSingle
	}
}

func (c *DiscoveryConfig) DiscoveryRestConfig() (*rest.Config, error) {
	if c.DiscoveryKubeconfigPath == "" {
		return ctrl.GetConfig()
	}

	// Expand environment variables in the kubeconfig path
	expandedPath := os.ExpandEnv(c.DiscoveryKubeconfigPath)

	return clientcmd.BuildConfigFromFlags("", expandedPath)
}

func (c *DiscoveryConfig) ProjectRestConfig() (*rest.Config, error) {
	if c.ProjectKubeconfigPath == "" {
		return ctrl.GetConfig()
	}

	// Expand environment variables in the kubeconfig path
	expandedPath := os.ExpandEnv(c.ProjectKubeconfigPath)

	return clientcmd.BuildConfigFromFlags("", expandedPath)
}

func init() {
	SchemeBuilder.Register(&NetworkServicesOperator{})
}
