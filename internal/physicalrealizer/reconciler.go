// SPDX-License-Identifier: AGPL-3.0-only

package physicalrealizer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"go.miloapis.com/instrumentation/labels"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	ControllerName = "physical-realizer"

	SweepInterval = 30 * time.Second

	maxChangesPerPass = 4
)

// SweepRequest asks the realizer to clear every port no hold names. Holds are
// cluster-scoped, so a namespaced key never names one.
var SweepRequest = ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ControllerName, Name: "sweep"}}

// Reconciler programs one switch port per SwitchPortHold. It must run with a
// single worker: it is the only writer of what each port carries, and the
// sweep and the per-hold passes must never change one port at once.
type Reconciler struct {
	Client client.Client

	// APIReader must not be a cache. A deletion the cache has not seen would
	// keep a network on its port, and a hold it has not seen would be swept.
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
	if req == SweepRequest {
		return ctrl.Result{RequeueAfter: SweepInterval}, r.sweep(ctx)
	}

	hold := &networkingv1alpha.SwitchPortHold{}
	if err := r.APIReader.Get(ctx, req.NamespacedName, hold); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, r.sweep(ctx)
		}
		return ctrl.Result{}, err
	}
	port := Port{Switch: hold.Spec.Switch, Name: hold.Spec.Port}
	logger := log.FromContext(ctx, "switch", port.Switch, "port", port.Name)

	if !namesItsPort(hold) {
		logger.Error(nil, "refusing a switch port hold whose name is not its port's",
			labels.ResourceName, hold.Name, "expected", networkingv1alpha.SwitchPortHoldName(port.Switch, port.Name))
		return ctrl.Result{}, nil
	}

	carried, err := r.Driver.Read(ctx, port)
	if err != nil {
		logger.Error(err, "failed reading switch port")
		return ctrl.Result{}, fmt.Errorf("failed reading switch port %s %s: %w", port.Switch, port.Name, err)
	}

	for range maxChangesPerPass {
		entries, gone, err := r.observe(ctx, req.NamespacedName, hold)
		if err != nil {
			return ctrl.Result{}, err
		}
		if gone {
			return ctrl.Result{}, r.sweep(ctx)
		}
		desired := planFor(hold, entries).carried()
		if sameMemberships(carried, desired) {
			return ctrl.Result{}, r.settle(ctx, logger, hold, entries, carried)
		}

		if err := r.withdraw(ctx, hold, entries, carried, desired); err != nil {
			return ctrl.Result{}, err
		}
		if carried, err = r.change(ctx, logger, port, desired); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{Requeue: true}, nil
}

func namesItsPort(hold *networkingv1alpha.SwitchPortHold) bool {
	return hold.Name == networkingv1alpha.SwitchPortHoldName(hold.Spec.Switch, hold.Spec.Port)
}

func (r *Reconciler) change(ctx context.Context, logger logr.Logger, port Port, desired []Membership) ([]Membership, error) {
	applyErr := r.Driver.Apply(ctx, port, desired)
	if applyErr != nil {
		logger.Error(applyErr, "failed changing switch port")
	}
	readBack, err := r.Driver.Read(ctx, port)
	if err != nil {
		logger.Error(err, "failed reading switch port back")
		return nil, errors.Join(applyErr,
			fmt.Errorf("failed reading switch port %s %s back: %w", port.Switch, port.Name, err))
	}
	if applyErr != nil {
		return nil, fmt.Errorf("failed changing switch port %s %s: %w", port.Switch, port.Name, applyErr)
	}
	if !sameMemberships(readBack, desired) {
		logger.Error(nil, "switch port reads back other than what was applied", "applied", desired, "read", readBack)
		return nil, fmt.Errorf("switch port %s %s reads back %v after a change to %v",
			port.Switch, port.Name, readBack, desired)
	}
	return readBack, nil
}

