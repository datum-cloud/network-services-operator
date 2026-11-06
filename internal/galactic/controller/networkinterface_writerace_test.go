/*
Copyright © 2026 Datum Technology, Inc. All rights reserved.

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

package controller

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	cloudv1alpha1 "go.datum.net/galactic/api/cloud/v1alpha1"
	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// reconcileWithFailingInterfaceStatus reconciles a bound interface whose VPC
// does not exist yet, so the pass ends on an interface status write, and makes
// that write return statusErr.
func reconcileWithFailingInterfaceStatus(t *testing.T, statusErr error) (ctrl.Result, error) {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{cloudv1alpha1.AddToScheme, networkingv1alpha.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	iface := &networkingv1alpha.NetworkInterface{ObjectMeta: metav1.ObjectMeta{Name: "nic", Namespace: "tenant"}}
	iface.Status.Phase = networkingv1alpha.NetworkInterfacePhaseBound
	iface.Status.NetworkContextRef = &networkingv1alpha.LocalNetworkContextRef{Name: "vpc"}
	apimeta.SetStatusCondition(&iface.Status.Conditions, metav1.Condition{
		Type: networkingv1alpha.NetworkInterfaceAllocated, Status: metav1.ConditionTrue, Reason: "Allocated",
	})
	cl := interceptor.NewClient(
		fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(iface).WithObjects(iface).Build(),
		interceptor.Funcs{
			SubResourceUpdate: func(context.Context, client.Client, string, client.Object,
				...client.SubResourceUpdateOption,
			) error {
				return statusErr
			},
		})
	r := &NetworkInterfaceReconciler{Client: cl, APIReader: cl, Scheme: scheme}
	return r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(iface)})
}

var networkInterfaces = schema.GroupResource{Group: networkingv1alpha.GroupVersion.Group, Resource: "networkinterfaces"}

// Losing a status write to the claim controller or galactic is normal
// convergence: it retries without counting as a reconcile error.
func TestAStatusWriteConflictRetriesWithoutError(t *testing.T) {
	result, err := reconcileWithFailingInterfaceStatus(t, apierrors.NewConflict(networkInterfaces, "nic", nil))
	if err != nil {
		t.Fatalf("Reconcile error = %v, want nil", err)
	}
	if result != (ctrl.Result{Requeue: true}) {
		t.Fatalf("result = %+v, want a requeue", result)
	}
}

// An interface deleted after it was read has nothing left to report on.
func TestAnInterfaceDeletedMidPassIsNotAnError(t *testing.T) {
	if _, err := reconcileWithFailingInterfaceStatus(t, apierrors.NewNotFound(networkInterfaces, "nic")); err != nil {
		t.Fatalf("Reconcile error = %v, want nil", err)
	}
}
