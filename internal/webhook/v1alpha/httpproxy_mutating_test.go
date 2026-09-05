// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/display"
)

func TestHTTPProxyCustomDefaulter_Default(t *testing.T) {
	t.Parallel()

	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: "alb", Namespace: "proj"},
		Spec: networkingv1alpha.HTTPProxySpec{
			Hostnames: []gatewayv1.Hostname{"app.example.com"},
			Rules: []networkingv1alpha.HTTPProxyRule{{
				Matches: []gatewayv1.HTTPRouteMatch{{
					Path: &gatewayv1.HTTPPathMatch{
						Type:  ptr.To(gatewayv1.PathMatchPathPrefix),
						Value: ptr.To("/"),
					},
				}},
				Backends: []networkingv1alpha.HTTPProxyRuleBackend{{
					Endpoint: "https://origin.example.com",
				}},
			}},
		},
	}

	require.NoError(t, (&HTTPProxyCustomDefaulter{}).Default(context.Background(), proxy))
	assert.Equal(t, "app.example.com", proxy.Annotations[display.AnnotationDisplayName])
	assert.Equal(t, "https://origin.example.com", proxy.Annotations[display.AnnotationDisplayValue])
}
