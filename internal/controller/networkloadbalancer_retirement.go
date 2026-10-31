// SPDX-License-Identifier: AGPL-3.0-only
package controller

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	galactic "go.datum.net/galactic/api/network/v1alpha1"
	api "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/downstreamclient"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func allocationAuthorityIntent(project, namespace string, parent *api.NetworkLoadBalancer, endpoint *api.NetworkLoadBalancerEndpointStatus, address *api.NetworkLoadBalancerAddressStatus) *api.NetworkLoadBalancerCellIntent {
	uid := networkLoadBalancerAllocationUID(string(parent.UID), endpoint.RequestName, endpoint.Location, address.ClaimRef.UID)
	return &api.NetworkLoadBalancerCellIntent{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "nlb-allocation-" + uid, Labels: map[string]string{
		downstreamclient.UpstreamOwnerClusterNameLabel: "cluster-" + strings.ReplaceAll(project, "/", "_"), downstreamclient.UpstreamOwnerNamespaceLabel: parent.Namespace, api.LocationLabel: endpoint.Location,
	}}, Spec: api.NetworkLoadBalancerCellIntentSpec{
		Allocation: &api.NetworkLoadBalancerAllocationAuthority{UID: uid, ClaimUID: address.ClaimRef.UID, Retired: address.Retiring},
		Source:     api.NetworkLoadBalancerCellIntentSource{ProjectCluster: project, Namespace: parent.Namespace, ParentName: parent.Name, ParentUID: string(parent.UID), ParentGeneration: parent.Generation, AddressRequest: endpoint.RequestName},
		Location:   endpoint.Location, NetworkName: parent.Spec.NetworkRef.Name, VIPFamily: address.Family, VIP: address.Value, State: api.NetworkLoadBalancerCellIntentWithdrawn,
	}}
}

func (r *NetworkLoadBalancerCellIntentReconciler) publishAllocationAuthorities(ctx context.Context, projectClient, hub client.Client, project string, parent *api.NetworkLoadBalancer) (bool, error) {
	pending, namespace := false, ""
	for i := range parent.Status.Endpoints {
		endpoint := &parent.Status.Endpoints[i]
		for j := range endpoint.Addresses {
			address := &endpoint.Addresses[j]
			if !address.Coordinated || !address.WithdrawalPending {
				continue
			}
			pending = pending || address.Retiring
			if address.ClaimRef == nil || address.ClaimRef.UID == "" || address.Value == "" {
				return true, fmt.Errorf("coordinated allocation lacks a claim identity")
			}
			if namespace == "" {
				strategy := downstreamclient.NewMappedNamespaceResourceStrategy(project, projectClient, hub)
				var err error
				namespace, err = strategy.GetDownstreamNamespaceNameForUpstreamNamespace(ctx, parent.Namespace)
				if apierrors.IsNotFound(err) {
					return true, nil
				}
				if err != nil {
					return true, err
				}
			}
			wanted := allocationAuthorityIntent(project, namespace, parent, endpoint, address)
			var current api.NetworkLoadBalancerCellIntent
			if err := hub.Get(ctx, client.ObjectKeyFromObject(wanted), &current); apierrors.IsNotFound(err) {
				if err := hub.Create(ctx, wanted); err != nil && !apierrors.IsAlreadyExists(err) {
					return true, err
				}
			} else if err != nil {
				return true, err
			} else if current.Spec.Allocation == nil || current.Spec.Allocation.UID != wanted.Spec.Allocation.UID || current.Spec.Allocation.ClaimUID != wanted.Spec.Allocation.ClaimUID || current.Spec.VIP != wanted.Spec.VIP || current.Spec.Location != wanted.Spec.Location || current.Spec.Source.ParentUID != wanted.Spec.Source.ParentUID {
				return true, fmt.Errorf("allocation authority %s has conflicting identity", wanted.Name)
			} else if address.Retiring && !current.Spec.Allocation.Retired {
				before := current.DeepCopy()
				current.Spec.Allocation.Retired = true
				if err := hub.Patch(ctx, &current, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
					return true, err
				}
			}
		}
	}
	return pending, nil
}

func allocationAuthorityRetired(intent *api.NetworkLoadBalancerCellIntent) bool {
	authority := intent.Spec.Allocation
	if authority == nil || !authority.Retired {
		return false
	}
	for _, uid := range authority.Participants {
		if !slices.ContainsFunc(intent.Status.AllocationRetirements, func(receipt api.NetworkLoadBalancerAllocationRetirementReceipt) bool {
			return receipt.CellUID == uid && receipt.ObservedGeneration == intent.Generation
		}) {
			return false
		}
	}
	return true
}

