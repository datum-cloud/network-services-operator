// SPDX-License-Identifier: AGPL-3.0-only

package display

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

func TestComputeHTTPProxyActivityDiff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		old  *networkingv1alpha.HTTPProxy
		new  *networkingv1alpha.HTTPProxy
		want ActivityDiff
	}{
		{
			name: "hostname added",
			old:  proxyWith("https://origin.example.com", "/"),
			new:  proxyWithHostnames([]string{"app.example.com", "api.example.com"}, "https://origin.example.com", "/"),
			want: ActivityDiff{
				Change: ActivityChangeAdded,
				Field:  ActivityFieldHostname,
				Name:   "api.example.com",
				Value:  "https://origin.example.com",
			},
		},
		{
			name: "hostname removed",
			old:  proxyWithHostnames([]string{"app.example.com", "api.example.com"}, "https://origin.example.com", "/"),
			new:  proxyWith("https://origin.example.com", "/"),
			want: ActivityDiff{
				Change: ActivityChangeRemoved,
				Field:  ActivityFieldHostname,
				Name:   "api.example.com",
				Value:  "https://origin.example.com",
			},
		},
		{
			name: "backend changed",
			old:  proxyWith("https://origin.example.com", "/"),
			new:  proxyWith("https://new-origin.example.com", "/"),
			want: ActivityDiff{
				Change: ActivityChangeUpdated,
				Field:  ActivityFieldBackend,
				Name:   "alb",
				Value:  "https://new-origin.example.com",
			},
		},
		{
			name: "rule path changed",
			old:  proxyWith("https://origin.example.com", "/"),
			new:  proxyWith("https://origin.example.com", "/api"),
			want: ActivityDiff{
				Change: ActivityChangeUpdated,
				Field:  ActivityFieldRule,
				Name:   "alb",
				Value:  "https://origin.example.com",
			},
		},
		{
			name: "mixed hostname and backend",
			old:  proxyWith("https://origin.example.com", "/"),
			new:  proxyWithHostnames([]string{"app.example.com", "api.example.com"}, "https://other.example.com", "/"),
			want: ActivityDiff{
				Change: ActivityChangeUpdated,
				Name:   "api.example.com",
				Value:  "https://other.example.com",
			},
		},
		{
			name: "unchanged",
			old:  proxyWith("https://origin.example.com", "/"),
			new:  proxyWith("https://origin.example.com", "/"),
			want: ActivityDiff{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ComputeHTTPProxyActivityDiff(tt.old, tt.new))
		})
	}
}

func TestEnsureHTTPProxyAnnotations(t *testing.T) {
	t.Parallel()

	proxy := proxyWith("https://origin.example.com", "/")
	require.True(t, EnsureHTTPProxyAnnotations(proxy, nil))
	assert.Equal(t, "alb", proxy.Annotations[AnnotationDisplayName])
	assert.Equal(t, "https://origin.example.com", proxy.Annotations[AnnotationDisplayValue])
	assert.NotContains(t, proxy.Annotations, AnnotationActivityChange)

	old := proxyWith("https://origin.example.com", "/")
	updated := proxyWithHostnames([]string{"app.example.com", "api.example.com"}, "https://origin.example.com", "/")
	require.True(t, EnsureHTTPProxyAnnotations(updated, old))
	assert.Equal(t, "alb", updated.Annotations[AnnotationDisplayName])
	assert.Equal(t, ActivityChangeAdded, updated.Annotations[AnnotationActivityChange])
	assert.Equal(t, ActivityFieldHostname, updated.Annotations[AnnotationActivityField])
	assert.Equal(t, "api.example.com", updated.Annotations[AnnotationActivityName])
}

func TestHTTPProxyDisplayName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "alb", HTTPProxyDisplayName(proxyWith("https://origin.example.com", "/")))

	named := proxyWith("https://origin.example.com", "/")
	named.Annotations = map[string]string{AnnotationChosenName: "Test ALB"}
	assert.Equal(t, "Test ALB", HTTPProxyDisplayName(named))
	require.True(t, EnsureHTTPProxyAnnotations(named, nil))
	assert.Equal(t, "Test ALB", named.Annotations[AnnotationDisplayName])

	blank := proxyWith("https://origin.example.com", "/")
	blank.Annotations = map[string]string{AnnotationChosenName: "  "}
	assert.Equal(t, "alb", HTTPProxyDisplayName(blank))
}

func proxyWith(endpoint, path string) *networkingv1alpha.HTTPProxy {
	return proxyWithHostnames([]string{"app.example.com"}, endpoint, path)
}

func proxyWithHostnames(hostnames []string, endpoint, path string) *networkingv1alpha.HTTPProxy {
	hs := make([]gatewayv1.Hostname, 0, len(hostnames))
	for _, h := range hostnames {
		hs = append(hs, gatewayv1.Hostname(h))
	}
	return &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: "alb", Namespace: "proj"},
		Spec: networkingv1alpha.HTTPProxySpec{
			Hostnames: hs,
			Rules: []networkingv1alpha.HTTPProxyRule{
				{
					Matches: []gatewayv1.HTTPRouteMatch{{
						Path: &gatewayv1.HTTPPathMatch{
							Type:  ptr.To(gatewayv1.PathMatchPathPrefix),
							Value: ptr.To(path),
						},
					}},
					Backends: []networkingv1alpha.HTTPProxyRuleBackend{{
						Endpoint: endpoint,
					}},
				},
			},
		},
	}
}
