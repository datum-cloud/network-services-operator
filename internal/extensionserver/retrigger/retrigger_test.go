// SPDX-License-Identifier: AGPL-3.0-only

package retrigger

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
)

const (
	testNS        = "ns-uid"
	testConnector = "connector-1"
	testProxy     = "proxy-1" // Gateway name == HTTPProxy name
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, networkingv1alpha.AddToScheme(s))
	require.NoError(t, networkingv1alpha1.AddToScheme(s))
	require.NoError(t, gatewayv1.Install(s))
	return s
}

// connectorWithUpstreamStatus builds an edge Connector whose liveness is carried
// in the UpstreamStatusAnnotation (the real two-cluster shape), with the given
// Ready value and, when online, a PublicKey node id.
func connectorWithUpstreamStatus(t *testing.T, ready bool, nodeID string) *networkingv1alpha1.Connector {
	t.Helper()
	readyStatus := metav1.ConditionFalse
	if ready {
		readyStatus = metav1.ConditionTrue
	}
	status := networkingv1alpha1.ConnectorStatus{
		Conditions: []metav1.Condition{{
			Type:   networkingv1alpha1.ConnectorConditionReady,
			Status: readyStatus,
			Reason: "Test",
		}},
	}
	if ready && nodeID != "" {
		status.ConnectionDetails = &networkingv1alpha1.ConnectorConnectionDetails{
			Type:      networkingv1alpha1.PublicKeyConnectorConnectionType,
			PublicKey: &networkingv1alpha1.ConnectorConnectionDetailsPublicKey{Id: nodeID},
		}
	}
	raw, err := json.Marshal(status)
	require.NoError(t, err)

	return &networkingv1alpha1.Connector{
		ObjectMeta: metav1.ObjectMeta{
			Name:        testConnector,
			Namespace:   testNS,
			Annotations: map[string]string{networkingv1alpha1.UpstreamStatusAnnotation: string(raw)},
		},
	}
}

func proxyRefersConnector() *networkingv1alpha.HTTPProxy {
	return &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: testProxy, Namespace: testNS},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{{
				Backends: []networkingv1alpha.HTTPProxyRuleBackend{{
					Connector: &networkingv1alpha.ConnectorReference{Name: testConnector},
				}},
			}},
		},
	}
}

func gateway() *gatewayv1.Gateway {
	return &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: testProxy, Namespace: testNS},
	}
}

func reconcileConnector(t *testing.T, cl client.Client) {
	t.Helper()
	r := &Reconciler{Client: cl}
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Namespace: testNS, Name: testConnector},
	})
	require.NoError(t, err)
}

func gatewayLiveness(t *testing.T, cl client.Client) string {
	t.Helper()
	var gw gatewayv1.Gateway
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: testProxy}, &gw))
	return gw.Annotations[ConnectorReadyAnnotationKey]
}

// TestReconcile_OnlineConnector_StampsGatewayWithNodeID: when an online
// connector is reconciled, the owning Gateway gets a trigger annotation encoding
// the live (online, nodeID) so EG re-translates.
func TestReconcile_OnlineConnector_StampsGatewayWithNodeID(t *testing.T) {
	scheme := testScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(connectorWithUpstreamStatus(t, true, "node-abc"), proxyRefersConnector(), gateway()).
		Build()

	reconcileConnector(t, cl)

	assert.Equal(t, "true/node-abc", gatewayLiveness(t, cl),
		"online connector must stamp the Gateway trigger annotation with online/nodeID")
}

// TestReconcile_OfflineConnector_StampsOffline verifies the offline value so a
// True→False flip changes the annotation and re-translates to the 503 program.
func TestReconcile_OfflineConnector_StampsOffline(t *testing.T) {
	scheme := testScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(connectorWithUpstreamStatus(t, false, ""), proxyRefersConnector(), gateway()).
		Build()

	reconcileConnector(t, cl)

	assert.Equal(t, "false/", gatewayLiveness(t, cl),
		"offline connector must stamp an offline trigger value")
}

// TestReconcile_NoGateway_NoError verifies a missing Gateway (HTTPProxy exists
// but its Gateway is not yet created) is ignored — EG translates a Gateway on
// creation, so there is nothing to nudge yet.
func TestReconcile_NoGateway_NoError(t *testing.T) {
	scheme := testScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(connectorWithUpstreamStatus(t, true, "node-abc"), proxyRefersConnector()).
		Build()

	reconcileConnector(t, cl) // require.NoError inside
}

// TestReconcile_UnrelatedProxy_NotTouched verifies a Gateway whose HTTPProxy
// does not reference the connector is left untouched.
func TestReconcile_UnrelatedProxy_NotTouched(t *testing.T) {
	scheme := testScheme(t)
	otherProxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: testNS},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{{
				Backends: []networkingv1alpha.HTTPProxyRuleBackend{{Endpoint: "http://x:80"}},
			}},
		},
	}
	otherGW := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: testNS}}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(connectorWithUpstreamStatus(t, true, "node-abc"), otherProxy, otherGW).
		Build()

	reconcileConnector(t, cl)

	var gw gatewayv1.Gateway
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "other"}, &gw))
	assert.NotContains(t, gw.Annotations, ConnectorReadyAnnotationKey,
		"a Gateway whose HTTPProxy does not reference the connector must not be touched")
}

// TestLivenessChangedPredicate verifies the controller only reconciles on
// changes that affect the (online, nodeID) the extension server keys on — not
// on unrelated status churn (e.g. heartbeat lastTransitionTime updates).
func TestLivenessChangedPredicate(t *testing.T) {
	p := livenessChangedPredicate()

	offline := connectorWithUpstreamStatus(t, false, "")
	onlineA := connectorWithUpstreamStatus(t, true, "node-a")
	onlineB := connectorWithUpstreamStatus(t, true, "node-b")

	assert.True(t, p.Create(event.CreateEvent{Object: onlineA}),
		"create is always admitted so already-online connectors stamp their Gateway on startup")
	assert.False(t, p.Delete(event.DeleteEvent{Object: onlineA}),
		"delete is ignored; EG re-translates on Gateway/route teardown")

	assert.True(t, p.Update(event.UpdateEvent{ObjectOld: offline, ObjectNew: onlineA}),
		"Ready False→True must reconcile")
	assert.True(t, p.Update(event.UpdateEvent{ObjectOld: onlineA, ObjectNew: onlineB}),
		"nodeID change must reconcile")
	assert.False(t, p.Update(event.UpdateEvent{ObjectOld: onlineA, ObjectNew: onlineA.DeepCopy()}),
		"no liveness change must NOT reconcile")
}
