package validation

import (
	"testing"
	"time"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	"go.datum.net/network-services-operator/internal/config"
)

func TestValidateBackendTrafficPolicy(t *testing.T) {
	scenarios := map[string]struct {
		backendTrafficPolicy *envoygatewayv1alpha1.BackendTrafficPolicy
		opts                 config.BackendTrafficPolicyValidationOptions
		expectedErrors       field.ErrorList
	}{
		"spec.targetRef forbidden": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					PolicyTargetReferences: envoygatewayv1alpha1.PolicyTargetReferences{
						TargetRef: &gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{
							SectionName: ptr.To(gatewayv1.SectionName("test")),
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Forbidden(field.NewPath("spec", "targetRef"), "Invalid"),
			},
		},
		"invalid load balancer endpoint override": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					ClusterSettings: envoygatewayv1alpha1.ClusterSettings{
						LoadBalancer: &envoygatewayv1alpha1.LoadBalancer{
							EndpointOverride: &envoygatewayv1alpha1.EndpointOverride{
								ExtractFrom: []envoygatewayv1alpha1.EndpointOverrideExtractFrom{},
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Forbidden(field.NewPath("spec", "loadBalancer", "endpointOverride"), ""),
			},
		},
		"retry not permitted": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					ClusterSettings: envoygatewayv1alpha1.ClusterSettings{
						Retry: &envoygatewayv1alpha1.Retry{
							NumRetries: ptr.To(int32(3)),
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Forbidden(field.NewPath("spec", "retry"), ""),
			},
		},
		"tcp keepalive probes too low": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					ClusterSettings: envoygatewayv1alpha1.ClusterSettings{
						TCPKeepalive: &envoygatewayv1alpha1.TCPKeepalive{
							Probes: ptr.To(uint32(1)),
						},
					},
				},
			},
			opts: config.BackendTrafficPolicyValidationOptions{
				ClusterSettings: config.ClusterSettingsValidationOptions{
					TCPKeepaliveMinProbes: 2,
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "tcpKeepalive", "probes"), int32(0), ""),
			},
		},
		"tcp idleTime too low": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					ClusterSettings: envoygatewayv1alpha1.ClusterSettings{
						TCPKeepalive: &envoygatewayv1alpha1.TCPKeepalive{
							IdleTime: ptr.To(gatewayv1.Duration("5s")),
						},
					},
				},
			},
			opts: config.BackendTrafficPolicyValidationOptions{
				ClusterSettings: config.ClusterSettingsValidationOptions{
					TCPKeepaliveMinIdleTime: 1 * time.Minute,
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "tcpKeepalive", "idleTime"), "4m0s", ""),
			},
		},
		"tcp interval too low": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					ClusterSettings: envoygatewayv1alpha1.ClusterSettings{
						TCPKeepalive: &envoygatewayv1alpha1.TCPKeepalive{
							Interval: ptr.To(gatewayv1.Duration("5s")),
						},
					},
				},
			},
			opts: config.BackendTrafficPolicyValidationOptions{
				ClusterSettings: config.ClusterSettingsValidationOptions{
					TCPKeepaliveMinInterval: 1 * time.Minute,
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "tcpKeepalive", "interval"), "4m0s", ""),
			},
		},
		"active healthcheck not permitted": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					ClusterSettings: envoygatewayv1alpha1.ClusterSettings{
						HealthCheck: &envoygatewayv1alpha1.HealthCheck{
							Active: &envoygatewayv1alpha1.ActiveHealthCheck{},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Forbidden(field.NewPath("spec", "healthCheck", "active"), ""),
			},
		},
		"tcp connect timeout too high": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					ClusterSettings: envoygatewayv1alpha1.ClusterSettings{
						Timeout: &envoygatewayv1alpha1.Timeout{
							TCP: &envoygatewayv1alpha1.TCPTimeout{
								ConnectTimeout: ptr.To(gatewayv1.Duration("24h")),
							},
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "timeout", "tcp", "connectTimeout"), "", ""),
			},
		},
		"http connection idle timeout too high": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					ClusterSettings: envoygatewayv1alpha1.ClusterSettings{
						Timeout: &envoygatewayv1alpha1.Timeout{
							HTTP: &envoygatewayv1alpha1.HTTPTimeout{
								ConnectionIdleTimeout: ptr.To(gatewayv1.Duration("24h")),
							},
						},
					},
				},
			},
			opts: config.BackendTrafficPolicyValidationOptions{
				ClusterSettings: config.ClusterSettingsValidationOptions{
					HTTPMaxConnectionIdleTimeout: 1 * time.Hour,
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "timeout", "http", "connectionIdleTimeout"), "", ""),
			},
		},
		"http max connection duration too high": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					ClusterSettings: envoygatewayv1alpha1.ClusterSettings{
						Timeout: &envoygatewayv1alpha1.Timeout{
							HTTP: &envoygatewayv1alpha1.HTTPTimeout{
								MaxConnectionDuration: ptr.To(gatewayv1.Duration("24h")),
							},
						},
					},
				},
			},
			opts: config.BackendTrafficPolicyValidationOptions{
				ClusterSettings: config.ClusterSettingsValidationOptions{
					HTTPMaxConnectionDuration: 1 * time.Hour,
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "timeout", "http", "maxConnectionDuration"), "", ""),
			},
		},
		"http request timeout too high": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					ClusterSettings: envoygatewayv1alpha1.ClusterSettings{
						Timeout: &envoygatewayv1alpha1.Timeout{
							HTTP: &envoygatewayv1alpha1.HTTPTimeout{
								RequestTimeout: ptr.To(gatewayv1.Duration("24h")),
							},
						},
					},
				},
			},
			opts: config.BackendTrafficPolicyValidationOptions{
				ClusterSettings: config.ClusterSettingsValidationOptions{
					HTTPMaxRequestTimeout: 1 * time.Hour,
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "timeout", "http", "requestTimeout"), "", ""),
			},
		},
		"connection buffer limit too high": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					ClusterSettings: envoygatewayv1alpha1.ClusterSettings{
						Connection: &envoygatewayv1alpha1.BackendConnection{
							BufferLimit: ptr.To(resource.MustParse("2Gi")),
						},
					},
				},
			},
			opts: config.BackendTrafficPolicyValidationOptions{
				ClusterSettings: config.ClusterSettingsValidationOptions{
					ConnectionMaxBufferLimit: resource.MustParse("512Ki"),
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "connection", "bufferLimit"), "", ""),
			},
		},
		"dns refresh rate too low": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					ClusterSettings: envoygatewayv1alpha1.ClusterSettings{
						DNS: &envoygatewayv1alpha1.DNS{
							DNSRefreshRate: ptr.To(gatewayv1.Duration("500ms")),
						},
					},
				},
			},
			opts: config.BackendTrafficPolicyValidationOptions{
				ClusterSettings: config.ClusterSettingsValidationOptions{
					DNSMinRefreshRate: 1 * time.Second,
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "dns", "dnsRefreshRate"), "", ""),
			},
		},
		"dns respect ttl false": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					ClusterSettings: envoygatewayv1alpha1.ClusterSettings{
						DNS: &envoygatewayv1alpha1.DNS{
							RespectDNSTTL: ptr.To(false),
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Forbidden(field.NewPath("spec", "dns", "respectDnsTtl"), "must respect DNS TTL"),
			},
		},
		"http2 initial stream window size too large": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					ClusterSettings: envoygatewayv1alpha1.ClusterSettings{
						HTTP2: &envoygatewayv1alpha1.HTTP2Settings{
							InitialStreamWindowSize: ptr.To(resource.MustParse("2Gi")),
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "http2", "initialStreamWindowSize"), 0, ""),
			},
		},
		"http2 initial connection window size too large": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					ClusterSettings: envoygatewayv1alpha1.ClusterSettings{
						HTTP2: &envoygatewayv1alpha1.HTTP2Settings{
							InitialConnectionWindowSize: ptr.To(resource.MustParse("2Gi")),
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "http2", "initialConnectionWindowSize"), 0, ""),
			},
		},
		"http2 max concurrent streams too large": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					ClusterSettings: envoygatewayv1alpha1.ClusterSettings{
						HTTP2: &envoygatewayv1alpha1.HTTP2Settings{
							MaxConcurrentStreams: ptr.To(uint32(2000000)),
						},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.Invalid(field.NewPath("spec", "http2", "maxConcurrentStreams"), 0, ""),
			},
		},
		"rate limit type invalid": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					RateLimit: &envoygatewayv1alpha1.RateLimitSpec{
						Type:   envoygatewayv1alpha1.GlobalRateLimitType,
						Global: &envoygatewayv1alpha1.GlobalRateLimit{},
					},
				},
			},
			expectedErrors: field.ErrorList{
				field.NotSupported(field.NewPath("spec", "rateLimit", "type"), "", []string{}),
				field.Forbidden(field.NewPath("spec", "rateLimit", "global"), ""),
			},
		},
		"fault injection not permitted": {
			backendTrafficPolicy: &envoygatewayv1alpha1.BackendTrafficPolicy{
				Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
					FaultInjection: &envoygatewayv1alpha1.FaultInjection{},
				},
			},
			expectedErrors: field.ErrorList{
				field.Forbidden(field.NewPath("spec", "faultInjection"), "fault injection is not permitted"),
			},
		},
	}

	for name, scenario := range scenarios {
		t.Run(name, func(t *testing.T) {
			errs := ValidateBackendTrafficPolicy(scenario.backendTrafficPolicy, scenario.opts)
			delta := cmp.Diff(scenario.expectedErrors, errs, cmpopts.IgnoreFields(field.Error{}, "BadValue", "Detail"))
			if delta != "" {
				t.Errorf("Testcase %s - expected errors '%v', got '%v', diff: '%v'", name, scenario.expectedErrors, errs, delta)
			}
		})
	}
}
