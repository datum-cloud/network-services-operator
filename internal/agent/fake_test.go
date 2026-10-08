// SPDX-License-Identifier: AGPL-3.0-only

package agent

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// fakeReader serves objects from memory so the walk can be tested without a
// cluster. Errors are set per method to exercise the partial-evidence paths.
type fakeReader struct {
	proxies     []networkingv1alpha.HTTPProxy
	domains     []networkingv1alpha.Domain
	policies    []networkingv1alpha.TrafficProtectionPolicy
	services    map[string]*networkingv1alpha.NetworkService
	secrets     map[string]*corev1.Secret
	domainsErr  error
	policiesErr error
}

func (f *fakeReader) ListProxies(context.Context, string) ([]networkingv1alpha.HTTPProxy, error) {
	return f.proxies, nil
}

func (f *fakeReader) GetProxy(_ context.Context, _, name string) (*networkingv1alpha.HTTPProxy, error) {
	for i := range f.proxies {
		if f.proxies[i].Name == name {
			return &f.proxies[i], nil
		}
	}
	return nil, fmt.Errorf("getting load balancer %s: not found", name)
}

func (f *fakeReader) ListDomains(context.Context, string) ([]networkingv1alpha.Domain, error) {
	return f.domains, f.domainsErr
}

func (f *fakeReader) GetNetworkService(_ context.Context, _, name string) (*networkingv1alpha.NetworkService, error) {
	if s, ok := f.services[name]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("getting network service %s: not found", name)
}

func (f *fakeReader) ListProtectionPolicies(context.Context, string) ([]networkingv1alpha.TrafficProtectionPolicy, error) {
	return f.policies, f.policiesErr
}

func (f *fakeReader) GetSecret(_ context.Context, _, name string) (*corev1.Secret, error) {
	if s, ok := f.secrets[name]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("getting secret %s: not found", name)
}

var _ Reader = (*fakeReader)(nil)

// testNow is the clock every test reads against.
var testNow = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) metav1.Time { return metav1.NewTime(testNow.Add(-d)) }

func cond(condType, reason string, status metav1.ConditionStatus, since metav1.Time) metav1.Condition {
	return metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		LastTransitionTime: since,
	}
}

// healthyProxy is a load balancer with nothing wrong: accepted, published, a
// generated hostname and no custom hostnames.
func healthyProxy() networkingv1alpha.HTTPProxy {
	return networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "my-app",
			Namespace:         "default",
			CreationTimestamp: ago(30 * time.Minute),
			Annotations:       map[string]string{"kubernetes.io/display-name": "My App"},
		},
		Status: networkingv1alpha.HTTPProxyStatus{
			CanonicalHostname: "abc123.datumproxy.net",
			Conditions: []metav1.Condition{
				cond(networkingv1alpha.HTTPProxyConditionAccepted, networkingv1alpha.HTTPProxyReasonAccepted, metav1.ConditionTrue, ago(29*time.Minute)),
				cond(networkingv1alpha.HTTPProxyConditionProgrammed, networkingv1alpha.HTTPProxyReasonProgrammed, metav1.ConditionTrue, ago(28*time.Minute)),
			},
		},
	}
}

func withHostname(p networkingv1alpha.HTTPProxy, hs networkingv1alpha.HostnameStatus) networkingv1alpha.HTTPProxy {
	p.Spec.Hostnames = append(p.Spec.Hostnames, gatewayv1.Hostname(hs.Hostname))
	p.Status.HostnameStatuses = append(p.Status.HostnameStatuses, hs)
	return p
}

func tppFor(proxyName, mode string) networkingv1alpha.TrafficProtectionPolicy {
	return networkingv1alpha.TrafficProtectionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: proxyName, Namespace: "default"},
		Spec: networkingv1alpha.TrafficProtectionPolicySpec{
			Mode: networkingv1alpha.TrafficProtectionPolicyMode(mode),
			TargetRefs: []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName{{
				LocalPolicyTargetReference: gatewayv1.LocalPolicyTargetReference{
					Group: "gateway.networking.k8s.io",
					Kind:  "Gateway",
					Name:  gatewayv1.ObjectName(proxyName),
				},
			}},
		},
	}
}
