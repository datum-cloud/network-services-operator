package config

import (
	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func SetDefaults_GatewayConfig(obj *GatewayConfig) {
	if obj.IPFamilies == nil {
		obj.IPFamilies = []networkingv1alpha.IPFamily{
			networkingv1alpha.IPv4Protocol,
			networkingv1alpha.IPv6Protocol,
		}
	}
}
