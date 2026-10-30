// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"reflect"
	"testing"

	networking "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestInternalDNSForeignOrUnknownLocationDoesNotPublish(t *testing.T) {
	for _, location := range []string{"other-location", ""} {
		t.Run(location, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := networking.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			nc := testNetworkContext("context", "context-uid")
			nc.Annotations = testDNSIntent(metav1.Now().Time)
			nc.Status.Conditions = []metav1.Condition{{Type: networking.NetworkContextDNSReady, Status: metav1.ConditionTrue, Reason: "Ready", LastTransitionTime: metav1.Now()}}
			hub := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(nc).WithObjects(nc.DeepCopy()).Build()
			before := &networking.NetworkContext{}
			if err := hub.Get(context.Background(), client.ObjectKeyFromObject(nc), before); err != nil {
				t.Fatal(err)
			}
			cell := fake.NewClientBuilder().WithScheme(scheme).Build()
			r := &InternalDNSCellReconciler{Location: config.LocationConfig{Name: location}, localReader: noServingLocations{cell}, HubCluster: &fakeCluster{cl: hub}}
			result, err := r.reconcile(context.Background(), cell, nc)
			if err != nil {
				t.Fatal(err)
			}
			if result.RequeueAfter == 0 {
				t.Fatal("must retry location resolution")
			}
			got := &networking.NetworkContext{}
			if err := hub.Get(context.Background(), client.ObjectKeyFromObject(nc), got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, got) {
				t.Fatal("unrelated cell overwrote readiness")
			}
		})
	}
}
