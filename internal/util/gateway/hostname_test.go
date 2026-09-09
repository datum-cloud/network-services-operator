package gateway

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestIsPreciseHostname(t *testing.T) {
	tests := map[string]struct {
		hostname string
		want     bool
	}{
		"lower case fqdn":            {hostname: "example.com", want: true},
		"mixed case fqdn":            {hostname: "Coffee.Example.Com", want: true},
		"single label":               {hostname: "localhost", want: true},
		"digits and hyphens":         {hostname: "web-01.example.com", want: true},
		"empty":                      {hostname: "", want: false},
		"wildcard":                   {hostname: "*.example.com", want: false},
		"with port":                  {hostname: "example.com:8080", want: false},
		"with scheme":                {hostname: "http://example.com", want: false},
		"with path":                  {hostname: "example.com/api", want: false},
		"space":                      {hostname: "not a hostname", want: false},
		"underscore":                 {hostname: "my_backend.example.com", want: false},
		"trailing dot":               {hostname: "example.com.", want: false},
		"leading hyphen":             {hostname: "-example.com", want: false},
		"too long":                   {hostname: strings.Repeat("a", 254), want: false},
		"at the length limit":        {hostname: strings.Repeat("a", 253), want: true},
		"mixed case at length limit": {hostname: strings.Repeat("A", 253), want: true},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, test.want, IsPreciseHostname(test.hostname))
		})
	}
}

func TestNormalizeHostname(t *testing.T) {
	assert.Equal(t, "coffee.example.com", NormalizeHostname("Coffee.Example.COM"))
	assert.Equal(t, "example.com", NormalizeHostname("example.com"))
}

func TestFindHostHeaderOverride(t *testing.T) {
	headerFilter := func(headers ...gatewayv1.HTTPHeader) gatewayv1.HTTPRouteFilter {
		return gatewayv1.HTTPRouteFilter{
			Type:                  gatewayv1.HTTPRouteFilterRequestHeaderModifier,
			RequestHeaderModifier: &gatewayv1.HTTPHeaderFilter{Set: headers},
		}
	}

	t.Run("reports the position of the header the user wrote", func(t *testing.T) {
		filters := []gatewayv1.HTTPRouteFilter{
			{Type: gatewayv1.HTTPRouteFilterURLRewrite},
			headerFilter(
				gatewayv1.HTTPHeader{Name: "X-Trace", Value: "on"},
				gatewayv1.HTTPHeader{Name: "host", Value: "Coffee.example.com"},
			),
		}

		override, found := FindHostHeaderOverride(filters)
		assert.True(t, found)
		assert.Equal(t, "Coffee.example.com", override.Value)
		assert.Equal(t, 1, override.FilterIndex)
		assert.Equal(t, 1, override.SetIndex)
	})

	t.Run("no host header", func(t *testing.T) {
		filters := []gatewayv1.HTTPRouteFilter{
			headerFilter(gatewayv1.HTTPHeader{Name: "X-Trace", Value: "on"}),
		}

		_, found := FindHostHeaderOverride(filters)
		assert.False(t, found)
	})

	t.Run("nil modifier is skipped", func(t *testing.T) {
		filters := []gatewayv1.HTTPRouteFilter{
			{Type: gatewayv1.HTTPRouteFilterRequestHeaderModifier},
		}

		_, found := FindHostHeaderOverride(filters)
		assert.False(t, found)
	})
}
