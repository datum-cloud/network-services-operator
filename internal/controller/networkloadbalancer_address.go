// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	networkv1alpha1 "go.datum.net/galactic/api/network/v1alpha1"
	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const nlbAddressManagedByLabel = "nlb.networking.datumapis.com/address-manager"

func galacticAddressGroup(group *networkingv1alpha.NetworkLoadBalancerCellIntentAddressGroup) *networkv1alpha1.NetworkServiceAddressGroup {
	if group == nil {
		return nil
	}
	out := &networkv1alpha1.NetworkServiceAddressGroup{UID: group.UID, Generation: group.Generation, Revision: group.Revision, ListenerName: group.ListenerName}
	for _, l := range group.Listeners {
		listener := networkv1alpha1.NetworkServiceAddressListener{Name: l.Name, Protocol: networkv1alpha1.NetworkServiceProtocol(strings.ToLower(string(l.Protocol)))}
		interval := networkingv1alpha.PortInterval(l.Port, l.PortRange)
		if interval.Singleton() {
			listener.Port = interval.Start
		} else {
			listener.PortRange = &networkv1alpha1.PortRange{Start: interval.Start, End: interval.End}
		}
		out.Listeners = append(out.Listeners, listener)
	}
	return out
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) requiredCapabilities(ctx context.Context, intent *networkingv1alpha.NetworkLoadBalancerCellIntent) (string, error) {
	group := intent.Spec.AddressGroup
	interval := networkingv1alpha.PortInterval(intent.Spec.ListenerPort, intent.Spec.ListenerPortRange)
	maxPorts := r.MaxExposedPorts
	if maxPorts <= 0 {
		maxPorts = 1024
	}
	coverage := interval.Width()
	if group != nil {
		coverage = 0
		for _, listener := range group.Listeners {
			ports := networkingv1alpha.PortInterval(listener.Port, listener.PortRange)
			if !ports.Valid() {
				return "Address inventory contains an invalid interval", nil
			}
			coverage += ports.Width()
		}
	}
	if !interval.Valid() || coverage > maxPorts {
		return "Address inventory exceeds this cell's exposed port capacity", nil
	}
	var required []string
	if group != nil {
		if len(group.Listeners) > 1 && !r.SharedAddressListeners {
			return "SharedAddressListeners is not enabled in this cell", nil
		}
		required = append(required, networkv1alpha1.CapabilitySharedAddressListeners, networkv1alpha1.CapabilityTerminalAddressRetirement)
	}
	if !interval.Singleton() {
		if !r.UDPPortRanges {
			return "UDPPortRanges is not enabled in this cell", nil
		}
		required = append(required, networkv1alpha1.CapabilityUDPPortRanges)
	}
	if len(required) == 0 {
		return "", nil
	}
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	var gateways networkv1alpha1.NetworkGatewayList
	if err := reader.List(ctx, &gateways); err != nil {
		return "", fmt.Errorf("list serving gateway capabilities: %w", err)
	}
	count := 0
	for _, gateway := range gateways.Items {
		if !gateway.DeletionTimestamp.IsZero() {
			continue
		}
		count++
		if gateway.Status.ObservedGeneration != gateway.Generation || !gateway.Status.BackendHealthFiltering {
			return fmt.Sprintf("Gateway %s has not confirmed current fail-closed capabilities", gateway.Name), nil
		}
		for _, capability := range required {
			if !slices.Contains(gateway.Status.Capabilities, capability) {
				return fmt.Sprintf("Gateway %s does not support %s", gateway.Name, capability), nil
			}
		}
	}
	if count == 0 {
		return "No serving gateways have reported capabilities", nil
	}
	return "", nil
}

func (r *NetworkLoadBalancerCellProgrammingReconciler) ensureAddress(ctx context.Context, service *networkv1alpha1.NetworkService) error {
	if service.Spec.AddressGroup == nil {
		return nil
	}
	if err := r.registerAllocationParticipant(ctx, service.Namespace, service.Spec.AddressGroup.UID, service.Spec.VIPAddresses[0]); err != nil {
		return err
	}
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	var intents networkingv1alpha.NetworkLoadBalancerCellIntentList
	if err := reader.List(ctx, &intents, client.InNamespace(service.Namespace)); err != nil {
		return err
	}
	current := false
	for _, intent := range intents.Items {
		if intent.Spec.Allocation != nil && intent.Spec.Allocation.Retired && intent.Spec.Allocation.UID == service.Spec.AddressGroup.UID {
			return fmt.Errorf("allocation %s is permanently retired", service.Spec.AddressGroup.UID)
		}
		if intent.Name == service.Name && intent.DeletionTimestamp.IsZero() && cellIntentRequestsServing(&intent) && intent.Spec.AddressGroup != nil {
			current = equality.Semantic.DeepEqual(galacticAddressGroup(intent.Spec.AddressGroup), service.Spec.AddressGroup)
		}
		if candidate := intent.Spec.AddressGroup; candidate != nil && candidate.UID == service.Spec.AddressGroup.UID && candidate.Generation > service.Spec.AddressGroup.Generation {
			return fmt.Errorf("address inventory is superseded by newer intent %s", intent.Name)
		}
	}
	if !current {
		return fmt.Errorf("serving intent no longer authorizes this address inventory")
	}
	group := *service.Spec.AddressGroup
	group.ListenerName = ""
	wanted := networkv1alpha1.NetworkServiceAddressSpec{VIPAddress: service.Spec.VIPAddresses[0], VPCRef: service.Spec.VPCRef, Group: group}
	key := client.ObjectKey{Namespace: service.Namespace, Name: networkLoadBalancerAddressName(group.UID)}
	var address networkv1alpha1.NetworkServiceAddress
	if err := reader.Get(ctx, key, &address); apierrors.IsNotFound(err) {
		return r.Client.Create(ctx, &networkv1alpha1.NetworkServiceAddress{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name, Labels: map[string]string{nlbAddressManagedByLabel: "true"}}, Spec: wanted})
	} else if err != nil {
		return err
	}
	if address.Spec.Retired {
		return fmt.Errorf("address allocation %s is permanently retired", key.Name)
	}
	if !address.DeletionTimestamp.IsZero() {
		return fmt.Errorf("address inventory %s is withdrawing", key.Name)
	}
	if address.Labels[nlbAddressManagedByLabel] != "true" || address.Spec.Group.UID != group.UID || address.Spec.VIPAddress != wanted.VIPAddress || address.Spec.VPCRef != wanted.VPCRef {
		return fmt.Errorf("address inventory %s belongs to another allocation", key.Name)
	}
	if address.Spec.Group.Generation > group.Generation {
		return fmt.Errorf("address inventory %s has a newer generation", key.Name)
	}
	if address.Spec.Group.Generation == group.Generation && !equality.Semantic.DeepEqual(address.Spec, wanted) {
		return fmt.Errorf("address inventory %s has conflicting content at this generation", key.Name)
	}
	if equality.Semantic.DeepEqual(address.Spec, wanted) {
		return nil
	}
	before := address.DeepCopy()
	address.Spec = wanted
	return r.Client.Patch(ctx, &address, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
}

