// SPDX-License-Identifier: AGPL-3.0-only

package alb

import (
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/cmd/alb/spec"
	"go.datum.net/network-services-operator/internal/cmd/alb/util"
)

func twoRuleProxy() *networkingv1alpha.HTTPProxy {
	rule := func(name, path string) networkingv1alpha.HTTPProxyRule {
		return networkingv1alpha.HTTPProxyRule{
			Name: ptr.To(gatewayv1.SectionName(name)),
			Matches: []gatewayv1.HTTPRouteMatch{{Path: &gatewayv1.HTTPPathMatch{
				Type: ptr.To(gatewayv1.PathMatchPathPrefix), Value: ptr.To(path),
			}}},
			Backends: []networkingv1alpha.HTTPProxyRuleBackend{{Endpoint: "https://origin.example.com"}},
		}
	}
	return &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: "default"},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{rule("api", "/api"), rule("web", "/")},
		},
	}
}

func ruleScopedBasicAuth(t *testing.T, accepted metav1.ConditionStatus) (*envoygatewayv1alpha1.SecurityPolicy, *corev1.Secret) {
	t.Helper()
	content, err := spec.GenerateHtpasswd([]spec.BasicAuthUser{{Username: "alice", Password: "pw"}})
	require.NoError(t, err)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "api-users", Namespace: "default"},
		Data:       map[string][]byte{envoygatewayv1alpha1.BasicAuthUsersSecretKey: []byte(content)},
	}
	policy := &envoygatewayv1alpha1.SecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "api-auth", Namespace: "default"},
		Spec: envoygatewayv1alpha1.SecurityPolicySpec{
			PolicyTargetReferences: envoygatewayv1alpha1.PolicyTargetReferences{
				TargetRefs: []gatewayv1.LocalPolicyTargetReferenceWithSectionName{{
					LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
						Group: gatewayv1.GroupName, Kind: "HTTPRoute", Name: "my-app",
					},
					SectionName: ptr.To(gatewayv1.SectionName("api")),
				}},
			},
			BasicAuth: &envoygatewayv1alpha1.BasicAuth{
				Users: gatewayv1.SecretObjectReference{Name: "api-users"},
			},
		},
	}
	if accepted != "" {
		policy.Status.Ancestors = []gatewayv1.PolicyAncestorStatus{{
			AncestorRef:    gatewayv1.ParentReference{Name: "my-app"},
			ControllerName: "gateway.networking.datumapis.com/external-global-proxy-controller",
			Conditions: []metav1.Condition{{
				Type: string(gatewayv1.PolicyConditionAccepted), Status: accepted,
				Reason: string(gatewayv1.PolicyReasonAccepted), Message: "Policy has been accepted.",
			}},
		}}
	}
	return policy, secret
}

func newAuthTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme, err := util.NewScheme()
	require.NoError(t, err)
	return fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&networkingv1alpha.HTTPProxy{}, &envoygatewayv1alpha1.SecurityPolicy{}).
		WithObjects(objs...).Build()
}

func TestDescribeReportsBasicAuthOnOneRule(t *testing.T) {
	policy, secret := ruleScopedBasicAuth(t, metav1.ConditionTrue)
	c := newAuthTestClient(t, twoRuleProxy(), policy, secret)

	out, err := run(t, c, "describe", "my-app")
	require.NoError(t, err)

	assert.NotContains(t, out, "Basic auth:         off")
	assert.Contains(t, out, "Basic auth:         on")
	assert.Contains(t, out, "api-auth  rules: api  users: alice  accepted")
}

func TestDescribeReportsBasicAuthNotYetAccepted(t *testing.T) {
	policy, secret := ruleScopedBasicAuth(t, "")
	c := newAuthTestClient(t, twoRuleProxy(), policy, secret)

	out, err := run(t, c, "describe", "my-app")
	require.NoError(t, err)

	assert.Contains(t, out, "api-auth  rules: api  users: alice  pending")
}

func TestDescribeReportsWholeRouteBasicAuth(t *testing.T) {
	content, err := spec.GenerateHtpasswd([]spec.BasicAuthUser{{Username: "admin", Password: "pw"}})
	require.NoError(t, err)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: spec.BasicAuthSecretName("my-app"), Namespace: "default"},
		Data:       map[string][]byte{envoygatewayv1alpha1.BasicAuthUsersSecretKey: []byte(content)},
	}
	c := newAuthTestClient(t, twoRuleProxy(), spec.BuildSecurityPolicy("my-app", ""), secret)

	out, err := run(t, c, "describe", "my-app")
	require.NoError(t, err)

	assert.Contains(t, out, "my-app  rules: all  users: admin  pending")
}

func TestDescribeIgnoresBasicAuthOnAnotherLoadBalancer(t *testing.T) {
	policy, secret := ruleScopedBasicAuth(t, metav1.ConditionTrue)
	policy.Spec.TargetRefs[0].Name = "other-app"
	c := newAuthTestClient(t, twoRuleProxy(), policy, secret)

	out, err := run(t, c, "describe", "my-app")
	require.NoError(t, err)

	assert.Contains(t, out, "Basic auth:         off")
}

func TestAuthListNamesTheRulesEachUserGuards(t *testing.T) {
	policy, secret := ruleScopedBasicAuth(t, metav1.ConditionFalse)
	policy.Status.Ancestors[0].Conditions[0].Reason = string(gatewayv1.PolicyReasonInvalid)
	policy.Status.Ancestors[0].Conditions[0].Message = "secret not found"
	c := newAuthTestClient(t, twoRuleProxy(), policy, secret)

	out, err := run(t, c, "auth", "list", "my-app")
	require.NoError(t, err)

	assert.NotContains(t, out, "Basic authentication: off")
	assert.Contains(t, out, "USERNAME")
	assert.Regexp(t, `alice\s+api\s+api-auth\s+not accepted: secret not found`, out)
}
