// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/nlbhealth"
)

const probesPerHealthObject = 8

var cellProbeSlots = make(chan struct{}, 32)

type NetworkLoadBalancerBackendHealthReconciler struct {
	Client client.Client
	Prober nlbhealth.Prober
	Now    func() time.Time
}

// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancerbackendhealths,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.datumapis.com,resources=networkloadbalancerbackendhealths/status,verbs=get;update;patch

func (r *NetworkLoadBalancerBackendHealthReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	var health networkingv1alpha.NetworkLoadBalancerBackendHealth
	if err := r.Client.Get(ctx, req.NamespacedName, &health); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !health.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	if len(health.Spec.Targets) > 64 {
		return ctrl.Result{}, fmt.Errorf("health object %s has too many targets", req.NamespacedName)
	}

	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	previous := make(map[string]*networkingv1alpha.NetworkLoadBalancerBackendHealthTargetStatus, len(health.Status.Targets))
	for i := range health.Status.Targets {
		previous[health.Status.Targets[i].Name] = &health.Status.Targets[i]
	}
	statuses := make([]networkingv1alpha.NetworkLoadBalancerBackendHealthTargetStatus, len(health.Spec.Targets))
	localSlots := make(chan struct{}, probesPerHealthObject)
	var workers sync.WaitGroup
	for i := range health.Spec.Targets {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			target := health.Spec.Targets[i]
			probeCtx, cancel := context.WithTimeout(ctx, nlbhealth.ProbeTimeout)
			defer cancel()
			var probeErr error
			if health.Spec.RouteRef.UID == "" || health.Spec.RouteRef.Generation < 1 ||
				health.Spec.ParentRef.UID == "" || health.Spec.ParentRef.Generation < 1 ||
				target.SourceUID == "" || target.AttachmentUID == "" {
				probeErr = errors.New("source identity is incomplete")
			} else {
				probeErr = r.validateLiveTarget(probeCtx, &health, target)
			}
			if probeErr == nil {
				probeErr = takeProbeSlot(probeCtx, localSlots)
			}
			if probeErr == nil {
				defer func() { <-localSlots }()
				if probeErr = takeProbeSlot(probeCtx, cellProbeSlots); probeErr == nil {
					defer func() { <-cellProbeSlots }()
					probeErr = r.Prober.Probe(probeCtx, health.Spec.FabricVPCID, target)
				}
			}
			statuses[i] = nlbhealth.NextTargetStatus(&health, target, previous[target.Name], probeErr, now())
		}(i)
	}
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return ctrl.Result{}, err
	}

	wanted := networkingv1alpha.NetworkLoadBalancerBackendHealthStatus{
		Conditions: slices.Clone(health.Status.Conditions), Targets: statuses,
	}
	condition := metav1.Condition{
		Type: "ProbesEvaluated", Status: metav1.ConditionTrue, Reason: "Evaluated",
		Message:            fmt.Sprintf("Evaluated %d targets from the cell network path", len(statuses)),
		ObservedGeneration: health.Generation,
	}
	if r.Prober.DialContext == nil {
		condition.Status = metav1.ConditionFalse
		condition.Reason = "NoDataPlaneDialer"
		condition.Message = "No VPC-aware probe dialer is configured"
	}
	apimeta.SetStatusCondition(&wanted.Conditions, condition)
	if !equality.Semantic.DeepEqual(health.Status, wanted) {
		health.Status = wanted
		if err := r.Client.Status().Update(ctx, &health); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, fmt.Errorf("update backend health status: %w", err)
		}
	}
	return ctrl.Result{RequeueAfter: nlbhealth.ProbeInterval}, nil
}

func (r *NetworkLoadBalancerBackendHealthReconciler) validateLiveTarget(
	ctx context.Context,
	health *networkingv1alpha.NetworkLoadBalancerBackendHealth,
	target networkingv1alpha.NetworkLoadBalancerBackendHealthTarget,
) error {
	var iface networkingv1alpha.NetworkInterface
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: target.SourceNamespace, Name: target.SourceInterface}, &iface); err != nil {
		return fmt.Errorf("read source interface: %w", err)
	}
	if iface.UID != target.SourceUID || iface.Labels[networkingv1alpha.NetworkInterfaceProjectionLabel] == "true" ||
		iface.Spec.Network.Name != health.Spec.NetworkName ||
		iface.Labels[networkingv1alpha.NetworkInterfaceLocationLabel] != health.Spec.Location ||
		iface.Status.Phase != networkingv1alpha.NetworkInterfacePhaseBound || !isServiceMemberHealthy(&iface) ||
		iface.Status.VPC != health.Spec.FabricVPCID || iface.Status.AttachmentRef == nil ||
		iface.Status.AttachmentRef.Name != target.AttachmentName {
		return errors.New("source interface no longer matches this target")
	}
	if !slices.ContainsFunc(iface.Spec.Addresses, func(a networkingv1alpha.NetworkInterfaceAddress) bool {
		address, _, _ := strings.Cut(a.Address, "/")
		return address == target.Address
	}) {
		return errors.New("source interface no longer holds this address")
	}
	return nil
}

func takeProbeSlot(ctx context.Context, slots chan struct{}) error {
	select {
	case slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *NetworkLoadBalancerBackendHealthReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Prober.DialContext == nil {
		return errors.New("VPC-aware probe dialer is required")
	}
	r.Client = mgr.GetClient()
	return ctrl.NewControllerManagedBy(mgr).
		For(&networkingv1alpha.NetworkLoadBalancerBackendHealth{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 4}).
		Named("networkloadbalancerbackendhealth").
		Complete(r)
}
