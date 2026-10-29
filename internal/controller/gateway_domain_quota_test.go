package controller

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/config"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"testing"
	"time"
)

func TestGatewayDomainQuotaIsAnActionableRefusal(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		refused bool
	}{
		{"quota", apierrors.NewForbidden(schema.GroupResource{Group: "networking.datumapis.com", Resource: "domains"}, "example.com", errors.New("You've reached your quota for this resource type (Insufficient quota resources. Contact your account administrator to review quota limits and usage.).")), true},
		{"rbac", apierrors.NewForbidden(schema.GroupResource{Resource: "domains"}, "example.com", errors.New("cannot create resource domains")), false},
		{"transport", errors.New("connection refused"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			denial := tc.err
			upstream := fake.NewClientBuilder().WithScheme(claimsTestScheme(t)).WithInterceptorFuncs(interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*networkingv1alpha.Domain); ok && denial != nil {
					return denial
				}
				return c.Create(ctx, obj, opts...)
			}}).Build()
			r := &GatewayReconciler{Config: config.NetworkServicesOperator{Gateway: config.GatewayConfig{TargetDomain: "datumproxy.net"}}}
			gateway := claimingGateway("www.example.com", "other.example.com")
			verified, refusals, err := r.ensureHostnameVerification(context.Background(), upstream, gateway, &gatewayv1.Gateway{})
			if !tc.refused {
				require.ErrorIs(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.NotContains(t, verified, "www.example.com")
			require.Len(t, refusals, 2)
			require.Equal(t, domainQuotaExceededReason, refusals["www.example.com"].reason)
			require.Contains(t, refusals["www.example.com"].message, "quota")
			require.Equal(t, time.Minute, domainQuotaRequeue(refusals))
			denial = nil
			_, refusals, err = r.ensureHostnameVerification(context.Background(), upstream, gateway, &gatewayv1.Gateway{})
			require.NoError(t, err)
			require.Empty(t, refusals)
			require.Zero(t, domainQuotaRequeue(refusals))
			var domain networkingv1alpha.Domain
			require.NoError(t, upstream.Get(context.Background(), client.ObjectKey{Namespace: gateway.Namespace, Name: "example.com"}, &domain))
		})
	}
}
