// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	"context"
	"testing"

	envoygatewayv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	mccontext "sigs.k8s.io/multicluster-runtime/pkg/context"

	"go.datum.net/network-services-operator/internal/config"
)

func clusterContext() context.Context {
	return mccontext.WithCluster(context.Background(), "test")
}

func deleting[T interface {
	SetDeletionTimestamp(*metav1.Time)
}](obj T) T {
	now := metav1.Now()
	obj.SetDeletionTimestamp(&now)
	return obj
}

func invalidSecurityPolicy() *envoygatewayv1alpha1.SecurityPolicy {
	return &envoygatewayv1alpha1.SecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "sp", Namespace: "default"},
		Spec: envoygatewayv1alpha1.SecurityPolicySpec{
			ExtAuth: &envoygatewayv1alpha1.ExtAuth{},
		},
	}
}

func invalidBackendTrafficPolicy() *envoygatewayv1alpha1.BackendTrafficPolicy {
	return &envoygatewayv1alpha1.BackendTrafficPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "btp", Namespace: "default"},
		Spec: envoygatewayv1alpha1.BackendTrafficPolicySpec{
			ResponseOverride: []*envoygatewayv1alpha1.ResponseOverride{
				{Redirect: &envoygatewayv1alpha1.CustomRedirect{Hostname: ptr.To(gatewayv1.PreciseHostname("redirect.example.com"))}},
			},
		},
	}
}

func invalidBackend() *envoygatewayv1alpha1.Backend {
	return &envoygatewayv1alpha1.Backend{
		ObjectMeta: metav1.ObjectMeta{Name: "backend", Namespace: "default"},
		Spec: envoygatewayv1alpha1.BackendSpec{
			Endpoints: []envoygatewayv1alpha1.BackendEndpoint{
				{Unix: &envoygatewayv1alpha1.UnixSocket{Path: "/tmp/sock"}},
			},
		},
	}
}

func invalidHTTPRouteFilter() *envoygatewayv1alpha1.HTTPRouteFilter {
	return &envoygatewayv1alpha1.HTTPRouteFilter{
		ObjectMeta: metav1.ObjectMeta{Name: "filter", Namespace: "default"},
		Spec: envoygatewayv1alpha1.HTTPRouteFilterSpec{
			DirectResponse: &envoygatewayv1alpha1.HTTPDirectResponseFilter{
				Body: &envoygatewayv1alpha1.CustomResponseBody{
					Inline: ptr.To("this body exceeds a zero-byte limit"),
				},
			},
		},
	}
}

