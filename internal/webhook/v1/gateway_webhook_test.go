// SPDX-License-Identifier: AGPL-3.0-only

package v1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestValidateManagedGatewayClass(t *testing.T) {
	const controllerName gatewayv1.GatewayController = "gateway.networking.datumapis.com/external-global-proxy-controller"

	managedClass := &gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "datum-external-global-proxy"},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: controllerName},
	}
	foreignClass := &gatewayv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "some-other-provider"},
		Spec:       gatewayv1.GatewayClassSpec{ControllerName: "example.com/other-controller"},
	}

	gatewayFor := func(className string) *gatewayv1.Gateway {
		return &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec:       gatewayv1.GatewaySpec{GatewayClassName: gatewayv1.ObjectName(className)},
		}
	}

	tests := []struct {
		name       string
		gateway    *gatewayv1.Gateway
		wantReject bool
	}{
		{
			name:       "managed class is accepted",
			gateway:    gatewayFor("datum-external-global-proxy"),
			wantReject: false,
		},
		{
			name:       "class managed by another controller is rejected",
			gateway:    gatewayFor("some-other-provider"),
			wantReject: true,
		},
		{
			name:       "nonexistent class is rejected",
			gateway:    gatewayFor("does-not-exist"),
			wantReject: true,
		},
	}

	scheme := runtime.NewScheme()
	require.NoError(t, gatewayv1.Install(scheme))

	clusterClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(managedClass, foreignClass).
		Build()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fieldErr, err := validateManagedGatewayClass(context.Background(), clusterClient, controllerName, tt.gateway)
			require.NoError(t, err)

			if !tt.wantReject {
				assert.Nil(t, fieldErr)
				return
			}

			require.NotNil(t, fieldErr)
			assert.Equal(t, "spec.gatewayClassName", fieldErr.Field)
			assert.Contains(t, fieldErr.Error(), "datum-external-global-proxy")
		})
	}
}
