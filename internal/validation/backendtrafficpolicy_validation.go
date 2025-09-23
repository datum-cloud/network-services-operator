package validation

import (
	"fmt"
	"time"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// TODO(jreese) move limits to config
func ValidateBackendTrafficPolicy(backendTrafficPolicy *envoygatewayv1alpha1.BackendTrafficPolicy) field.ErrorList {
	allErrs := field.ErrorList{}

	specPath := field.NewPath("spec")

	// nolint:staticcheck
	if backendTrafficPolicy.Spec.TargetRef != nil {
		allErrs = append(allErrs, field.Forbidden(specPath.Child("targetRef"), "deprecated field, use spec.targetRefs or spec.targetSelectors instead"))
	}

	allErrs = append(allErrs, validateBackendTrafficPolicyLoadBalancer(backendTrafficPolicy.Spec.LoadBalancer, specPath.Child("loadBalancer"))...)

	if backendTrafficPolicy.Spec.Retry != nil {
		allErrs = append(allErrs, field.Forbidden(specPath.Child("retry"), "retry settings are not permitted"))
	}

	allErrs = append(allErrs, validateBackendTrafficPolicyTCPKeepalive(backendTrafficPolicy.Spec.TCPKeepalive, specPath.Child("tcpKeepalive"))...)
	allErrs = append(allErrs, validateBackendTrafficPolicyHealthCheck(backendTrafficPolicy.Spec.HealthCheck, specPath.Child("healthCheck"))...)
	allErrs = append(allErrs, validateBackendTrafficPolicyTimeout(backendTrafficPolicy.Spec.Timeout, specPath.Child("timeout"))...)
	allErrs = append(allErrs, validateBackendTrafficPolicyConnection(backendTrafficPolicy.Spec.Connection, specPath.Child("connection"))...)
	allErrs = append(allErrs, validateBackendTrafficPolicyDNS(backendTrafficPolicy.Spec.DNS, specPath.Child("dns"))...)
	allErrs = append(allErrs, validateBackendTrafficPolicyHTTP2(backendTrafficPolicy.Spec.HTTP2, specPath.Child("http2"))...)
	allErrs = append(allErrs, validateBackendTrafficPolicyRateLimit(backendTrafficPolicy.Spec.RateLimit, specPath.Child("rateLimit"))...)
	allErrs = append(allErrs, validateBackendTrafficPolicyFaultInjection(backendTrafficPolicy.Spec.FaultInjection, specPath.Child("faultInjection"))...)

	return allErrs
}

func validateBackendTrafficPolicyLoadBalancer(loadBalancer *envoygatewayv1alpha1.LoadBalancer, fldPath *field.Path) field.ErrorList {
	if loadBalancer == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if loadBalancer.EndpointOverride != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("endpointOverride"), "loadbalancer endpoint overrides are not permitted"))
	}

	return allErrs
}

func validateBackendTrafficPolicyTCPKeepalive(tcpKeepalive *envoygatewayv1alpha1.TCPKeepalive, fldPath *field.Path) field.ErrorList {
	if tcpKeepalive == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if tcpKeepalive.Probes != nil && *tcpKeepalive.Probes < 9 {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("probe"), *tcpKeepalive.Probes, "must be greater than or equal to 9"))
	}

	if v := tcpKeepalive.IdleTime; v != nil {
		idleTimeFieldPath := fldPath.Child("idleTime")
		allErrs = append(allErrs, validateGatewayDuration(idleTimeFieldPath, v, ptr.To(5*time.Minute), nil)...)
	}

	if v := tcpKeepalive.Interval; v != nil {
		intervalFieldPath := fldPath.Child("interval")
		allErrs = append(allErrs, validateGatewayDuration(intervalFieldPath, v, ptr.To(30*time.Second), nil)...)
	}

	return allErrs
}

func validateBackendTrafficPolicyHealthCheck(healthCheck *envoygatewayv1alpha1.HealthCheck, fldPath *field.Path) field.ErrorList {
	if healthCheck == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if healthCheck.Active != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("active"), "active health checks are not permitted"))
	}

	return allErrs
}