func (r *Reconciler) sweep(ctx context.Context) error {
	ports, err := r.Driver.Ports(ctx)
	if err != nil {
		log.FromContext(ctx).Error(err, "failed listing switch ports to sweep")
		return fmt.Errorf("failed listing switch ports: %w", err)
	}
	if len(ports) == 0 {
		return nil
	}

	var holds networkingv1alpha.SwitchPortHoldList
	if err := r.APIReader.List(ctx, &holds); err != nil {
		return fmt.Errorf("failed listing switch port holds: %w", err)
	}
	named := map[Port]bool{}
	for i := range holds.Items {
		if hold := &holds.Items[i]; namesItsPort(hold) {
			named[Port{Switch: hold.Spec.Switch, Name: hold.Spec.Port}] = true
		}
	}

	var claims networkingv1alpha.NetworkInterfaceClaimList
	if err := r.APIReader.List(ctx, &claims); err != nil {
		return fmt.Errorf("failed listing claims: %w", err)
	}

	var errs []error
	for _, port := range ports {
		if named[port] {
			continue
		}
		logger := log.FromContext(ctx, "switch", port.Switch, "port", port.Name)
		if err := r.withdrawPort(ctx, claims.Items, port); err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := r.change(ctx, logger, port, nil); err != nil {
			errs = append(errs, err)
			continue
		}
		logger.Info("cleared a switch port no hold names", labels.Outcome, "cleared")
	}
	return errors.Join(errs...)
}

func (r *Reconciler) withdrawPort(ctx context.Context, claims []networkingv1alpha.NetworkInterfaceClaim, port Port) error {
	for i := range claims {
		claim := &claims[i]
		point := claim.Spec.AttachmentPoint
		if claim.Spec.AttachmentMode != networkingv1alpha.NetworkInterfaceAttachmentModePhysical ||
			point == nil || point.Switch != port.Switch || point.Port != port.Name {
			continue
		}
		if err := r.report(ctx, claim, nil, false, portChanging()); err != nil {
			return fmt.Errorf("failed withdrawing claim %s/%s before clearing its port: %w", claim.Namespace, claim.Name, err)
		}
	}
	return nil
}

func (r *Reconciler) observe(
	ctx context.Context,
	key client.ObjectKey,
	hold *networkingv1alpha.SwitchPortHold,
) ([]recorded, bool, error) {
	if err := r.APIReader.Get(ctx, key, hold); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("failed reading switch port hold %s: %w", key.Name, err)
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
			return nil, false, fmt.Errorf("failed reading claim %s recorded on switch port hold %s: %w", claimKey, key.Name, err)
		}
		entries[i].claim = claim
	}
	return entries, false, nil
}

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
		if err := r.report(ctx, entry.claim, nil, false, portChanging()); err != nil {
			return fmt.Errorf("failed withdrawing claim %s/%s before changing its port: %w",
				entry.claim.Namespace, entry.claim.Name, err)
		}
	}
	return nil
}

func (r *Reconciler) settle(
	ctx context.Context,
	logger logr.Logger,
	hold *networkingv1alpha.SwitchPortHold,
	entries []recorded,
	carried []Membership,
) error {
	for _, entry := range entries {
		if !entry.releasing(hold) {
			continue
		}
		claim := entry.claim
		if entry.carriedOn(carried) {
			logger.Info("keeping the detach finalizer while the port carries the claim's network",
				labels.ResourceNamespace, claim.Namespace, labels.ResourceName, claim.Name)
			continue
		}
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
		vlan := network.membership.VLAN
		if err := r.report(ctx, network.claim, &vlan, true, programmed); err != nil {
			return err
		}
	}
	return nil
}

func portChanging() metav1.Condition {
	return metav1.Condition{
		Type:    networkingv1alpha.NetworkInterfaceClaimProgrammed,
		Status:  metav1.ConditionFalse,
		Reason:  networkingv1alpha.NetworkInterfaceClaimReasonPortChanging,
		Message: "The switch port is changing and is not confirmed to carry this network",
	}
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

// The interface is written first, so the claim never says more than it.
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

func startSweeping(_ context.Context, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) error {
	queue.Add(SweepRequest)
	return nil
}

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
		WatchesRawSource(source.Func(startSweeping)).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Named(ControllerName).
		Complete(r)
}
