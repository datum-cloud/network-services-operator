// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/display"
)

func TestTrafficProtectionPolicyDefaulter_DefaultUsesHTTPProxyName(t *testing.T) {
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
	assert.Equal(t, "alb", displayName)

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

func TestTrafficProtectionPolicyValidator_SectionNames(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))
	require.NoError(t, gatewayv1.Install(scheme))

	ruleName := func(s string) *gatewayv1.SectionName { return ptr.To(gatewayv1.SectionName(s)) }
	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: "alb", Namespace: "proj"},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{
				{Name: ruleName("exempt")},
				{Name: ruleName("protected")},
				{},
			},
		},
	}

	policyWithRef := func(kind, name string, section *gatewayv1.SectionName) *networkingv1alpha.TrafficProtectionPolicy {
		p := tppPolicy()
		p.Spec.TargetRefs = []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{{
			LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
				Group: gatewayv1.GroupName,
				Kind:  gatewayv1.Kind(kind),
				Name:  gatewayv1.ObjectName(name),
			},
			SectionName: section,
		}}
		return p
	}

	tests := []struct {
		name         string
		objs         []client.Object
		policy       *networkingv1alpha.TrafficProtectionPolicy
		wantErr      string
		wantWarnings int
	}{
		{name: "existing rule accepted", objs: []client.Object{proxy}, policy: policyWithRef("HTTPRoute", "alb", ruleName("protected"))},
		{name: "missing rule rejected", objs: []client.Object{proxy}, policy: policyWithRef("HTTPRoute", "alb", ruleName("nope")), wantErr: "spec.targetRefs[0].sectionName"},
		{name: "HTTPProxy absent only warns", policy: policyWithRef("HTTPRoute", "alb", ruleName("protected")), wantWarnings: 1},
		{name: "route-level ref not validated", objs: []client.Object{proxy}, policy: policyWithRef("HTTPRoute", "alb", nil)},
		{name: "gateway listener sectionName not validated", objs: []client.Object{proxy}, policy: policyWithRef("Gateway", "alb", ruleName("https"))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.objs...).Build()
			v := &TrafficProtectionPolicyValidator{}

			warnings, err := v.validateSectionNamesWithClient(context.Background(), cl, tt.policy)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Len(t, warnings, tt.wantWarnings)
		})
	}
}

func TestTrafficProtectionPolicyValidator_UpdateSkipsUnchangedTargetRefs(t *testing.T) {
	t.Parallel()

	old := tppPolicy()
	old.Spec.TargetRefs[0].Kind = "HTTPRoute"
	old.Spec.TargetRefs[0].SectionName = ptr.To(gatewayv1.SectionName("stale"))
	updated := old.DeepCopy()
	updated.Spec.Mode = networkingv1alpha.TrafficProtectionPolicyEnforce

	warnings, err := (&TrafficProtectionPolicyValidator{}).ValidateUpdate(context.Background(), old, updated)
	require.NoError(t, err)
	assert.Empty(t, warnings)
}
