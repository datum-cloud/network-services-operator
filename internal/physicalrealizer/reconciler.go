// SPDX-License-Identifier: AGPL-3.0-only

package physicalrealizer

import (
	"context"
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

// ControllerName is the name the realizer registers under.
const ControllerName = "physical-realizer"

// maxChangesPerPass bounds how often one pass changes a port again because
// its claims changed while it was changing it.
const maxChangesPerPass = 4

// Reconciler programs one switch port per SwitchPortHold.
type Reconciler struct {
	Client client.Client

	// APIReader reads holds, claims and interfaces. It must not be a cache: a
	// claim whose deletion the cache has not seen yet would be kept on its
	// port, and one whose hold entry the cache has not seen would be dropped.
	APIReader client.Reader

	Driver Driver
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=switchportholds,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaceclaims,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaceclaims/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaceclaims/finalizers,verbs=update
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkinterfaces/status,verbs=get;update;patch

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	hold := &networkingv1alpha.SwitchPortHold{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, hold); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	port := Port{Switch: hold.Spec.Switch, Name: hold.Spec.Port}

	carried, err := r.Driver.Read(ctx, port)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed reading switch port %s %s: %w", port.Switch, port.Name, err)
	}

	for range maxChangesPerPass {
		entries, err := r.observe(ctx, req.NamespacedName, hold)
		if err != nil {
			return ctrl.Result{}, err
		}
		desired := planFor(hold, entries).carried()
		if sameMemberships(carried, desired) {
			return ctrl.Result{}, r.settle(ctx, hold, entries, carried)
		}

		if err := r.withdraw(ctx, hold, entries, carried, desired); err != nil {
			return ctrl.Result{}, err
		}
		applyErr := r.Driver.Apply(ctx, port, desired)
		readBack, err := r.Driver.Read(ctx, port)
		if err != nil {
			return ctrl.Result{}, errors.Join(applyErr,
				fmt.Errorf("failed reading switch port %s %s back: %w", port.Switch, port.Name, err))
		}
		if applyErr != nil {
			return ctrl.Result{}, fmt.Errorf("failed changing switch port %s %s: %w", port.Switch, port.Name, applyErr)
		}
		if !sameMemberships(readBack, desired) {
			return ctrl.Result{}, fmt.Errorf("switch port %s %s reads back %v after a change to %v",
				port.Switch, port.Name, readBack, desired)
		}
		carried = readBack
	}
	return ctrl.Result{Requeue: true}, nil
}

// observe reads the hold and every claim it records again, from the API
// server. A hold that is gone records nothing, so its port carries nothing.
func (r *Reconciler) observe(
	ctx context.Context,
	key client.ObjectKey,
	hold *networkingv1alpha.SwitchPortHold,
) ([]recorded, error) {
	if err := r.APIReader.Get(ctx, key, hold); err != nil {
		if apierrors.IsNotFound(err) {
			hold.Spec.Boot = nil
			hold.Spec.NetworkClaims = nil
			return nil, nil
		}
		return nil, fmt.Errorf("failed reading switch port hold %s: %w", key.Name, err)
	}

	entries := make([]recorded, 0, len(hold.Spec.NetworkClaims)+1)
	if hold.Spec.Boot != nil {
		entries = append(entries, recorded{entry: *hold.Spec.Boot, boot: true})
	}
	for _, entry := range hold.Spec.NetworkClaims {
		entries = append(entries, recorded{entry: entry})
	}
	for i := range entries {
		claim := &networkingv1alpha.NetworkInterfaceClaim{}
		claimKey := client.ObjectKey{Namespace: entries[i].entry.Namespace, Name: entries[i].entry.Name}
		if err := r.APIReader.Get(ctx, claimKey, claim); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, fmt.Errorf("failed reading claim %s recorded on switch port hold %s: %w", claimKey, key.Name, err)
		}
		entries[i].claim = claim
	}
	return entries, nil
}

// withdraw reports every claim the change touches as not programmed before
// the port moves, so none reports a port that is between states.
func (r *Reconciler) withdraw(
	ctx context.Context,
	hold *networkingv1alpha.SwitchPortHold,
	entries []recorded,
	current, desired []Membership,
) error {
	for _, entry := range entries {
		if !entry.onPort(hold) || !entry.changes(current, desired) {
			continue
		}
		condition := metav1.Condition{
			Type:    networkingv1alpha.NetworkInterfaceClaimProgrammed,
			Status:  metav1.ConditionFalse,
			Reason:  networkingv1alpha.NetworkInterfaceClaimReasonPortChanging,
			Message: "The switch port is changing and is not confirmed to carry this network",
		}
		if err := r.report(ctx, entry.claim, nil, false, condition); err != nil {
			return fmt.Errorf("failed withdrawing claim %s/%s before changing its port: %w",
				entry.claim.Namespace, entry.claim.Name, err)
		}
	}
	return nil
}

// settle runs once the port is read back carrying what its hold asks for.
func (r *Reconciler) settle(
	ctx context.Context,
	hold *networkingv1alpha.SwitchPortHold,
	entries []recorded,
	carried []Membership,
) error {
	for _, entry := range entries {
		if !entry.releasing(hold) || entry.carriedOn(carried) {
			continue
		}
		claim := entry.claim
		controllerutil.RemoveFinalizer(claim, networkingv1alpha.NetworkInterfaceClaimDetachFinalizer)
		if err := r.Client.Update(ctx, claim); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed removing the detach finalizer from claim %s/%s: %w", claim.Namespace, claim.Name, err)
		}
	}

	p := planFor(hold, entries)
	if p.boot != nil {
		if err := r.report(ctx, p.boot.claim, nil, true, carriesNetwork()); err != nil {
			return err
		}
	}
	for _, network := range p.networks {
		programmed := carriesNetwork()
		if p.boot != nil {
			programmed = metav1.Condition{
				Type:    networkingv1alpha.NetworkInterfaceClaimProgrammed,
				Status:  metav1.ConditionFalse,
				Reason:  networkingv1alpha.NetworkInterfaceClaimReasonBootAttachmentActive,
				Message: "A boot claim holds the switch port, so the port carries the boot network instead",
			}
		}
		var vlan *int32
		if network.membership.VLAN != 0 {
			vlan = &network.membership.VLAN
		}
		if err := r.report(ctx, network.claim, vlan, true, programmed); err != nil {
			return err
		}
	}
	return nil
}

