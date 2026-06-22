package controller

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
	"go.datum.net/network-services-operator/internal/config"
)

func TestConnectorReconcile(t *testing.T) {
	log.SetLogger(zap.New(zap.UseDevMode(true)))

	tests := []struct {
		name            string
		connector       *networkingv1alpha1.Connector
		connectorClass  *networkingv1alpha1.ConnectorClass
		lease           *coordinationv1.Lease
		wantStatus      metav1.ConditionStatus
		wantReason      string
		wantReady       metav1.ConditionStatus
		wantReadyReason string
	}{
		{
			name: "connector class resolved",
			connector: &networkingv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{Name: "connector", Namespace: "default"},
				Spec: networkingv1alpha1.ConnectorSpec{
					ConnectorClassName: "datum-connect",
				},
			},
			connectorClass: &networkingv1alpha1.ConnectorClass{
				ObjectMeta: metav1.ObjectMeta{Name: "datum-connect"},
			},
			wantStatus:      metav1.ConditionTrue,
			wantReason:      networkingv1alpha1.ConnectorReasonAccepted,
			wantReady:       metav1.ConditionFalse,
			wantReadyReason: networkingv1alpha1.ConnectorReasonNotReady,
		},
		{
			name: "connector class missing",
			connector: &networkingv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{Name: "connector", Namespace: "default"},
				Spec: networkingv1alpha1.ConnectorSpec{
					ConnectorClassName: "missing",
				},
			},
			wantStatus:      metav1.ConditionFalse,
			wantReason:      networkingv1alpha1.ConnectorReasonConnectorClassNotFound,
			wantReady:       metav1.ConditionFalse,
			wantReadyReason: networkingv1alpha1.ConnectorReasonNotReady,
		},
		{
			name: "connector lease ready",
			connector: &networkingv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{Name: "connector", Namespace: "default"},
				Spec: networkingv1alpha1.ConnectorSpec{
					ConnectorClassName: "datum-connect",
				},
			},
			connectorClass: &networkingv1alpha1.ConnectorClass{
				ObjectMeta: metav1.ObjectMeta{Name: "datum-connect"},
			},
			lease: &coordinationv1.Lease{
				ObjectMeta: metav1.ObjectMeta{Name: "connector", Namespace: "default"},
				Spec: coordinationv1.LeaseSpec{
					LeaseDurationSeconds: ptr.To[int32](30),
					RenewTime:            &metav1.MicroTime{Time: time.Now()},
				},
			},
			wantStatus:      metav1.ConditionTrue,
			wantReason:      networkingv1alpha1.ConnectorReasonAccepted,
			wantReady:       metav1.ConditionTrue,
			wantReadyReason: networkingv1alpha1.ConnectorReasonReady,
		},
		{
			name: "connector lease expired",
			connector: &networkingv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{Name: "connector", Namespace: "default"},
				Spec: networkingv1alpha1.ConnectorSpec{
					ConnectorClassName: "datum-connect",
				},
			},
			connectorClass: &networkingv1alpha1.ConnectorClass{
				ObjectMeta: metav1.ObjectMeta{Name: "datum-connect"},
			},
			lease: &coordinationv1.Lease{
				ObjectMeta: metav1.ObjectMeta{Name: "connector", Namespace: "default"},
				Spec: coordinationv1.LeaseSpec{
					LeaseDurationSeconds: ptr.To[int32](30),
					RenewTime:            &metav1.MicroTime{Time: time.Now().Add(-time.Minute)},
				},
			},
			wantStatus:      metav1.ConditionTrue,
			wantReason:      networkingv1alpha1.ConnectorReasonAccepted,
			wantReady:       metav1.ConditionFalse,
			wantReadyReason: networkingv1alpha1.ConnectorReasonNotReady,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testScheme := runtime.NewScheme()
			assert.NoError(t, scheme.AddToScheme(testScheme))
			assert.NoError(t, coordinationv1.AddToScheme(testScheme))
			assert.NoError(t, networkingv1alpha1.AddToScheme(testScheme))

			builder := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(tt.connector)
			if tt.connectorClass != nil {
				builder = builder.WithObjects(tt.connectorClass)
			}
			if tt.lease != nil {
				builder = builder.WithObjects(tt.lease)
			}
			builder = builder.WithStatusSubresource(tt.connector)
			cl := builder.Build()

			reconciler := &ConnectorReconciler{mgr: &fakeMockManager{cl: cl}}
			req := mcreconcile.Request{
				Request: reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(tt.connector),
				},
				ClusterName: "single",
			}

			_, err := reconciler.Reconcile(context.Background(), req)
			assert.NoError(t, err)

			var updated networkingv1alpha1.Connector
			assert.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(tt.connector), &updated))

			condition := apimeta.FindStatusCondition(updated.Status.Conditions, networkingv1alpha1.ConnectorConditionAccepted)
			if assert.NotNil(t, condition) {
				assert.Equal(t, tt.wantStatus, condition.Status)
				assert.Equal(t, tt.wantReason, condition.Reason)
			}

			readyCondition := apimeta.FindStatusCondition(updated.Status.Conditions, networkingv1alpha1.ConnectorConditionReady)
			if assert.NotNil(t, readyCondition) {
				assert.Equal(t, tt.wantReady, readyCondition.Status)
				assert.Equal(t, tt.wantReadyReason, readyCondition.Reason)
			}
		})
	}
}

