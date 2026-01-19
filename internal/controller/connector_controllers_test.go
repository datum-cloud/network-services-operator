package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
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
		name           string
		connector      *networkingv1alpha1.Connector
		connectorClass *networkingv1alpha1.ConnectorClass
		wantStatus     metav1.ConditionStatus
		wantReason     string
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
			wantStatus: metav1.ConditionTrue,
			wantReason: networkingv1alpha1.ConnectorReasonAccepted,
		},
		{
			name: "connector class missing",
			connector: &networkingv1alpha1.Connector{
				ObjectMeta: metav1.ObjectMeta{Name: "connector", Namespace: "default"},
				Spec: networkingv1alpha1.ConnectorSpec{
					ConnectorClassName: "missing",
				},
			},
			wantStatus: metav1.ConditionFalse,
			wantReason: networkingv1alpha1.ConnectorReasonConnectorClassNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testScheme := runtime.NewScheme()
			assert.NoError(t, scheme.AddToScheme(testScheme))
			assert.NoError(t, networkingv1alpha1.AddToScheme(testScheme))

			builder := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(tt.connector)
			if tt.connectorClass != nil {
				builder = builder.WithObjects(tt.connectorClass)
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
