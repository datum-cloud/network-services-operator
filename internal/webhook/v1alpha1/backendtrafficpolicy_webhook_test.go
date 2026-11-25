// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	"context"
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
)

type fakeWebhookManager struct {
	mcmanager.Manager
	cl client.Client
}

func (m *fakeWebhookManager) GetCluster(context.Context, multicluster.ClusterName) (cluster.Cluster, error) {
	return &fakeWebhookCluster{cl: m.cl}, nil
}

type fakeWebhookCluster struct {
	cluster.Cluster
	cl client.Client
}

func (c *fakeWebhookCluster) GetClient() client.Client { return c.cl }

func generatedRouteFixtures(t *testing.T) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, gatewayv1.Install(scheme))
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))

	proxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: "lb", Namespace: "default", UID: "proxy-uid"},
	}
	generated := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lb",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: networkingv1alpha.GroupVersion.String(),
				Kind:       "HTTPProxy",
				Name:       "lb",
				UID:        "proxy-uid",
				Controller: ptr.To(true),
			}},
		},
	}
	plain := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "plain", Namespace: "default"},
	}
	pending := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: "pending", Namespace: "default", UID: "pending-uid"},
	}

	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(proxy, generated, plain, pending).Build()
}

func routeTargetRef(name, section string) gatewayv1.LocalPolicyTargetReferenceWithSectionName {
	ref := gatewayv1.LocalPolicyTargetReferenceWithSectionName{
		LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
			Group: gatewayv1.GroupName,
			Kind:  "HTTPRoute",
			Name:  gatewayv1.ObjectName(name),
		},
	}
	if section != "" {
		ref.SectionName = ptr.To(gatewayv1.SectionName(section))
	}
	return ref
}

func policyTargeting(mutate func(*envoygatewayv1alpha1.BackendTrafficPolicy)) *envoygatewayv1alpha1.BackendTrafficPolicy {
	policy := &envoygatewayv1alpha1.BackendTrafficPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "timeouts", Namespace: "default"},
	}
	mutate(policy)
	return policy
}

func TestBackendTrafficPolicyRefusesGeneratedRoutes(t *testing.T) {
	tests := []struct {
		name       string
		policy     *envoygatewayv1alpha1.BackendTrafficPolicy
		wantDenied bool
	}{
		{
			name: "targetRef to a route generated for a load balancer",
			policy: policyTargeting(func(p *envoygatewayv1alpha1.BackendTrafficPolicy) {
				p.Spec.TargetRefs = append(p.Spec.TargetRefs, routeTargetRef("lb", ""))
			}),
			wantDenied: true,
		},
		{
			name: "targetRef to a rule of a generated route",
			policy: policyTargeting(func(p *envoygatewayv1alpha1.BackendTrafficPolicy) {
				p.Spec.TargetRefs = append(p.Spec.TargetRefs, routeTargetRef("lb", "rule-0"))
			}),
			wantDenied: true,
		},
		{
			name: "targetRef to the route of a load balancer that has not created it yet",
			policy: policyTargeting(func(p *envoygatewayv1alpha1.BackendTrafficPolicy) {
				p.Spec.TargetRefs = append(p.Spec.TargetRefs, routeTargetRef("pending", ""))
			}),
			wantDenied: true,
		},
		{
			name: "targetSelector matching every route in the namespace",
			policy: policyTargeting(func(p *envoygatewayv1alpha1.BackendTrafficPolicy) {
				p.Spec.TargetSelectors = []envoygatewayv1alpha1.TargetSelector{{Kind: "HTTPRoute"}}
			}),
			wantDenied: true,
		},
		{
			name: "targetSelector matching the generated route by name",
			policy: policyTargeting(func(p *envoygatewayv1alpha1.BackendTrafficPolicy) {
				p.Spec.TargetSelectors = []envoygatewayv1alpha1.TargetSelector{{
					Kind:        "HTTPRoute",
					MatchLabels: map[string]string{downstreamclient.UpstreamOwnerNameLabel: "lb"},
				}}
			}),
			wantDenied: true,
		},
		{
			name: "targetSelector that also names the project",
			policy: policyTargeting(func(p *envoygatewayv1alpha1.BackendTrafficPolicy) {
				p.Spec.TargetSelectors = []envoygatewayv1alpha1.TargetSelector{{
					Kind: "HTTPRoute",
					MatchLabels: map[string]string{
						downstreamclient.UpstreamOwnerClusterNameLabel: downstreamclient.UpstreamClusterNameLabelValue("/test"),
						downstreamclient.UpstreamOwnerKindLabel:        "HTTPRoute",
					},
				}}
			}),
			wantDenied: true,
		},
		{
			name: "targetRef to the Gateway named after the load balancer",
			policy: policyTargeting(func(p *envoygatewayv1alpha1.BackendTrafficPolicy) {
				p.Spec.TargetRefs = []gatewayv1.LocalPolicyTargetReferenceWithSectionName{{
					LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
						Group: gatewayv1.GroupName,
						Kind:  "Gateway",
						Name:  "lb",
					},
				}}
			}),
		},
		{
			name: "targetRef to a route the customer wrote",
			policy: policyTargeting(func(p *envoygatewayv1alpha1.BackendTrafficPolicy) {
				p.Spec.TargetRefs = append(p.Spec.TargetRefs, routeTargetRef("plain", ""))
			}),
		},
		{
			name: "targetSelector matching only the route the customer wrote",
			policy: policyTargeting(func(p *envoygatewayv1alpha1.BackendTrafficPolicy) {
				p.Spec.TargetSelectors = []envoygatewayv1alpha1.TargetSelector{{
					Kind:        "HTTPRoute",
					MatchLabels: map[string]string{downstreamclient.UpstreamOwnerNameLabel: "plain"},
				}}
			}),
		},
		{
			name: "targetSelector for Gateways",
			policy: policyTargeting(func(p *envoygatewayv1alpha1.BackendTrafficPolicy) {
				p.Spec.TargetSelectors = []envoygatewayv1alpha1.TargetSelector{{Kind: "Gateway"}}
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &BackendTrafficPolicyCustomValidator{mgr: &fakeWebhookManager{cl: generatedRouteFixtures(t)}}

			_, createErr := v.ValidateCreate(clusterContext(), tt.policy)

			old := policyTargeting(func(*envoygatewayv1alpha1.BackendTrafficPolicy) {})
			_, updateErr := v.ValidateUpdate(clusterContext(), old, tt.policy)

			if !tt.wantDenied {
				assert.NoError(t, createErr)
				assert.NoError(t, updateErr)
				return
			}

			for _, err := range []error{createErr, updateErr} {
				require.Error(t, err)
				assert.True(t, apierrors.IsInvalid(err), "want an Invalid error, got %v", err)
				assert.Contains(t, err.Error(), `target the Gateway "`)
				t.Log(err.Error())
			}
		})
	}
}
