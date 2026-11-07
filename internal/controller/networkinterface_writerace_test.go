// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// An interface deleted after the cache last saw it needs no finalizer, so
// failing to add one is not an error.
func TestAddingAFinalizerToADeletedInterfaceIsNotAnError(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, networkingv1alpha.AddToScheme(scheme))
	iface := &networkingv1alpha.NetworkInterface{ObjectMeta: metav1.ObjectMeta{Name: "nic", Namespace: "tenant"}}
	cl := interceptor.NewClient(fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(iface).Build(),
		interceptor.Funcs{
			Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
				return apierrors.NewNotFound(schema.GroupResource{
					Group: networkingv1alpha.GroupVersion.Group, Resource: "networkinterfaces",
				}, "nic")
			},
		})

	r := &NetworkInterfaceReconciler{}
	require.NoError(t, r.reconcileInterface(context.Background(), cl, client.ObjectKeyFromObject(iface)))
}
