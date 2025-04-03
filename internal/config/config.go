package config

import (
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/providers"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +k8s:defaulter-gen=true

type NetworkServicesOperator struct {
	metav1.TypeMeta

	Gateway GatewayConfig `json:"gateway"`

	Discovery DiscoveryConfig `json:"discovery"`

	DownstreamResourceManagement DownstreamResourceManagementConfig `json:"downstreamResourceManagement"`
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

type GatewayConfig struct {
	TargetDomain string `json:"targetDomain"`

	PermitCertificateRefs bool `json:"permitCertificateRefs"`

	// IPFamilies defines the IP families that should be enabled on gateways
	// created by the operator.
	//
	// Defaults to [IPv4, IPv6]
	IPFamilies []networkingv1alpha.IPFamily `json:"ipFamilies,omitempty"`

	// DownstreamGatewayClassName is the name of the GatewayClass that should be
	// used when programming gateways in the downstream cluster.
	DownstreamGatewayClassName string `json:"downstreamGatewayClassName"`
}

func SetDefaults_GatewayConfig(obj *GatewayConfig) {
	if obj.IPFamilies == nil {
		obj.IPFamilies = []networkingv1alpha.IPFamily{
			networkingv1alpha.IPv4Protocol,
			networkingv1alpha.IPv6Protocol,
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
