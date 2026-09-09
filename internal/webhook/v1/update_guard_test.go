// SPDX-License-Identifier: AGPL-3.0-only

package v1

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"

	"go.datum.net/network-services-operator/internal/config"
)

func clusterContext() context.Context {
	return mccontext.WithCluster(context.Background(), "test")
}

func invalidHTTPRoute() *gatewayv1.HTTPRoute {
	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "route", Namespace: "default"},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{Name: "gw", Kind: ptr.To(gatewayv1.Kind("NotAGateway"))},
				},
			},
		},
	}
}

func invalidBackendTLSPolicy() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "BackendTLSPolicy",
		"metadata": map[string]any{
			"name":      "btls",
			"namespace": "default",
		},
		"spec": map[string]any{
			"targetRefs": []any{
				map[string]any{
					"group": "wrong.example.com",
					"kind":  "NotABackend",
					"name":  "target",
				},
			},
		},
	}}
}

func TestHTTPRouteValidateUpdateSkipsUnchangedSpec(t *testing.T) {
	v := &HTTPRouteCustomValidator{validationOpts: config.HTTPRouteValidationOptions{}}

	oldObj, changed := invalidHTTPRoute(), invalidHTTPRoute()
	changed.Spec.ParentRefs[0].Name = "other"
	if _, err := v.ValidateUpdate(clusterContext(), oldObj, changed); err == nil {
		t.Fatal("a spec change on an already-invalid HTTPRoute was admitted, want rejected")
	}

	metadataOnly := invalidHTTPRoute()
	metadataOnly.Finalizers = []string{"example.com/f"}
	if _, err := v.ValidateUpdate(clusterContext(), oldObj, metadataOnly); err != nil {
		t.Fatalf("a metadata-only HTTPRoute update was rejected: %v", err)
	}

	beingDeleted := invalidHTTPRoute()
	now := metav1.Now()
	beingDeleted.DeletionTimestamp = &now
	beingDeleted.Spec.ParentRefs[0].Name = "other"
	if _, err := v.ValidateUpdate(clusterContext(), oldObj, beingDeleted); err != nil {
		t.Fatalf("an update to a deleting HTTPRoute was rejected: %v", err)
	}
}

func TestBackendTLSPolicyValidateUpdateSkipsUnchangedSpec(t *testing.T) {
	v := &BackendTLSPolicyCustomValidator{}

	oldObj := invalidBackendTLSPolicy()

	changed := invalidBackendTLSPolicy()
	targetRefs, _, _ := unstructured.NestedSlice(changed.Object, "spec", "targetRefs")
	targetRefs[0].(map[string]any)["name"] = "other"
	if err := unstructured.SetNestedSlice(changed.Object, targetRefs, "spec", "targetRefs"); err != nil {
		t.Fatalf("failed to build the changed object: %v", err)
	}
	if _, err := v.ValidateUpdate(clusterContext(), oldObj, changed); err == nil {
		t.Fatal("a spec change on an already-invalid BackendTLSPolicy was admitted, want rejected")
	}

	metadataOnly := invalidBackendTLSPolicy()
	metadataOnly.SetFinalizers([]string{"example.com/f"})
	if _, err := v.ValidateUpdate(clusterContext(), oldObj, metadataOnly); err != nil {
		t.Fatalf("a metadata-only BackendTLSPolicy update was rejected: %v", err)
	}

	beingDeleted := invalidBackendTLSPolicy()
	now := metav1.Now()
	beingDeleted.SetDeletionTimestamp(&now)
	if _, err := v.ValidateUpdate(clusterContext(), oldObj, beingDeleted); err != nil {
		t.Fatalf("an update to a deleting BackendTLSPolicy was rejected: %v", err)
	}
}

func TestGatewayValidateUpdateSkipsUnchangedSpec(t *testing.T) {
	gateway := func() *gatewayv1.Gateway {
		return &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: "gw", Namespace: "default"},
			Spec:       gatewayv1.GatewaySpec{GatewayClassName: "some-class"},
		}
	}

	v := &GatewayCustomValidator{}

	metadataOnly := gateway()
	metadataOnly.Finalizers = []string{"example.com/f"}
	if _, err := v.ValidateUpdate(context.Background(), gateway(), metadataOnly); err != nil {
		t.Fatalf("a metadata-only Gateway update was rejected: %v", err)
	}

	beingDeleted := gateway()
	now := metav1.Now()
	beingDeleted.DeletionTimestamp = &now
	beingDeleted.Spec.GatewayClassName = "other-class"
	if _, err := v.ValidateUpdate(context.Background(), gateway(), beingDeleted); err != nil {
		t.Fatalf("an update to a deleting Gateway was rejected: %v", err)
	}
}
