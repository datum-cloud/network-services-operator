package validation

import (
	"fmt"
	"strconv"
	"time"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"go.datum.net/network-services-operator/internal/config"
)

func ValidateBackendTrafficPolicy(backendTrafficPolicy *envoygatewayv1alpha1.BackendTrafficPolicy, opts config.BackendTrafficPolicyValidationOptions) field.ErrorList {
	allErrs := field.ErrorList{}

	specPath := field.NewPath("spec")

	// nolint:staticcheck
	if backendTrafficPolicy.Spec.TargetRef != nil {
		allErrs = append(allErrs, field.Forbidden(specPath.Child("targetRef"), "deprecated field, use spec.targetRefs or spec.targetSelectors instead"))
	}

	allErrs = append(allErrs, validateGatewayClusterSettings(backendTrafficPolicy.Spec.ClusterSettings, specPath, opts.ClusterSettings)...)
	allErrs = append(allErrs, validateBackendTrafficPolicyRateLimit(backendTrafficPolicy.Spec.RateLimit, specPath.Child("rateLimit"))...)
	allErrs = append(allErrs, validateBackendTrafficPolicyFaultInjection(backendTrafficPolicy.Spec.FaultInjection, specPath.Child("faultInjection"))...)

	return allErrs
}

func validateGatewayClusterSettings(clusterSettings envoygatewayv1alpha1.ClusterSettings, fldPath *field.Path, opts config.ClusterSettingsValidationOptions) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, validateGatewayBackendLoadBalancer(clusterSettings.LoadBalancer, fldPath.Child("loadBalancer"))...)

	if clusterSettings.Retry != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("retry"), "retry settings are not permitted"))
	}

	allErrs = append(allErrs, validateGatewayClusterSettingsTCPKeepalive(clusterSettings.TCPKeepalive, fldPath.Child("tcpKeepalive"), opts)...)
	allErrs = append(allErrs, validateGatewayClusterSettingsHealthCheck(clusterSettings.HealthCheck, fldPath.Child("healthCheck"))...)
	allErrs = append(allErrs, validateGatewayClusterSettingsTimeout(clusterSettings.Timeout, fldPath.Child("timeout"), opts)...)
	allErrs = append(allErrs, validateGatewayClusterSettingsConnection(clusterSettings.Connection, fldPath.Child("connection"), opts)...)
	allErrs = append(allErrs, validateGatewayClusterSettingsDNS(clusterSettings.DNS, fldPath.Child("dns"), opts)...)
	allErrs = append(allErrs, validateGatewayClusterSettingsHTTP2(clusterSettings.HTTP2, fldPath.Child("http2"), opts)...)

	return allErrs
}

func validateGatewayBackendLoadBalancer(loadBalancer *envoygatewayv1alpha1.LoadBalancer, fldPath *field.Path) field.ErrorList {
	if loadBalancer == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if loadBalancer.EndpointOverride != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("endpointOverride"), "loadbalancer endpoint overrides are not permitted"))
	}

	return allErrs
}

func validateGatewayClusterSettingsTCPKeepalive(tcpKeepalive *envoygatewayv1alpha1.TCPKeepalive, fldPath *field.Path, opts config.ClusterSettingsValidationOptions) field.ErrorList {
	if tcpKeepalive == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if tcpKeepalive.Probes != nil && *tcpKeepalive.Probes < opts.TCPKeepaliveMinProbes {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("probes"), strconv.FormatUint(uint64(*tcpKeepalive.Probes), 10), fmt.Sprintf("must be greater than or equal to %d", opts.TCPKeepaliveMinProbes)))
	}

	if v := tcpKeepalive.IdleTime; v != nil {
		idleTimeFieldPath := fldPath.Child("idleTime")
		allErrs = append(allErrs, validateGatewayDuration(idleTimeFieldPath, v, ptr.To(opts.TCPKeepaliveMinIdleTime), nil)...)
	}

	if v := tcpKeepalive.Interval; v != nil {
		intervalFieldPath := fldPath.Child("interval")
		allErrs = append(allErrs, validateGatewayDuration(intervalFieldPath, v, ptr.To(opts.TCPKeepaliveMinInterval), nil)...)
	}

	return allErrs
}

func validateGatewayClusterSettingsHealthCheck(healthCheck *envoygatewayv1alpha1.HealthCheck, fldPath *field.Path) field.ErrorList {
	if healthCheck == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if healthCheck.Active != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("active"), "active health checks are not permitted"))
	}

	return allErrs
}

func validateGatewayClusterSettingsTimeout(timeout *envoygatewayv1alpha1.Timeout, fldPath *field.Path, opts config.ClusterSettingsValidationOptions) field.ErrorList {
	if timeout == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if timeout.TCP != nil {
		if v := timeout.TCP.ConnectTimeout; v != nil {
			connectTimeoutFieldPath := fldPath.Child("tcp").Child("connectTimeout")
			allErrs = append(allErrs, validateGatewayDuration(connectTimeoutFieldPath, v, nil, ptr.To(opts.TCPMaxConnectionTimeout))...)
		}
	}

	if timeout.HTTP != nil {
		httpTimeoutFieldPath := fldPath.Child("http")

		if v := timeout.HTTP.ConnectionIdleTimeout; v != nil {
			connectionIdleTimeoutFieldPath := httpTimeoutFieldPath.Child("connectionIdleTimeout")
			allErrs = append(allErrs, validateGatewayDuration(connectionIdleTimeoutFieldPath, v, nil, ptr.To(opts.HTTPMaxConnectionIdleTimeout))...)
		}

		if v := timeout.HTTP.MaxConnectionDuration; v != nil {
			maxConnectionDurationFieldPath := httpTimeoutFieldPath.Child("maxConnectionDuration")
			allErrs = append(allErrs, validateGatewayDuration(maxConnectionDurationFieldPath, v, nil, ptr.To(opts.HTTPMaxConnectionDuration))...)
		}

		if v := timeout.HTTP.RequestTimeout; v != nil {
			requestTimeoutFieldPath := httpTimeoutFieldPath.Child("requestTimeout")
			allErrs = append(allErrs, validateGatewayDuration(requestTimeoutFieldPath, v, nil, ptr.To(opts.HTTPMaxRequestTimeout))...)
		}

	}

	return allErrs
}