func carriesNetwork() metav1.Condition {
	return metav1.Condition{
		Type:    networkingv1alpha.NetworkInterfaceClaimProgrammed,
		Status:  metav1.ConditionTrue,
		Reason:  networkingv1alpha.NetworkInterfaceClaimReasonPortCarriesNetwork,
		Message: "The switch port carries this network, as read back from the switch",
	}
}

func prepared() metav1.Condition {
	return metav1.Condition{
		Type:    networkingv1alpha.NetworkInterfaceClaimPrepared,
		Status:  metav1.ConditionTrue,
		Reason:  networkingv1alpha.NetworkInterfaceClaimReasonAttachmentPointAccepted,
		Message: "The switch port is accepted and its segment can be built",
	}
}

// report writes programmed, and with settled also Prepared and the VLAN, on
// the claim and on the interface bound to it. The interface goes first, so a
// provider reading the claim never sees more than the interface says.
func (r *Reconciler) report(
	ctx context.Context,
	claim *networkingv1alpha.NetworkInterfaceClaim,
	vlan *int32,
	settled bool,
	programmed metav1.Condition,
) error {
	if ref := claim.Status.NetworkInterfaceRef; ref != nil {
		iface := &networkingv1alpha.NetworkInterface{}
		err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: claim.Namespace, Name: ref.Name}, iface)
		switch {
		case apierrors.IsNotFound(err):
		case err != nil:
			return fmt.Errorf("failed reading interface %s/%s: %w", claim.Namespace, ref.Name, err)
		case iface.Spec.ClaimRef != nil && iface.Spec.ClaimRef.Name == claim.Name:
			changed := false
			if settled {
				changed = setVLAN(&iface.Status.VLANID, vlan)
				changed = setCondition(&iface.Status.Conditions, prepared(), iface.Generation) || changed
			}
			changed = setCondition(&iface.Status.Conditions, programmed, iface.Generation) || changed
			if changed {
				if err := r.Client.Status().Update(ctx, iface); err != nil && !apierrors.IsNotFound(err) {
					return fmt.Errorf("failed updating interface %s/%s status: %w", iface.Namespace, iface.Name, err)
				}
			}
		}
	}

	changed := false
	if settled {
		changed = setVLAN(&claim.Status.VLANID, vlan)
		changed = setCondition(&claim.Status.Conditions, prepared(), claim.Generation) || changed
	}
	changed = setCondition(&claim.Status.Conditions, programmed, claim.Generation) || changed
	if !changed {
		return nil
	}
	if err := r.Client.Status().Update(ctx, claim); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed updating claim %s/%s status: %w", claim.Namespace, claim.Name, err)
	}
	return nil
}

func setCondition(conditions *[]metav1.Condition, condition metav1.Condition, generation int64) bool {
	condition.ObservedGeneration = generation
	return apimeta.SetStatusCondition(conditions, condition)
}

func setVLAN(field **int32, vlan *int32) bool {
	if (*field == nil) == (vlan == nil) && (vlan == nil || **field == *vlan) {
		return false
	}
	if vlan == nil {
		*field = nil
		return true
	}
	value := *vlan
	*field = &value
	return true
}

func holdForAttachmentPoint(point *networkingv1alpha.NetworkInterfaceAttachmentPoint) []ctrl.Request {
	if point == nil {
		return nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{
		Name: networkingv1alpha.SwitchPortHoldName(point.Switch, point.Port),
	}}}
}

func holdForClaim(_ context.Context, obj client.Object) []ctrl.Request {
	claim, ok := obj.(*networkingv1alpha.NetworkInterfaceClaim)
	if !ok || claim.Spec.AttachmentMode != networkingv1alpha.NetworkInterfaceAttachmentModePhysical {
		return nil
	}
	return holdForAttachmentPoint(claim.Spec.AttachmentPoint)
}

func holdForInterface(_ context.Context, obj client.Object) []ctrl.Request {
	iface, ok := obj.(*networkingv1alpha.NetworkInterface)
	if !ok || iface.Spec.AttachmentMode != networkingv1alpha.NetworkInterfaceAttachmentModePhysical {
		return nil
	}
	return holdForAttachmentPoint(iface.Spec.AttachmentPoint)
}

// SetupWithManager registers the realizer. A claim being deleted or bound,
// and an interface being bound, wakes the hold of the port they name.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Driver == nil {
		return errors.New("a switch driver is required")
	}
	if r.Client == nil {
		r.Client = mgr.GetClient()
	}
	if r.APIReader == nil {
		r.APIReader = mgr.GetAPIReader()
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1alpha.SwitchPortHold{}).
		Watches(&networkingv1alpha.NetworkInterfaceClaim{}, handler.EnqueueRequestsFromMapFunc(holdForClaim)).
		Watches(&networkingv1alpha.NetworkInterface{}, handler.EnqueueRequestsFromMapFunc(holdForInterface)).
		Named(ControllerName).
		Complete(r)
}