func allocationRetirementConfirmed(parent *api.NetworkLoadBalancer, endpoint *api.NetworkLoadBalancerEndpointStatus, address *api.NetworkLoadBalancerAddressStatus, intents []api.NetworkLoadBalancerCellIntent) bool {
	if address.ClaimRef == nil || address.ClaimRef.UID == "" {
		return false
	}
	uid := networkLoadBalancerAllocationUID(string(parent.UID), endpoint.RequestName, endpoint.Location, address.ClaimRef.UID)
	for i := range intents {
		intent := &intents[i]
		if intent.Spec.Allocation != nil && intent.Spec.Allocation.UID == uid && intent.Spec.Allocation.ClaimUID == address.ClaimRef.UID && intent.Spec.VIP == address.Value && intent.Spec.Location == endpoint.Location && allocationAuthorityRetired(intent) {
			return true
		}
	}
	return false
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) hubAuthorityClient() (client.Client, error) {
	if r.HubCluster == nil || r.HubCluster.GetAPIReader() == nil {
		return nil, fmt.Errorf("authoritative federation hub client is unavailable")
	}
	return nlbAuthoritativeClient(r.HubCluster.GetClient(), r.HubCluster.GetAPIReader()), nil
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) cellIdentity(ctx context.Context) (string, error) {
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	var namespace corev1.Namespace
	if err := reader.Get(ctx, client.ObjectKey{Name: "kube-system"}, &namespace); err != nil {
		return "", fmt.Errorf("read stable cell identity: %w", err)
	}
	if namespace.UID == "" {
		return "", fmt.Errorf("cell identity namespace has no UID")
	}
	return string(namespace.UID), nil
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) registerAllocationParticipant(ctx context.Context, namespace, uid, vip string) error {
	hub, err := r.hubAuthorityClient()
	if err != nil {
		return err
	}
	var authority api.NetworkLoadBalancerCellIntent
	if err := hub.Get(ctx, client.ObjectKey{Namespace: namespace, Name: "nlb-allocation-" + uid}, &authority); err != nil {
		return fmt.Errorf("read allocation authority: %w", err)
	}
	authorityVIP, addressErr := netip.ParseAddr(authority.Spec.VIP)
	requestedVIP, requestedErr := netip.ParseAddr(vip)
	if authority.Spec.Allocation == nil || authority.Spec.Allocation.UID != uid || addressErr != nil || requestedErr != nil || authorityVIP != requestedVIP {
		return fmt.Errorf("allocation authority has conflicting identity")
	}
	if authority.Spec.Allocation.Retired || !authority.DeletionTimestamp.IsZero() {
		return fmt.Errorf("allocation %s is permanently retired in the federation hub", uid)
	}
	cellUID, err := r.cellIdentity(ctx)
	if err != nil {
		return err
	}
	if slices.Contains(authority.Spec.Allocation.Participants, cellUID) {
		return nil
	}
	before := authority.DeepCopy()
	authority.Spec.Allocation.Participants = append(authority.Spec.Allocation.Participants, cellUID)
	return hub.Patch(ctx, &authority, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) reconcileAllocationAuthority(ctx context.Context, intent *api.NetworkLoadBalancerCellIntent) (ctrl.Result, error) {
	hub, err := r.hubAuthorityClient()
	if err != nil {
		return ctrl.Result{}, err
	}
	var authority api.NetworkLoadBalancerCellIntent
	if err := hub.Get(ctx, client.ObjectKeyFromObject(intent), &authority); err != nil {
		return ctrl.Result{}, err
	}
	if authority.Spec.Allocation == nil || authority.Spec.Allocation.UID != intent.Spec.Allocation.UID {
		return ctrl.Result{}, fmt.Errorf("allocation authority identity differs from cell copy")
	}
	if !authority.Spec.Allocation.Retired {
		return ctrl.Result{RequeueAfter: nlbProgrammingRetry}, nil
	}
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	key := client.ObjectKey{Namespace: intent.Namespace, Name: networkLoadBalancerAddressName(authority.Spec.Allocation.UID)}
	var anchor galactic.NetworkServiceAddress
	if err := reader.Get(ctx, key, &anchor); apierrors.IsNotFound(err) {
		anchor = galactic.NetworkServiceAddress{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name, Labels: map[string]string{nlbAddressManagedByLabel: "true"}}, Spec: galactic.NetworkServiceAddressSpec{
			Retired: true, VIPAddress: authority.Spec.VIP, Group: galactic.NetworkServiceAddressGroup{UID: authority.Spec.Allocation.UID, Generation: max(int64(1), authority.Spec.Source.ParentGeneration), Revision: authority.Spec.Allocation.UID, Listeners: []galactic.NetworkServiceAddressListener{}},
		}}
		if err := r.Client.Create(ctx, &anchor); err != nil {
			return ctrl.Result{}, err
		}
	} else if err != nil {
		return ctrl.Result{}, err
	} else {
		if anchor.Spec.Group.UID != authority.Spec.Allocation.UID || anchor.Spec.VIPAddress != authority.Spec.VIP || anchor.Labels[nlbAddressManagedByLabel] != "true" {
			return ctrl.Result{}, fmt.Errorf("retirement anchor %s has another allocation identity", key)
		}
		if !anchor.DeletionTimestamp.IsZero() {
			return ctrl.Result{RequeueAfter: nlbProgrammingRetry}, r.reportAllocationRetirement(ctx, intent, false)
		}
		if !anchor.Spec.Retired {
			before := anchor.DeepCopy()
			anchor.Spec.Retired = true
			if err := r.Client.Patch(ctx, &anchor, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
				return ctrl.Result{}, err
			}
		}
	}
	var services galactic.NetworkServiceList
	if err := reader.List(ctx, &services, client.InNamespace(intent.Namespace)); err != nil {
		return ctrl.Result{}, err
	}
	for i := range services.Items {
		service := &services.Items[i]
		if service.Spec.AddressGroup != nil && service.Spec.AddressGroup.UID == authority.Spec.Allocation.UID {
			if _, err := r.withdraw(ctx, client.ObjectKeyFromObject(service)); err != nil {
				return ctrl.Result{}, err
			}
		}
	}
	if err := reader.Get(ctx, key, &anchor); err != nil {
		return ctrl.Result{}, err
	}
	condition := apimeta.FindStatusCondition(anchor.Status.Conditions, galactic.ConditionTypeAddressRetired)
	done := anchor.Spec.Retired && anchor.DeletionTimestamp.IsZero() && anchor.Status.ObservedGeneration == anchor.Generation && condition != nil && condition.ObservedGeneration == anchor.Generation && condition.Status == metav1.ConditionTrue
	if done {
		if err := r.acknowledgeHubRetirement(ctx, hub, &authority); err != nil {
			return ctrl.Result{}, err
		}
	}
	result := ctrl.Result{}
	if !done {
		result.RequeueAfter = nlbProgrammingRetry
	}
	return result, r.reportAllocationRetirement(ctx, intent, done)
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) acknowledgeHubRetirement(ctx context.Context, hub client.Client, authority *api.NetworkLoadBalancerCellIntent) error {
	cellUID, err := r.cellIdentity(ctx)
	if err != nil {
		return err
	}
	var current api.NetworkLoadBalancerCellIntent
	if err := hub.Get(ctx, client.ObjectKeyFromObject(authority), &current); err != nil {
		return err
	}
	if current.UID != authority.UID || current.Generation != authority.Generation || current.Spec.Allocation == nil || !current.Spec.Allocation.Retired {
		return fmt.Errorf("allocation authority changed during retirement")
	}
	if !slices.Contains(current.Spec.Allocation.Participants, cellUID) {
		return nil
	}
	before := current.DeepCopy()
	receipt := api.NetworkLoadBalancerAllocationRetirementReceipt{CellUID: cellUID, ObservedGeneration: current.Generation}
	index := slices.IndexFunc(current.Status.AllocationRetirements, func(value api.NetworkLoadBalancerAllocationRetirementReceipt) bool { return value.CellUID == cellUID })
	if index < 0 {
		current.Status.AllocationRetirements = append(current.Status.AllocationRetirements, receipt)
	} else {
		current.Status.AllocationRetirements[index] = receipt
	}
	if equality.Semantic.DeepEqual(before.Status, current.Status) {
		return nil
	}
	return hub.Status().Patch(ctx, &current, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) reportAllocationRetirement(ctx context.Context, intent *api.NetworkLoadBalancerCellIntent, done bool) error {
	var latest api.NetworkLoadBalancerCellIntent
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(intent), &latest); err != nil {
		return err
	}
	if latest.UID != intent.UID || !equality.Semantic.DeepEqual(latest.Spec, intent.Spec) {
		return fmt.Errorf("cell allocation authority changed during reporting")
	}
	*intent = latest
	before := intent.DeepCopy()
	intent.Status.ObservedGeneration = intent.Generation
	status, reason, message := metav1.ConditionUnknown, "RetirementPending", "Waiting for permanent allocation retirement and gateway withdrawal"
	retired := metav1.ConditionFalse
	if done {
		status, retired, reason, message = metav1.ConditionFalse, metav1.ConditionTrue, "AllocationRetired", "The allocation is permanently fenced from serving"
	}
	for _, typ := range []string{api.NetworkLoadBalancerCellIntentProgrammed, api.NetworkLoadBalancerCellIntentAnnounced, api.NetworkLoadBalancerCellIntentAllocationRetired} {
		value := status
		if typ == api.NetworkLoadBalancerCellIntentAllocationRetired {
			value = retired
		}
		apimeta.SetStatusCondition(&intent.Status.Conditions, metav1.Condition{Type: typ, Status: value, Reason: reason, Message: message, ObservedGeneration: intent.Generation})
	}
	if equality.Semantic.DeepEqual(before.Status, intent.Status) {
		return nil
	}
	return r.Client.Status().Patch(ctx, intent, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}
