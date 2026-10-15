// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// TestDomainChangeReachesOnlyCoveredGateways checks that a verified Domain's
// change reaches the Gateways in its namespace with a listener hostname the
// domain covers, and no others; an unverified Domain reaches none.
func TestDomainChangeReachesOnlyCoveredGateways(t *testing.T) {
	gateways := []client.Object{
		domainTestGateway("default", "under", "app.example.test"),
		domainTestGateway("default", "apex", "example.test"),
		domainTestGateway("default", "other", "app.other.test"),
		domainTestGateway("default", "suffix", "appexample.test"),
		domainTestGateway("elsewhere", "under", "app.example.test"),
	}
	cases := []struct {
		name     string
		verified bool
		want     []types.NamespacedName
	}{
		{"a verified Domain", true, []types.NamespacedName{{Namespace: "default", Name: "apex"}, {Namespace: "default", Name: "under"}}},
		{"a Domain not verified", false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(claimsTestScheme(t)).WithObjects(gateways...).Build()
			h := (&GatewayReconciler{}).listGatewaysForDomainFunc("", &fakeCluster{cl: c})
			queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[mcreconcile.Request]())
			t.Cleanup(queue.ShutDown)

			h.Create(context.Background(), event.TypedCreateEvent[client.Object]{Object: domainTestDomain(tc.verified)}, queue)

			var got []types.NamespacedName
			for queue.Len() > 0 {
				req, _ := queue.Get()
				queue.Done(req)
				got = append(got, req.NamespacedName)
			}
			require.ElementsMatch(t, tc.want, got)
		})
	}
}

func domainTestGateway(namespace, name, hostname string) *gatewayv1.Gateway {
	return &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: gatewayv1.GatewaySpec{Listeners: []gatewayv1.Listener{{
			Name: "http", Port: 80, Protocol: gatewayv1.HTTPProtocolType, Hostname: ptr.To(gatewayv1.Hostname(hostname)),
		}}},
	}
}

func domainTestDomain(verified bool) *networkingv1alpha.Domain {
	domain := &networkingv1alpha.Domain{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "example-test"},
		Spec:       networkingv1alpha.DomainSpec{DomainName: "example.test"},
	}
	if verified {
		apimeta.SetStatusCondition(&domain.Status.Conditions, metav1.Condition{
			Type: networkingv1alpha.DomainConditionVerified, Status: metav1.ConditionTrue, Reason: "Verified",
		})
	}
	return domain
}
