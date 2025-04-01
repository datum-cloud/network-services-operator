package config

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type NetworkServicesOperator struct {
	metav1.TypeMeta

	Gateway GatewayConfig `json:"gateway"`
}

type GatewayConfig struct {
	TargetDomain string `json:"targetDomain"`

	PermitCertificateRefs bool `json:"permitCertificateRefs"`
}

func init() {
	SchemeBuilder.Register(&NetworkServicesOperator{})
}
