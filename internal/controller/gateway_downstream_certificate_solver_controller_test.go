package controller

import (
	"context"
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"go.datum.net/network-services-operator/internal/config"
)

func TestGatewayDownstreamCertificateSolverReconciler(t *testing.T) {
	testScheme := runtime.NewScheme()
	assert.NoError(t, scheme.AddToScheme(testScheme))
	assert.NoError(t, gatewayv1.Install(testScheme))
	assert.NoError(t, envoygatewayv1alpha1.AddToScheme(testScheme))

	operatorConfig := config.NetworkServicesOperator{
		Gateway: config.GatewayConfig{
			ClusterIssuerMap: map[string]string{
				"auto": "test-issuer",
			},
		},
	}
	config.SetObjectDefaults_NetworkServicesOperator(&operatorConfig)

	testNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
			UID:  uuid.NewUUID(),
		},
	}

	certNotReadyStatus := map[string]any{
		"conditions": []any{
			map[string]any{
				"type":   "Ready",
				"status": "False",
			},
		},
	}

	testGateway := newGateway(operatorConfig, testNamespace.Name, "test-gateway")

	tests := []struct {
		name            string
		certificate     *unstructured.Unstructured
		existingObjects []client.Object
		assert          func(t *testing.T, cl client.Client, result ctrl.Result)
	}{
		{
			name: "Certificate Ready",
			certificate: newUnstructuredGVK(certificateGVK, "test-cert-ready", testNamespace.Name, func(u *unstructured.Unstructured) {
				status := map[string]any{
					"conditions": []any{
						map[string]any{
							"type":   "Ready",
							"status": "True",
						},
					},
				}
				if err := unstructured.SetNestedMap(u.Object, status, "status"); err != nil {
					t.Fatalf("failed to set status: %v", err)
				}
			}),
			assert: func(t *testing.T, cl client.Client, result ctrl.Result) {
				// Expect no requeue
				assert.Equal(t, ctrl.Result{}, result)
			},
		},
		{
			name: "Irrelevant issuer",
			certificate: newUnstructuredGVK(certificateGVK, "test-cert-irrelevant-issuer", testNamespace.Name, func(u *unstructured.Unstructured) {
				if err := unstructured.SetNestedMap(u.Object, certNotReadyStatus, "status"); err != nil {
					t.Fatalf("failed to set status: %v", err)
				}

				spec := map[string]any{
					"issuerRef": map[string]any{
						"kind": "ClusterIssuer",
						"name": "some-other-issuer",
					},
				}
				if err := unstructured.SetNestedMap(u.Object, spec, "spec"); err != nil {
					t.Fatalf("failed to set spec: %v", err)
				}
			}),
			assert: func(t *testing.T, cl client.Client, result ctrl.Result) {
				// Expect no requeue
				assert.Equal(t, ctrl.Result{}, result)
			},
		},
		{
			name: "HTTPRouteFilter and HTTPRoute created",
			certificate: newUnstructuredGVK(certificateGVK, "test-cert-create-route", testNamespace.Name, func(u *unstructured.Unstructured) {
				if err := unstructured.SetNestedMap(u.Object, certNotReadyStatus, "status"); err != nil {
					t.Fatalf("failed to set status: %v", err)
				}

				spec := map[string]any{
					"issuerRef": map[string]any{
						"kind": "ClusterIssuer",
						"name": "test-issuer",
					},
				}
				if err := unstructured.SetNestedMap(u.Object, spec, "spec"); err != nil {
					t.Fatalf("failed to set spec: %v", err)
				}

				if err := controllerutil.SetControllerReference(testGateway, u, testScheme); err != nil {
					t.Fatalf("failed to set owner reference: %v", err)
				}
			}),
			existingObjects: append(
				[]client.Object{testGateway},
				func() []client.Object {
					var objs []client.Object
					order := newUnstructuredGVK(orderGVK, "test-order", testNamespace.Name, func(u *unstructured.Unstructured) {
						u.SetAnnotations(map[string]string{
							certManagerCertificateNameAnnotation: "test-cert-create-route",
						})
					})

					objs = append(objs, order)

					challenge := newUnstructuredGVK(challengeGVK, "test-challenge", testNamespace.Name, func(u *unstructured.Unstructured) {
						// Set spec.key and spec.token
						spec := map[string]any{
							"key":   "test-key",
							"token": "test-token",
						}
						if err := unstructured.SetNestedMap(u.Object, spec, "spec"); err != nil {
							t.Fatalf("failed to set spec on Challenge: %v", err)
						}
					})

					if err := controllerutil.SetControllerReference(order, challenge, testScheme); err != nil {
						t.Fatalf("failed to set owner reference on Challenge: %v", err)
					}

					objs = append(objs, challenge)

					return objs
				}()...,
			),
			assert: func(t *testing.T, cl client.Client, result ctrl.Result) {
				// Expect no requeue
				assert.Equal(t, ctrl.Result{}, result)

				// Check that HTTPRouteFilter and HTTPRoute were created and are owned
				// by the challenge
				httpRouteFilter := &envoygatewayv1alpha1.HTTPRouteFilter{}
				err := cl.Get(context.Background(), client.ObjectKey{
					Namespace: "test",
					Name:      "test-challenge",
				}, httpRouteFilter)
				assert.NoError(t, err, "expected HTTPRouteFilter to be created")

				owner := metav1.GetControllerOf(httpRouteFilter)
				assert.NotNil(t, owner, "expected HTTPRouteFilter to have an owner")
				assert.Equal(t, "test-challenge", owner.Name, "expected HTTPRouteFilter to be owned by the challenge")

				// Confirm that the filter's direct response body matches the key
				assert.Equal(t, "test-key", ptr.Deref(httpRouteFilter.Spec.DirectResponse.Body.Inline, ""), "expected HTTPRouteFilter direct response body to match challenge key")

				httpRoute := &gatewayv1.HTTPRoute{}
				err = cl.Get(context.Background(), client.ObjectKey{
					Namespace: "test",
					Name:      "test-challenge",
				}, httpRoute)
				assert.NoError(t, err, "expected HTTPRoute to be created")

				owner = metav1.GetControllerOf(httpRoute)
				assert.NotNil(t, owner, "expected HTTPRoute to have an owner")
				assert.Equal(t, "test-challenge", owner.Name, "expected HTTPRoute to be owned by the challenge")

				// Confirm that the HTTPRoute has a rule that matches the expected path
				found := false
				for _, rule := range httpRoute.Spec.Rules {
					for _, path := range rule.Matches {
						if path.Path != nil && ptr.Deref(path.Path.Value, "") == "/.well-known/acme-challenge/test-token" {
							found = true
							break
						}
					}
				}

				assert.True(t, found, "expected HTTPRoute to have a rule matching the challenge path")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeDownstreamClient := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithObjects(tt.certificate, testNamespace).
				WithObjects(tt.existingObjects...).
				WithStatusSubresource(tt.certificate).
				WithStatusSubresource(tt.existingObjects...).
				Build()

			ctx := context.Background()

			reconciler := &GatewayDownstreamCertificateSolverReconciler{
				Config:            operatorConfig,
				DownstreamCluster: &fakeCluster{cl: fakeDownstreamClient},
			}

			result, err := reconciler.Reconcile(
				ctx,
				ctrl.Request{
					NamespacedName: client.ObjectKeyFromObject(tt.certificate),
				},
			)
			if assert.NoError(t, err) {
				tt.assert(t, fakeDownstreamClient, result)
			}
		})
	}
}

func newUnstructuredGVK(gvk schema.GroupVersionKind, name, namespace string, opts ...func(*unstructured.Unstructured)) *unstructured.Unstructured {
	obj := newUnstructuredForGVK(gvk)
	obj.SetName(name)
	obj.SetNamespace(namespace)
	obj.SetUID(uuid.NewUUID())
	obj.SetCreationTimestamp(metav1.Now())

	for _, opt := range opts {
		opt(obj)
	}

	return obj
}