func TestConnectorAdvertisementReconcile(t *testing.T) {
	log.SetLogger(zap.New(zap.UseDevMode(true)))

	tests := []struct {
		name          string
		advertisement *networkingv1alpha1.ConnectorAdvertisement
		connector     *networkingv1alpha1.Connector
		wantStatus    metav1.ConditionStatus
		wantReason    string
	}{
		{
			name: "connector reference resolved",
			advertisement: &networkingv1alpha1.ConnectorAdvertisement{
				ObjectMeta: metav1.ObjectMeta{Name: "ad", Namespace: "default"},
				Spec: networkingv1alpha1.ConnectorAdvertisementSpec{
					ConnectorRef: networkingv1alpha1.LocalConnectorReference{Name: "connector"},
				},
			},
			connector: &networkingv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{Name: "connector", Namespace: "default"},
				Spec:       networkingv1alpha1.ConnectorSpec{ConnectorClassName: "datum-connect"},
			},
			wantStatus: metav1.ConditionTrue,
			wantReason: networkingv1alpha1.ConnectorAdvertisementReasonAccepted,
		},
		{
			name: "connector reference missing",
			advertisement: &networkingv1alpha1.ConnectorAdvertisement{
				ObjectMeta: metav1.ObjectMeta{Name: "ad", Namespace: "default"},
				Spec: networkingv1alpha1.ConnectorAdvertisementSpec{
					ConnectorRef: networkingv1alpha1.LocalConnectorReference{Name: "missing"},
				},
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: networkingv1alpha1.ConnectorAdvertisementReasonConnectorNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testScheme := runtime.NewScheme()
			assert.NoError(t, scheme.AddToScheme(testScheme))
			assert.NoError(t, networkingv1alpha1.AddToScheme(testScheme))

			builder := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.advertisement).
				WithStatusSubresource(tt.advertisement)
			if tt.connector != nil {
				builder = builder.WithObjects(tt.connector)
			}
			cl := builder.Build()

			reconciler := &ConnectorAdvertisementReconciler{mgr: &fakeMockManager{cl: cl}}
			req := mcreconcile.Request{
				Request: reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(tt.advertisement),
				},
				ClusterName: "single",
			}

			_, err := reconciler.Reconcile(context.Background(), req)
			assert.NoError(t, err)

			var updated networkingv1alpha1.ConnectorAdvertisement
			assert.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(tt.advertisement), &updated))

			condition := apimeta.FindStatusCondition(updated.Status.Conditions, networkingv1alpha1.ConnectorAdvertisementConditionAccepted)
			if assert.NotNil(t, condition) {
				assert.Equal(t, tt.wantStatus, condition.Status)
				assert.Equal(t, tt.wantReason, condition.Reason)
			}
			if tt.connector != nil {
				assert.True(t, metav1.IsControlledBy(&updated, tt.connector))
			}
		})
	}
}

// patchCountingClient wraps a fake client and counts how many times Patch has
// been called. Used to verify idempotency in downstream Gateway annotation tests.
type patchCountingClient struct {
	client.Client
	mu         sync.Mutex
	patchCount int
}

func (c *patchCountingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	c.mu.Lock()
	c.patchCount++
	c.mu.Unlock()
	return c.Client.Patch(ctx, obj, patch, opts...)
}

func (c *patchCountingClient) PatchCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.patchCount
}

// buildConnectorTestScheme assembles the scheme needed for connector annotation tests.
func buildConnectorTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	assert.NoError(t, scheme.AddToScheme(s))
	assert.NoError(t, corev1.AddToScheme(s))
	assert.NoError(t, coordinationv1.AddToScheme(s))
	assert.NoError(t, networkingv1alpha1.AddToScheme(s))
	assert.NoError(t, networkingv1alpha.AddToScheme(s))
	assert.NoError(t, gatewayv1.Install(s))
	return s
}

