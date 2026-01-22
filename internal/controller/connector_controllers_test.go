package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	coordinationv1 "k8s.io/api/coordination/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	networkingv1alpha1 "go.datum.net/network-services-operator/api/v1alpha1"
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
