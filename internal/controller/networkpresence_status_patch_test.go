// SPDX-License-Identifier: AGPL-3.0-only
package controller

import (
	"context"
	"github.com/stretchr/testify/require"
	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

func TestPresenceReportPreservesConcurrentMetadata(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))
	binding := &networkingv1alpha.NetworkBinding{ObjectMeta: metav1.ObjectMeta{Name: "binding", Namespace: "default", Generation: 1}}
	hub := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(binding).WithObjects(binding).Build()
	require.NoError(t, hub.Get(ctx, client.ObjectKeyFromObject(binding), binding))
	stale := binding.DeepCopy()
	binding.Labels = map[string]string{"consumer": "updated"}
	require.NoError(t, hub.Update(ctx, binding))
	r := &NetworkPresenceReconciler{hub: hub}
	require.NoError(t, r.report(ctx, []networkingv1alpha.NetworkBinding{*stale}, nil, refusal(networkingv1alpha.NetworkBindingReasonPending, "waiting")))
	require.NoError(t, hub.Get(ctx, client.ObjectKeyFromObject(binding), binding))
	require.Equal(t, "updated", binding.Labels["consumer"])
	require.Len(t, binding.Status.Conditions, 1)
	require.Equal(t, networkingv1alpha.NetworkBindingReasonPending, binding.Status.Conditions[0].Reason)
	require.Equal(t, int64(1), binding.Status.Conditions[0].ObservedGeneration)
}