// TestConnectorReconcile_ReadyFlip_TouchesDownstreamGatewayAnnotation verifies
// that when a Connector's Ready condition flips (nil → True via a valid Lease),
// the reconciler patches the trigger annotation on the affected downstream
// Gateway, causing EG's AnnotationChangedPredicate to fire.
func TestConnectorReconcile_ReadyFlip_TouchesDownstreamGatewayAnnotation(t *testing.T) {
	log.SetLogger(zap.New(zap.UseDevMode(true)))

	const (
		upstreamNS   = "user-project"
		upstreamUID  = types.UID("ns-uid-abc-123")
		downstreamNS = "ns-ns-uid-abc-123" // "ns-" + upstreamUID
		connName     = "my-connector"
		proxyName    = "my-httpproxy"
	)

	testScheme := buildConnectorTestScheme(t)

	connector := &networkingv1alpha1.Connector{
		ObjectMeta: metav1.ObjectMeta{
			Name:      connName,
			Namespace: upstreamNS,
		},
		Spec: networkingv1alpha1.ConnectorSpec{
			ConnectorClassName: "datum-connect",
		},
	}
	connectorClass := &networkingv1alpha1.ConnectorClass{
		ObjectMeta: metav1.ObjectMeta{Name: "datum-connect"},
	}
	// HTTPProxy that references the Connector, creating Gateway affinity.
	httpProxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: proxyName, Namespace: upstreamNS},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{
				{
					Backends: []networkingv1alpha.HTTPProxyRuleBackend{
						{
							Connector: &networkingv1alpha.ConnectorReference{Name: connName},
							Endpoint:  "http://example.com",
						},
					},
				},
			},
		},
	}
	// Upstream namespace with a known UID so we can predict the downstream ns name.
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: upstreamNS,
			UID:  upstreamUID,
		},
	}
	// Valid lease causes the reconciler to set Ready=True.
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: connName, Namespace: upstreamNS},
		Spec: coordinationv1.LeaseSpec{
			LeaseDurationSeconds: ptr.To[int32](30),
			RenewTime:            &metav1.MicroTime{Time: time.Now()},
		},
	}

	upstreamCl := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(connector, connectorClass, httpProxy, ns, lease).
		WithStatusSubresource(connector).
		Build()

	// Downstream Gateway — same name as the HTTPProxy, in the mapped namespace.
	downstreamGW := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Name: proxyName, Namespace: downstreamNS},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "datum-downstream-gateway",
		},
	}
	downstreamFakeCl := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(downstreamGW).
		Build()
	countingDownstreamCl := &patchCountingClient{Client: downstreamFakeCl}

	reconciler := &ConnectorReconciler{
		mgr: &fakeMockManager{cl: upstreamCl},
		Config: config.NetworkServicesOperator{
			Gateway: config.GatewayConfig{
				EPPEmissionEnabled: ptr.To(false), // Mode B
			},
		},
		DownstreamCluster: &fakeCluster{cl: countingDownstreamCl},
	}

	req := mcreconcile.Request{
		Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(connector)},
		ClusterName: "single",
	}
	_, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)

	// Connector should now be Ready=True.
	var updatedConnector networkingv1alpha1.Connector
	assert.NoError(t, upstreamCl.Get(context.Background(), client.ObjectKeyFromObject(connector), &updatedConnector))
	readyCond := apimeta.FindStatusCondition(updatedConnector.Status.Conditions, networkingv1alpha1.ConnectorConditionReady)
	assert.NotNil(t, readyCond)
	assert.Equal(t, metav1.ConditionTrue, readyCond.Status)

	// Downstream gateway must have the trigger annotation set.
	var updatedGW gatewayv1.Gateway
	assert.NoError(t, downstreamFakeCl.Get(context.Background(), client.ObjectKeyFromObject(downstreamGW), &updatedGW))
	assert.NotEmpty(t, updatedGW.Annotations[connectorReadyAnnotationKey],
		"trigger annotation should be set on downstream gateway after Ready flip")

	// Exactly one Patch should have been issued (the annotation write).
	assert.Equal(t, 1, countingDownstreamCl.PatchCount(),
		"should have issued exactly one Patch to set the trigger annotation")
}