func networkLoadBalancerAddressName(uid string) string { return "nlb-address-" + uid }

func (r *NetworkLoadBalancerCellProgrammingReconciler) cleanupAddresses(ctx context.Context, namespace string) (bool, error) {
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	var addresses networkv1alpha1.NetworkServiceAddressList
	if err := reader.List(ctx, &addresses, client.InNamespace(namespace), client.MatchingLabels{nlbAddressManagedByLabel: "true"}); err != nil {
		return false, err
	}
	if len(addresses.Items) == 0 {
		return true, nil
	}
	var intents networkingv1alpha.NetworkLoadBalancerCellIntentList
	if err := reader.List(ctx, &intents, client.InNamespace(namespace)); err != nil {
		return false, err
	}
	var services networkv1alpha1.NetworkServiceList
	if err := reader.List(ctx, &services, client.InNamespace(namespace)); err != nil {
		return false, err
	}
	live := map[string]bool{}
	for _, intent := range intents.Items {
		if intent.DeletionTimestamp.IsZero() && intent.Spec.AddressGroup != nil && cellIntentRequestsServing(&intent) {
			live[intent.Spec.AddressGroup.UID] = true
		}
	}
	for _, service := range services.Items {
		if service.Spec.AddressGroup != nil {
			live[service.Spec.AddressGroup.UID] = true
		}
	}
	done := true
	for i := range addresses.Items {
		address := &addresses.Items[i]
		if address.Spec.Retired || live[address.Spec.Group.UID] {
			continue
		}
		if address.DeletionTimestamp.IsZero() {
			if err := r.Client.Delete(ctx, address, client.Preconditions{UID: &address.UID, ResourceVersion: &address.ResourceVersion}); client.IgnoreNotFound(err) != nil {
				return false, err
			}
		}
		var remaining networkv1alpha1.NetworkServiceAddress
		if err := reader.Get(ctx, client.ObjectKeyFromObject(address), &remaining); err == nil {
			done = false
		} else if !apierrors.IsNotFound(err) {
			return false, err
		}
	}
	return done, nil
}

func validateAddressGroup(intent *networkingv1alpha.NetworkLoadBalancerCellIntent) string {
	group := intent.Spec.AddressGroup
	if group == nil {
		return ""
	}
	if group.UID == "" || group.Revision == "" || group.Generation != intent.Spec.Source.ParentGeneration || group.ListenerName != intent.Spec.Source.ListenerName || len(group.Listeners) == 0 || len(group.Listeners) > 16 {
		return "invalid address inventory identity"
	}
	found := false
	for i, l := range group.Listeners {
		interval := networkingv1alpha.PortInterval(l.Port, l.PortRange)
		if l.Name == "" || !interval.Valid() || (l.Port != 0) == (l.PortRange != nil) || (l.Protocol != networkingv1alpha.NetworkLoadBalancerProtocolTCP && l.Protocol != networkingv1alpha.NetworkLoadBalancerProtocolUDP) || (!interval.Singleton() && l.Protocol != networkingv1alpha.NetworkLoadBalancerProtocolUDP) {
			return "invalid address inventory listener"
		}
		for _, other := range group.Listeners[:i] {
			if l.Name == other.Name || (l.Protocol == other.Protocol && interval.Overlaps(networkingv1alpha.PortInterval(other.Port, other.PortRange))) {
				return "overlapping address inventory listeners"
			}
		}
		if l.Name == group.ListenerName {
			if l.Protocol != intent.Spec.Protocol || interval != networkingv1alpha.PortInterval(intent.Spec.ListenerPort, intent.Spec.ListenerPortRange) {
				return "listener does not match address inventory"
			}
			found = true
		}
	}
	if !found {
		return "listener is absent from address inventory"
	}
	return ""
}