func TestValidateUpdateSkipsUnchangedSpec(t *testing.T) {
	tests := []struct {
		name          string
		rejectChange  func() error
		acceptNoSpec  func() error
		acceptDeleted func() error
	}{
		{
			name: "SecurityPolicy",
			rejectChange: func() error {
				v := &SecurityPolicyCustomValidator{}
				oldObj, newObj := invalidSecurityPolicy(), invalidSecurityPolicy()
				newObj.Spec.ExtAuth = &envoygatewayv1alpha1.ExtAuth{FailOpen: ptr.To(true)}
				_, err := v.ValidateUpdate(clusterContext(), oldObj, newObj)
				return err
			},
			acceptNoSpec: func() error {
				v := &SecurityPolicyCustomValidator{}
				oldObj, newObj := invalidSecurityPolicy(), invalidSecurityPolicy()
				newObj.Finalizers = []string{"example.com/f"}
				_, err := v.ValidateUpdate(clusterContext(), oldObj, newObj)
				return err
			},
			acceptDeleted: func() error {
				v := &SecurityPolicyCustomValidator{}
				oldObj, newObj := invalidSecurityPolicy(), deleting(invalidSecurityPolicy())
				newObj.Spec.ExtAuth = &envoygatewayv1alpha1.ExtAuth{FailOpen: ptr.To(true)}
				_, err := v.ValidateUpdate(clusterContext(), oldObj, newObj)
				return err
			},
		},
		{
			name: "BackendTrafficPolicy",
			rejectChange: func() error {
				v := &BackendTrafficPolicyCustomValidator{}
				oldObj, newObj := invalidBackendTrafficPolicy(), invalidBackendTrafficPolicy()
				newObj.Spec.ResponseOverride[0].Redirect.Hostname = ptr.To(gatewayv1.PreciseHostname("other.example.com"))
				_, err := v.ValidateUpdate(clusterContext(), oldObj, newObj)
				return err
			},
			acceptNoSpec: func() error {
				v := &BackendTrafficPolicyCustomValidator{}
				oldObj, newObj := invalidBackendTrafficPolicy(), invalidBackendTrafficPolicy()
				newObj.Finalizers = []string{"example.com/f"}
				_, err := v.ValidateUpdate(clusterContext(), oldObj, newObj)
				return err
			},
			acceptDeleted: func() error {
				v := &BackendTrafficPolicyCustomValidator{}
				oldObj, newObj := invalidBackendTrafficPolicy(), deleting(invalidBackendTrafficPolicy())
				newObj.Spec.ResponseOverride[0].Redirect.Hostname = ptr.To(gatewayv1.PreciseHostname("other.example.com"))
				_, err := v.ValidateUpdate(clusterContext(), oldObj, newObj)
				return err
			},
		},
		{
			name: "Backend",
			rejectChange: func() error {
				v := &BackendCustomValidator{}
				oldObj, newObj := invalidBackend(), invalidBackend()
				newObj.Spec.Endpoints[0].Unix.Path = "/tmp/other"
				_, err := v.ValidateUpdate(clusterContext(), oldObj, newObj)
				return err
			},
			acceptNoSpec: func() error {
				v := &BackendCustomValidator{}
				oldObj, newObj := invalidBackend(), invalidBackend()
				newObj.Finalizers = []string{"example.com/f"}
				_, err := v.ValidateUpdate(clusterContext(), oldObj, newObj)
				return err
			},
			acceptDeleted: func() error {
				v := &BackendCustomValidator{}
				oldObj, newObj := invalidBackend(), deleting(invalidBackend())
				newObj.Spec.Endpoints[0].Unix.Path = "/tmp/other"
				_, err := v.ValidateUpdate(clusterContext(), oldObj, newObj)
				return err
			},
		},
		{
			name: "HTTPRouteFilter",
			rejectChange: func() error {
				v := &HTTPRouteFilterCustomValidator{validationOpts: config.HTTPRouteFilterValidationOptions{}}
				oldObj, newObj := invalidHTTPRouteFilter(), invalidHTTPRouteFilter()
				newObj.Spec.DirectResponse.Body.Inline = ptr.To("a different oversized body")
				_, err := v.ValidateUpdate(clusterContext(), oldObj, newObj)
				return err
			},
			acceptNoSpec: func() error {
				v := &HTTPRouteFilterCustomValidator{validationOpts: config.HTTPRouteFilterValidationOptions{}}
				oldObj, newObj := invalidHTTPRouteFilter(), invalidHTTPRouteFilter()
				newObj.Finalizers = []string{"example.com/f"}
				_, err := v.ValidateUpdate(clusterContext(), oldObj, newObj)
				return err
			},
			acceptDeleted: func() error {
				v := &HTTPRouteFilterCustomValidator{validationOpts: config.HTTPRouteFilterValidationOptions{}}
				oldObj, newObj := invalidHTTPRouteFilter(), deleting(invalidHTTPRouteFilter())
				newObj.Spec.DirectResponse.Body.Inline = ptr.To("a different oversized body")
				_, err := v.ValidateUpdate(clusterContext(), oldObj, newObj)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.rejectChange(); err == nil {
				t.Fatal("a spec change on an already-invalid object was admitted, want rejected")
			}
			if err := tt.acceptNoSpec(); err != nil {
				t.Fatalf("a metadata-only update was rejected: %v", err)
			}
			if err := tt.acceptDeleted(); err != nil {
				t.Fatalf("an update to a deleting object was rejected: %v", err)
			}
		})
	}
}
