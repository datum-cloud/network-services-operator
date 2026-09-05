// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/display"
)

func TestTrafficProtectionPolicyDefaulter_DefaultUsesHTTPProxyHostname(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))
	require.NoError(t, gatewayv1.Install(scheme))

	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: "alb", Namespace: "proj"},
		Spec: networkingv1alpha.HTTPProxySpec{
			Hostnames: []gatewayv1.Hostname{"app.example.com"},
		},
	}
	gateway := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "alb",
			Namespace: "proj",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: networkingv1alpha.GroupVersion.String(),
				Kind:       "HTTPProxy",
				Name:       "alb",
				UID:        "proxy-uid",
				Controller: ptrTrue(),
			}},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(proxy, gateway).Build()
	policy := tppPolicy()

	displayName := lookupTPPDisplayName(context.Background(), cl, policy)
	assert.Equal(t, "app.example.com", displayName)

	require.NoError(t, (&TrafficProtectionPolicyDefaulter{}).Default(context.Background(), policy))
	assert.Equal(t, "alb", policy.Annotations[display.AnnotationDisplayName])
	assert.Equal(t, "Observe", policy.Annotations[display.AnnotationDisplayValue])
}

func tppPolicy() *networkingv1alpha.TrafficProtectionPolicy {
	return &networkingv1alpha.TrafficProtectionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "waf", Namespace: "proj"},
		Spec: networkingv1alpha.TrafficProtectionPolicySpec{
			TargetRefs: []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{{
				LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
					Group: gatewayv1.GroupName,
					Kind:  gatewayv1.Kind("Gateway"),
					Name:  gatewayv1.ObjectName("alb"),
				},
			}},
			Mode: networkingv1alpha.TrafficProtectionPolicyObserve,
			RuleSets: []networkingv1alpha.TrafficProtectionPolicyRuleSet{{
				Type: networkingv1alpha.TrafficProtectionPolicyOWASPCoreRuleSet,
			}},
		},
	}
}

func ptrTrue() *bool {
	t := true
	return &t
}
