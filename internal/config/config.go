package config

import (
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +k8s:defaulter-gen=true

type NetworkServicesOperator struct {
	metav1.TypeMeta

	Gateway GatewayConfig `json:"gateway"`
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
}

func (c *GatewayConfig) IPv4Enabled() bool {
	return slices.Contains(c.IPFamilies, networkingv1alpha.IPv4Protocol)
}

func (c *GatewayConfig) IPv6Enabled() bool {
	return slices.Contains(c.IPFamilies, networkingv1alpha.IPv6Protocol)
}

func init() {
	SchemeBuilder.Register(&NetworkServicesOperator{})
}