func validateGatewayClusterSettingsConnection(connection *envoygatewayv1alpha1.BackendConnection, fldPath *field.Path, opts config.ClusterSettingsValidationOptions) field.ErrorList {
	if connection == nil {
		return nil
	}

	if connection.BufferLimit != nil && connection.BufferLimit.Cmp(opts.ConnectionMaxBufferLimit) == 1 {
		return field.ErrorList{
			field.Invalid(fldPath.Child("bufferLimit"), connection.BufferLimit.String(), fmt.Sprintf("must be less than or equal to %s", opts.ConnectionMaxBufferLimit.String())),
		}
	}

	return nil
}

func validateGatewayClusterSettingsDNS(dns *envoygatewayv1alpha1.DNS, fldPath *field.Path, opts config.ClusterSettingsValidationOptions) field.ErrorList {
	if dns == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if v := dns.DNSRefreshRate; v != nil {
		dnsRefreshRateFieldPath := fldPath.Child("dnsRefreshRate")
		allErrs = append(allErrs, validateGatewayDuration(dnsRefreshRateFieldPath, v, ptr.To(opts.DNSMinRefreshRate), nil)...)
	}

	if dns.RespectDNSTTL != nil && !*dns.RespectDNSTTL {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("respectDnsTtl"), "must respect DNS TTL"))
	}

	return allErrs
}

func validateGatewayClusterSettingsHTTP2(http2 *envoygatewayv1alpha1.HTTP2Settings, fldPath *field.Path, opts config.ClusterSettingsValidationOptions) field.ErrorList {
	if http2 == nil {
		return nil
	}

	allErrs := field.ErrorList{}
	if http2.InitialStreamWindowSize != nil {
		if http2.InitialStreamWindowSize.Cmp(opts.HTTP2MaxInitialStreamWindowSize) == 1 {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("initialStreamWindowSize"), http2.InitialStreamWindowSize.String(), fmt.Sprintf("must be less than or equal to %s", opts.HTTP2MaxInitialStreamWindowSize.String())))
		}
	}

	if http2.InitialConnectionWindowSize != nil {
		if http2.InitialConnectionWindowSize.Cmp(opts.HTTP2MaxInitialConnectionWindowSize) == 1 {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("initialConnectionWindowSize"), http2.InitialConnectionWindowSize.String(), fmt.Sprintf("must be less than or equal to %s", opts.HTTP2MaxInitialConnectionWindowSize.String())))
		}
	}

	if http2.MaxConcurrentStreams != nil {
		if *http2.MaxConcurrentStreams > opts.HTTP2MaxConcurrentStreams {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("maxConcurrentStreams"), http2.MaxConcurrentStreams, fmt.Sprintf("must be less than or equal to %d", opts.HTTP2MaxConcurrentStreams)))
		}
	}

	return allErrs
}

func validateBackendTrafficPolicyRateLimit(rateLimit *envoygatewayv1alpha1.RateLimitSpec, fldPath *field.Path) field.ErrorList {
	if rateLimit == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if rateLimit.Type != envoygatewayv1alpha1.LocalRateLimitType {
		allErrs = append(allErrs, field.NotSupported(fldPath.Child("type"), rateLimit.Type, []envoygatewayv1alpha1.RateLimitType{envoygatewayv1alpha1.LocalRateLimitType}))
	}

	if rateLimit.Global != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("global"), "global rate limits are not permitted"))
	}

	return allErrs
}

func validateBackendTrafficPolicyFaultInjection(faultInjection *envoygatewayv1alpha1.FaultInjection, fldPath *field.Path) field.ErrorList {
	if faultInjection != nil {
		return field.ErrorList{
			field.Forbidden(fldPath, "fault injection is not permitted"),
		}
	}

	return nil
}

func validateGatewayDuration(fldPath *field.Path, gatewayDuration *gatewayv1.Duration, lowerLimit *time.Duration, upperLimit *time.Duration) field.ErrorList {
	allErrs := field.ErrorList{}
	if d, err := time.ParseDuration(string(*gatewayDuration)); err != nil {
		allErrs = append(allErrs, field.Invalid(fldPath, *gatewayDuration, "invalid duration"))
	} else if lowerLimit != nil && d < *lowerLimit {
		allErrs = append(allErrs, field.Invalid(fldPath, *gatewayDuration, fmt.Sprintf("duration must be greater than or equal to %s", *lowerLimit)))
	} else if upperLimit != nil && d > *upperLimit {
		allErrs = append(allErrs, field.Invalid(fldPath, *gatewayDuration, fmt.Sprintf("duration must be less than or equal to %s", *upperLimit)))
	}

	return allErrs
}