func validateBackendTrafficPolicyTimeout(timeout *envoygatewayv1alpha1.Timeout, fldPath *field.Path) field.ErrorList {
	if timeout == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if timeout.TCP != nil {
		if v := timeout.TCP.ConnectTimeout; v != nil {
			connectTimeoutFieldPath := fldPath.Child("tcp").Child("connectTimeout")
			allErrs = append(allErrs, validateGatewayDuration(connectTimeoutFieldPath, v, nil, ptr.To(1*time.Hour))...)
		}
	}

	if timeout.HTTP != nil {
		httpTimeoutFieldPath := fldPath.Child("http")

		if v := timeout.HTTP.ConnectionIdleTimeout; v != nil {
			connectionIdleTimeoutFieldPath := httpTimeoutFieldPath.Child("connectionIdleTimeout")
			allErrs = append(allErrs, validateGatewayDuration(connectionIdleTimeoutFieldPath, v, nil, ptr.To(1*time.Hour))...)
		}

		if v := timeout.HTTP.MaxConnectionDuration; v != nil {
			maxConnectionDurationFieldPath := httpTimeoutFieldPath.Child("maxConnectionDuration")
			allErrs = append(allErrs, validateGatewayDuration(maxConnectionDurationFieldPath, v, nil, ptr.To(1*time.Hour))...)
		}

		if v := timeout.HTTP.RequestTimeout; v != nil {
			requestTimeoutFieldPath := httpTimeoutFieldPath.Child("requestTimeout")
			allErrs = append(allErrs, validateGatewayDuration(requestTimeoutFieldPath, v, nil, ptr.To(1*time.Hour))...)
		}

	}

	return allErrs
}

func validateBackendTrafficPolicyConnection(connection *envoygatewayv1alpha1.BackendConnection, fldPath *field.Path) field.ErrorList {
	if connection == nil {
		return nil
	}

	limit := resource.MustParse("512Ki")
	if connection.BufferLimit != nil && connection.BufferLimit.Cmp(limit) == 1 {
		return field.ErrorList{
			field.Invalid(fldPath.Child("bufferLimit"), connection.BufferLimit.String(), fmt.Sprintf("must be less than or equal to %s", limit.String())),
		}
	}

	return nil
}

func validateBackendTrafficPolicyDNS(dns *envoygatewayv1alpha1.DNS, fldPath *field.Path) field.ErrorList {
	if dns == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if v := dns.DNSRefreshRate; v != nil {
		dnsRefreshRateFieldPath := fldPath.Child("dnsRefreshRate")
		allErrs = append(allErrs, validateGatewayDuration(dnsRefreshRateFieldPath, v, ptr.To(30*time.Second), nil)...)
	}

	if dns.RespectDNSTTL != nil && !*dns.RespectDNSTTL {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("respectDnsTtl"), "must respect DNS TTL"))
	}

	return allErrs
}

func validateBackendTrafficPolicyHTTP2(http2 *envoygatewayv1alpha1.HTTP2Settings, fldPath *field.Path) field.ErrorList {
	if http2 == nil {
		return nil
	}

	allErrs := field.ErrorList{}
	if http2.InitialStreamWindowSize != nil {
		limit := resource.MustParse("64Ki")
		if http2.InitialStreamWindowSize.Cmp(limit) == 1 {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("initialStreamWindowSize"), http2.InitialStreamWindowSize.String(), fmt.Sprintf("must be less than or equal to %s", limit.String())))
		}
	}

	if http2.InitialConnectionWindowSize != nil {
		limit := resource.MustParse("1Mi")
		if http2.InitialConnectionWindowSize.Cmp(limit) == 1 {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("initialConnectionWindowSize"), http2.InitialConnectionWindowSize.String(), fmt.Sprintf("must be less than or equal to %s", limit.String())))
		}
	}

	if http2.MaxConcurrentStreams != nil {
		limit := uint32(1024)
		if *http2.MaxConcurrentStreams > limit {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("maxConcurrentStreams"), http2.MaxConcurrentStreams, fmt.Sprintf("must be less than or equal to %d", limit)))
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
