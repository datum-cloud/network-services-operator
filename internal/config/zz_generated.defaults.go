//go:build !ignore_autogenerated
// +build !ignore_autogenerated

// Code generated by defaulter-gen. DO NOT EDIT.

package config

import (
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// RegisterDefaults adds defaulters functions to the given scheme.
// Public to allow building arbitrary schemes.
// All generated defaulters are covering - they call all nested defaulters.
func RegisterDefaults(scheme *runtime.Scheme) error {
	scheme.AddTypeDefaultingFunc(&NetworkServicesOperator{}, func(obj interface{}) { SetObjectDefaults_NetworkServicesOperator(obj.(*NetworkServicesOperator)) })
	return nil
}

func SetObjectDefaults_NetworkServicesOperator(in *NetworkServicesOperator) {
	SetDefaults_MetricsServerConfig(&in.MetricsServer)
	SetDefaults_TLSConfig(&in.MetricsServer.TLS)
	SetDefaults_TLSConfig(&in.WebhookServer.TLS)
	SetDefaults_GatewayConfig(&in.Gateway)
	SetDefaults_DiscoveryConfig(&in.Discovery)
}
