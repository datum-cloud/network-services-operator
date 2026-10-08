package gateway

import (
	"regexp"
	"strings"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	hostHeaderName = "Host"

	// MaxPreciseHostnameLength mirrors the Gateway API PreciseHostname bound.
	MaxPreciseHostnameLength = 253
)

// preciseHostnameRegexp mirrors the Gateway API PreciseHostname pattern, the
// constraint every hostname synthesized into a generated HTTPRoute must satisfy.
var preciseHostnameRegexp = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)

// NormalizeHostname lowercases a hostname. Hostnames compare case-insensitively
// in HTTP Host headers, TLS SNI, and certificate SAN matching, while
// PreciseHostname accepts lowercase only.
func NormalizeHostname(hostname string) string {
	return strings.ToLower(hostname)
}

// IsPreciseHostname reports whether the value satisfies Gateway API's
// PreciseHostname constraints once normalized.
func IsPreciseHostname(hostname string) bool {
	normalized := NormalizeHostname(hostname)
	if len(normalized) == 0 || len(normalized) > MaxPreciseHostnameLength {
		return false
	}
	return preciseHostnameRegexp.MatchString(normalized)
}

// HostHeaderOverride locates a Host header set by a RequestHeaderModifier
// filter. The indices identify the field the user wrote, so validation can
// report against it rather than against the generated route.
type HostHeaderOverride struct {
	Value       string
	FilterIndex int
	SetIndex    int
}

// FindHostHeaderOverride returns the Host header value set by a
// RequestHeaderModifier filter, if present. Header names match
// case-insensitively per RFC 7230.
//
// Envoy Gateway does not accept Host header manipulation via
// RequestHeaderModifier; it must go through URLRewrite.Hostname instead. The
// HTTPProxy controller uses this to translate the user-facing
// RequestHeaderModifier{Host} shape (which round-trips with datumctl and the
// cloud portal) into the URLRewrite{Hostname} that Envoy honours at egress.
func FindHostHeaderOverride(filters []gatewayv1.HTTPRouteFilter) (HostHeaderOverride, bool) {
	for filterIndex, filter := range filters {
		if filter.Type != gatewayv1.HTTPRouteFilterRequestHeaderModifier || filter.RequestHeaderModifier == nil {
			continue
		}
		for setIndex, h := range filter.RequestHeaderModifier.Set {
			if strings.EqualFold(string(h.Name), hostHeaderName) {
				return HostHeaderOverride{
					Value:       h.Value,
					FilterIndex: filterIndex,
					SetIndex:    setIndex,
				}, true
			}
		}
	}
	return HostHeaderOverride{}, false
}
