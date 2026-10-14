// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func resultTestGateway(programmed metav1.ConditionStatus) *gatewayv1.Gateway {
	gw := &gatewayv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "gw", Generation: 1}}
	apimeta.SetStatusCondition(&gw.Status.Conditions, metav1.Condition{
		Type:               string(gatewayv1.GatewayConditionProgrammed),
		Status:             programmed,
		Reason:             string(gatewayv1.GatewayReasonProgrammed),
		ObservedGeneration: 1,
		LastTransitionTime: metav1.NewTime(time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)),
	})
	return gw
}

func TestResultCompleteWritesOnlyAChangedStatus(t *testing.T) {
	tests := []struct {
		name       string
		stored     *gatewayv1.Gateway
		desired    *gatewayv1.Gateway
		updateErr  error
		wantWrites int
		wantErr    bool
		wantResult time.Duration
	}{
		{
			name:    "unchanged: not written",
			stored:  resultTestGateway(metav1.ConditionTrue),
			desired: resultTestGateway(metav1.ConditionTrue),
		},
		{
			name:       "changed: written",
			stored:     resultTestGateway(metav1.ConditionFalse),
			desired:    resultTestGateway(metav1.ConditionTrue),
			wantWrites: 1,
		},
		{
			name:       "not stored: written, its error returned",
			desired:    resultTestGateway(metav1.ConditionTrue),
			wantWrites: 1,
			wantErr:    true,
		},
		{
			name:       "a conflict: requeued",
			stored:     resultTestGateway(metav1.ConditionFalse),
			desired:    resultTestGateway(metav1.ConditionTrue),
			updateErr:  apierrors.NewConflict(schema.GroupResource{Group: gatewayv1.GroupName, Resource: "gateways"}, "gw", nil),
			wantWrites: 1,
			wantResult: time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testScheme := runtime.NewScheme()
			require.NoError(t, scheme.AddToScheme(testScheme))
			require.NoError(t, gatewayv1.Install(testScheme))

			builder := fake.NewClientBuilder().WithScheme(testScheme).WithStatusSubresource(&gatewayv1.Gateway{})
			if tt.stored != nil {
				builder = builder.WithObjects(tt.stored)
			}
			writes := 0
			cl := builder.WithInterceptorFuncs(interceptor.Funcs{
				SubResourceUpdate: func(ctx context.Context, c client.Client, subResource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
					writes++
					if tt.updateErr != nil {
						return tt.updateErr
					}
					return c.SubResource(subResource).Update(ctx, obj, opts...)
				},
			}).Build()

			if tt.stored != nil {
				var stored gatewayv1.Gateway
				require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(tt.desired), &stored))
				tt.desired.ResourceVersion = stored.ResourceVersion
			}

			var result Result
			result.AddStatusUpdate(cl, tt.desired)
			res, err := result.Complete(context.Background())

			assert.Equal(t, tt.wantWrites, writes, "status writes")
			assert.Equal(t, tt.wantErr, err != nil, "error: %v", err)
			assert.Equal(t, tt.wantResult, res.RequeueAfter)
		})
	}
}

func TestResultCompletePatchesOnlyTheStatus(t *testing.T) {
	for _, tt := range []struct {
		name        string
		stored      *gatewayv1.Gateway
		wantPatches int
	}{
		{"unchanged: not written", resultTestGateway(metav1.ConditionTrue), 0},
		{"changed: patched", resultTestGateway(metav1.ConditionFalse), 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			testScheme := runtime.NewScheme()
			require.NoError(t, scheme.AddToScheme(testScheme))
			require.NoError(t, gatewayv1.Install(testScheme))
			var patches []map[string]any
			updates := 0
			cl := fake.NewClientBuilder().WithScheme(testScheme).WithStatusSubresource(&gatewayv1.Gateway{}).
				WithObjects(tt.stored).
				WithInterceptorFuncs(interceptor.Funcs{
					SubResourcePatch: func(ctx context.Context, c client.Client, subResource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
						data, err := patch.Data(obj)
						require.NoError(t, err)
						var body map[string]any
						require.NoError(t, json.Unmarshal(data, &body))
						patches = append(patches, body)
						return c.SubResource(subResource).Patch(ctx, obj, patch, opts...)
					},
					SubResourceUpdate: func(ctx context.Context, c client.Client, subResource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						updates++
						return c.SubResource(subResource).Update(ctx, obj, opts...)
					},
				}).Build()

			desired := resultTestGateway(metav1.ConditionTrue)
			desired.ResourceVersion = "stale"
			var result Result
			result.AddStatusPatch(cl, desired, "network-services/test")
			_, err := result.Complete(context.Background())

			require.NoError(t, err)
			assert.Zero(t, updates)
			require.Len(t, patches, tt.wantPatches)
			for _, body := range patches {
				assert.Equal(t, []string{"status"}, slices.Collect(maps.Keys(body)), "only the status is sent, without resourceVersion")
			}
			stored := &gatewayv1.Gateway{}
			require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(desired), stored))
			assert.True(t, apimeta.IsStatusConditionTrue(stored.Status.Conditions, string(gatewayv1.GatewayConditionProgrammed)))
		})
	}
}
