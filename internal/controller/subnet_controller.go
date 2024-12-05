// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// SubnetReconciler reconciles a Subnet object
type SubnetReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=subnets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=subnets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=subnets/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Subnet object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/reconcile
func (r *SubnetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var subnet networkingv1alpha.Subnet
	if err := r.Client.Get(ctx, req.NamespacedName, &subnet); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !subnet.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	logger.Info("reconciling subnet")
	defer logger.Info("reconcile complete")

	// TODO(jreese) finalizer work

	var networkContext networkingv1alpha.NetworkContext
	networkContextObjectKey := client.ObjectKey{
		Namespace: subnet.Namespace,
		Name:      subnet.Spec.NetworkContext.Name,
	}
	if err := r.Client.Get(ctx, networkContextObjectKey, &networkContext); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed fetching network context: %w", err)
	}

	var location networkingv1alpha.Location
	locationObjectKey := client.ObjectKey{
		Namespace: networkContext.Spec.Location.Namespace,
		Name:      networkContext.Spec.Location.Name,
	}
	if err := r.Client.Get(ctx, locationObjectKey, &location); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed fetching network context location: %w", err)
	}

	// TODO(jreese) get topology key from well known package
	cityCode, ok := location.Spec.Topology["topology.datum.net/city-code"]
	if !ok {
		return ctrl.Result{}, fmt.Errorf("unable to find topology key: topology.datum.net/city-code")
	}

	// TODO(jreese) move to proper higher level subnet allocation logic, this is
	// for the rough POC! Pay attention to the subnet class, etc.
	//
	// GCP allocates a /20 per region. Distribution seems to be as new regions
	// come online, a /20 is allocated, but there appears to be at least a /15
	// between each region's /20. For example:
	//
	// 	europe-west9      10.200.0.0/20
	// 	us-east5          10.202.0.0/20
	// 	europe-southwest1 10.204.0.0/20
	// 	us-south1         10.206.0.0/20
	// 	me-west1          10.208.0.0/20
	//
	// There's a few scenarios where this isn't the case.

	var startAddress string
	switch cityCode {
	case "DFW":
		startAddress = "10.128.0.0"
	case "DLS":
		startAddress = "10.130.0.0"
	case "LHR":
		startAddress = "10.132.0.0"
	}

	subnet.Status.StartAddress = proto.String(startAddress)
	subnet.Status.PrefixLength = proto.Int32(20)

	apimeta.SetStatusCondition(&subnet.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "PrefixAllocated",
		ObservedGeneration: subnet.Generation,
		Message:            "Subnet has been allocated a prefix",
	})

	if err := r.Client.Status().Update(ctx, &subnet); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed updating subnet status")
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SubnetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1alpha.Subnet{}, builder.WithPredicates(
			predicate.NewPredicateFuncs(func(object client.Object) bool {
				// Don't bother processing subnets that have been allocated and are not
				// deleting
				o := object.(*networkingv1alpha.Subnet)
				return o.Status.StartAddress == nil || !o.DeletionTimestamp.IsZero()
			}),
		)).
		Named("subnet").
		Complete(r)
}
