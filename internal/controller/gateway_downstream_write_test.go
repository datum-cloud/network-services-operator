// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"go.datum.net/network-services-operator/internal/downstreamclient"
)

func TestWriteDownstreamGateway(t *testing.T) {
	serving := []gatewayv1.Listener{{
		Name:     "default-http",
		Port:     DefaultHTTPPort,
		Protocol: gatewayv1.HTTPProtocolType,
		Hostname: ptr.To(gatewayv1.Hostname("gw.example.com")),
	}}
	desired := []gatewayv1.Listener{{
		Name:     "default-https",
		Port:     DefaultHTTPSPort,
		Protocol: gatewayv1.HTTPSProtocolType,
		Hostname: ptr.To(gatewayv1.Hostname("gw.example.com")),
	}}
	reissued := reissuanceAnnotationKey("gw-cert")

	tests := []struct {
		name             string
		stored           map[string]string
		storedListeners  []gatewayv1.Listener
		desiredListeners []gatewayv1.Listener
		awaiting         bool
		wantWrites       int
		wantAnnotations  map[string]string
		wantListeners    []gatewayv1.Listener
		wantNoDownstream bool
	}{
		{
			name:             "no copy and no listener: nothing created",
			awaiting:         true,
			wantNoDownstream: true,
		},
		{
			name:             "no copy: created with the mark",
			desiredListeners: desired,
			awaiting:         true,
			wantWrites:       1,
			wantAnnotations:  map[string]string{awaitingHostnameClaimAnnotation: awaitingHostnameClaimValue},
			wantListeners:    desired,
		},
		{
			name:             "unchanged: not written",
			stored:           map[string]string{awaitingHostnameClaimAnnotation: awaitingHostnameClaimValue},
			storedListeners:  desired,
			desiredListeners: desired,
			awaiting:         true,
			wantAnnotations:  map[string]string{awaitingHostnameClaimAnnotation: awaitingHostnameClaimValue},
			wantListeners:    desired,
		},
		{
			name:             "the mark cleared, another annotation kept",
			stored:           map[string]string{awaitingHostnameClaimAnnotation: awaitingHostnameClaimValue, reissued: "2"},
			storedListeners:  desired,
			desiredListeners: desired,
			wantWrites:       1,
			wantAnnotations:  map[string]string{reissued: "2"},
			wantListeners:    desired,
		},
		{
			name:             "the mark set, another annotation kept",
			stored:           map[string]string{reissued: "2"},
			storedListeners:  desired,
			desiredListeners: desired,
			awaiting:         true,
			wantWrites:       1,
			wantAnnotations:  map[string]string{awaitingHostnameClaimAnnotation: awaitingHostnameClaimValue, reissued: "2"},
			wantListeners:    desired,
		},
		{
			name:            "no listener: the serving listeners kept, the mark set",
			stored:          map[string]string{reissued: "2"},
			storedListeners: serving,
			awaiting:        true,
			wantWrites:      1,
			wantAnnotations: map[string]string{awaitingHostnameClaimAnnotation: awaitingHostnameClaimValue, reissued: "2"},
			wantListeners:   serving,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			testScheme := claimsTestScheme(t)

			namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test", UID: uuid.NewUUID()}}
			upstreamGateway := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "test", Name: "gw", UID: uuid.NewUUID()}}
			upstream := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(namespace, upstreamGateway).Build()

			writes := 0
			countGateway := func(obj client.Object) {
				if _, ok := obj.(*gatewayv1.Gateway); ok {
					writes++
				}
			}
			downstream := fake.NewClientBuilder().
				WithScheme(testScheme).
				WithInterceptorFuncs(interceptor.Funcs{
					Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						countGateway(obj)
						obj.SetCreationTimestamp(metav1.Now())
						return cl.Create(ctx, obj, opts...)
					},
					Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
						countGateway(obj)
						return cl.Update(ctx, obj, opts...)
					},
				}).
				Build()
			strategy := downstreamclient.NewMappedNamespaceResourceStrategy("test", upstream, downstream)

			meta, err := strategy.ObjectMetaFromUpstreamObject(ctx, upstreamGateway)
			require.NoError(t, err)
			current := &gatewayv1.Gateway{ObjectMeta: meta}
			if tt.storedListeners != nil {
				stored := current.DeepCopy()
				stored.Annotations = tt.stored
				stored.Spec = gatewayv1.GatewaySpec{GatewayClassName: "test-suite", Listeners: tt.storedListeners}
				require.NoError(t, downstream.Create(ctx, stored))
				require.NoError(t, downstream.Get(ctx, client.ObjectKeyFromObject(stored), current))
			}

			writes = 0
			spec := gatewayv1.GatewaySpec{GatewayClassName: "test-suite", Listeners: tt.desiredListeners}
			require.NoError(t, writeDownstreamGateway(ctx, strategy, upstreamGateway, current, spec, tt.awaiting))
			assert.Equal(t, tt.wantWrites, writes, "downstream Gateway writes")

			var got gatewayv1.Gateway
			err = downstream.Get(ctx, client.ObjectKeyFromObject(current), &got)
			if tt.wantNoDownstream {
				assert.True(t, apierrors.IsNotFound(err))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantAnnotations, got.Annotations)
			assert.Equal(t, tt.wantListeners, got.Spec.Listeners)
		})
	}
}