// TestConnectorReconcile_ReadyNoFlip_NoAnnotationWrite verifies that when a
// Connector's Ready condition does NOT change between reconciles (stays True),
// the reconciler does NOT write the trigger annotation again — idempotent.
func TestConnectorReconcile_ReadyNoFlip_NoAnnotationWrite(t *testing.T) {
	log.SetLogger(zap.New(zap.UseDevMode(true)))

	const (
		upstreamNS   = "user-project"
		upstreamUID  = types.UID("ns-uid-abc-123")
		downstreamNS = "ns-ns-uid-abc-123"
		connName     = "my-connector"
		proxyName    = "my-httpproxy"
	)

	testScheme := buildConnectorTestScheme(t)

	// Connector whose status already has Ready=True (set before first reconcile).
	// This simulates the connector being stable — no flip this reconcile.
	existingReadyCondition := metav1.Condition{
		Type:               networkingv1alpha1.ConnectorConditionReady,
		Status:             metav1.ConditionTrue,
		Reason:             networkingv1alpha1.ConnectorReasonReady,
		ObservedGeneration: 1,
	}
	connector := &networkingv1alpha1.Connector{
		ObjectMeta: metav1.ObjectMeta{
			Name:       connName,
			Namespace:  upstreamNS,
			Generation: 1,
		},
		Spec: networkingv1alpha1.ConnectorSpec{
			ConnectorClassName: "datum-connect",
		},
		Status: networkingv1alpha1.ConnectorStatus{
			Conditions: []metav1.Condition{
				{
					Type:               networkingv1alpha1.ConnectorConditionAccepted,
					Status:             metav1.ConditionTrue,
					Reason:             networkingv1alpha1.ConnectorReasonAccepted,
					ObservedGeneration: 1,
				},
				existingReadyCondition,
			},
			LeaseRef: &corev1.LocalObjectReference{Name: connName},
		},
	}
	connectorClass := &networkingv1alpha1.ConnectorClass{
		ObjectMeta: metav1.ObjectMeta{Name: "datum-connect"},
	}
	httpProxy := &networkingv1alpha.HTTPProxy{
		ObjectMeta: metav1.ObjectMeta{Name: proxyName, Namespace: upstreamNS},
		Spec: networkingv1alpha.HTTPProxySpec{
			Rules: []networkingv1alpha.HTTPProxyRule{
				{
					Backends: []networkingv1alpha.HTTPProxyRuleBackend{
						{
							Connector: &networkingv1alpha.ConnectorReference{Name: connName},
							Endpoint:  "http://example.com",
						},
					},
				},
			},
		},
	}
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: upstreamNS, UID: upstreamUID},
	}
	// Lease still valid → Ready stays True.
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: connName, Namespace: upstreamNS},
		Spec: coordinationv1.LeaseSpec{
			LeaseDurationSeconds: ptr.To[int32](30),
			RenewTime:            &metav1.MicroTime{Time: time.Now()},
		},
	}

	upstreamCl := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(connector, connectorClass, httpProxy, ns, lease).
		WithStatusSubresource(connector).
		Build()

	// Pre-set the annotation to the value the reconciler would write so we can
	// detect whether it tries to re-write it (idempotency).
	expectedAnnotationValue := connectorReadyAnnotationValue(&existingReadyCondition)
	downstreamGW := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxyName,
			Namespace: downstreamNS,
			Annotations: map[string]string{
				connectorReadyAnnotationKey: expectedAnnotationValue,
			},
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: "datum-downstream-gateway",
		},
	}
	downstreamFakeCl := fake.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(downstreamGW).
		Build()
	countingDownstreamCl := &patchCountingClient{Client: downstreamFakeCl}

	reconciler := &ConnectorReconciler{
		mgr: &fakeMockManager{cl: upstreamCl},
		Config: config.NetworkServicesOperator{
			Gateway: config.GatewayConfig{
				EPPEmissionEnabled: ptr.To(false),
			},
		},
		DownstreamCluster: &fakeCluster{cl: countingDownstreamCl},
	}

	req := mcreconcile.Request{
		Request:     reconcile.Request{NamespacedName: client.ObjectKeyFromObject(connector)},
		ClusterName: "single",
	}
	_, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)

	// Downstream gateway annotation should remain unchanged.
	var updatedGW gatewayv1.Gateway
	assert.NoError(t, downstreamFakeCl.Get(context.Background(), client.ObjectKeyFromObject(downstreamGW), &updatedGW))
	assert.Equal(t, expectedAnnotationValue, updatedGW.Annotations[connectorReadyAnnotationKey],
		"annotation should be unchanged when Ready does not flip")

	// No Patch should have been issued because Ready status is unchanged AND
	// the annotation value is already correct.
	assert.Equal(t, 0, countingDownstreamCl.PatchCount(),
		"should NOT issue a Patch when Ready does not change")
}
