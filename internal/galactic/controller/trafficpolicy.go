// SPDX-License-Identifier: AGPL-3.0-only

package controller

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	cloudv1alpha1 "go.datum.net/galactic/api/cloud/v1alpha1"
	galacticv1alpha1 "go.datum.net/galactic/api/v1alpha1"
	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
)

const (
	egressPolicySuffix  = "-egress"
	ingressPolicySuffix = "-ingress"

	ruleAllowPrivate       = "allow-private"
	ruleAllowInternet      = "allow-internet"
	ruleDenyInternet       = "deny-internet"
	ruleAllowPublicService = "allow-public-service"
)

// interfacePolicies are the objects that carry one interface's internet access
// to the node running its instance.
type interfacePolicies struct {
	egress  *galacticv1alpha1.TrafficPolicy
	ingress *galacticv1alpha1.TrafficPolicy
	binding *galacticv1alpha1.TrafficPolicyBinding
}

// internetEgressMode is the mode a context instructs its location to enforce.
// A context without one was projected before the control plane carried the
// mode, and is treated as Enabled so no running instance loses internet access
// while the projection catches up.
func internetEgressMode(networkContext *networkingv1alpha.NetworkContext) networkingv1alpha.NetworkInternetEgressMode {
	if networkContext.Spec.Egress != nil && networkContext.Spec.Egress.Internet != nil &&
		networkContext.Spec.Egress.Internet.Mode != "" {
		return networkContext.Spec.Egress.Internet.Mode
	}
	return networkingv1alpha.NetworkInternetEgressEnabled
}

// policyRules always allow the network, the platform's private services, and
// traffic through an external address, in either direction. The internet,
// reached through the NAT translators, is allowed only when the network turns
// it on. Policy tracks no connections, so the ingress policy needs the same
// internet grant to let the translators' replies back in.
func policyRules(mode networkingv1alpha.NetworkInternetEgressMode) []galacticv1alpha1.TrafficRule {
	return []galacticv1alpha1.TrafficRule{
		pathRule(ruleAllowPrivate, galacticv1alpha1.ActionAllow, galacticv1alpha1.PathPrivate),
		internetRule(mode),
		pathRule(ruleAllowPublicService, galacticv1alpha1.ActionAllow, galacticv1alpha1.PathPublicService),
	}
}

func internetRule(mode networkingv1alpha.NetworkInternetEgressMode) galacticv1alpha1.TrafficRule {
	if mode == networkingv1alpha.NetworkInternetEgressDisabled {
		return pathRule(ruleDenyInternet, galacticv1alpha1.ActionDeny, galacticv1alpha1.PathPublicNAT)
	}
	return pathRule(ruleAllowInternet, galacticv1alpha1.ActionAllow, galacticv1alpha1.PathPublicNAT)
}

func pathRule(
	name string, action galacticv1alpha1.RuleAction, path galacticv1alpha1.ForwardingPath,
) galacticv1alpha1.TrafficRule {
	return galacticv1alpha1.TrafficRule{
		Name:   name,
		Action: action,
		Match:  &galacticv1alpha1.TrafficRuleMatch{Path: ptr.To(path)},
	}
}

// reconcileTrafficPolicy writes the interface's egress and ingress policies
// and the binding that applies them to its attachment. It runs before the
// attachment definition and Prepared are written, so no instance can attach
// before its binding exists: until one does, Galactic denies everything,
// private traffic included.
func (r *NetworkInterfaceReconciler) reconcileTrafficPolicy(
	ctx context.Context,
	networkInterface *networkingv1alpha.NetworkInterface,
	attachment *cloudv1alpha1.VPCAttachment,
	mode networkingv1alpha.NetworkInternetEgressMode,
) (*interfacePolicies, error) {
	egress, err := r.reconcilePolicy(ctx, networkInterface, attachment.Name+egressPolicySuffix, policyRules(mode))
	if err != nil {
		return nil, err
	}
	ingress, err := r.reconcilePolicy(ctx, networkInterface, attachment.Name+ingressPolicySuffix, policyRules(mode))
	if err != nil {
		return nil, err
	}

	binding := &galacticv1alpha1.TrafficPolicyBinding{
		ObjectMeta: metav1.ObjectMeta{Name: attachment.Name, Namespace: attachment.Namespace},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, binding, func() error {
		binding.Spec.EgressPolicyRef = &galacticv1alpha1.LocalPolicyReference{Name: egress.Name}
		binding.Spec.IngressPolicyRef = &galacticv1alpha1.LocalPolicyReference{Name: ingress.Name}
		return controllerutil.SetControllerReference(networkInterface, binding, r.Scheme)
	}); err != nil {
		return nil, fmt.Errorf("reconcile traffic policy binding %s: %w", binding.Name, err)
	}

	return &interfacePolicies{egress: egress, ingress: ingress, binding: binding}, nil
}

func (r *NetworkInterfaceReconciler) reconcilePolicy(
	ctx context.Context,
	networkInterface *networkingv1alpha.NetworkInterface,
	name string,
	rules []galacticv1alpha1.TrafficRule,
) (*galacticv1alpha1.TrafficPolicy, error) {
	policy := &galacticv1alpha1.TrafficPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: networkInterface.Namespace},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, policy, func() error {
		policy.Spec.Rules = rules
		return controllerutil.SetControllerReference(networkInterface, policy, r.Scheme)
	}); err != nil {
		return nil, fmt.Errorf("reconcile traffic policy %s: %w", name, err)
	}
	return policy, nil
}

// setInternetEgressCondition reports whether the network's internet access
// mode is in effect for the interface.
//
// A binding reports Applied whatever mode the node enforces in, so Applied
// alone never proves traffic is blocked. Disabled is reported only for a cell
// configured as enforcing; anywhere else the interface says plainly that it
// can still reach the internet.
func (r *NetworkInterfaceReconciler) setInternetEgressCondition(
	networkInterface *networkingv1alpha.NetworkInterface,
	attachment *cloudv1alpha1.VPCAttachment,
	policies *interfacePolicies,
	mode networkingv1alpha.NetworkInternetEgressMode,
) {
	status, reason, message := internetEgressState(r.TrafficPolicyEnforced, mode, attachment, policies)
	if reason == networkingv1alpha.NetworkInterfaceInternetEgressReasonDisabled &&
		len(networkInterface.Spec.ExternalAddresses) > 0 {
		message = "Internet access is turned off for this network, but this instance has an external address, " +
			"and traffic through it is not affected, so it can still reach the internet through that address."
	}
	meta.SetStatusCondition(&networkInterface.Status.Conditions, metav1.Condition{
		Type:               networkingv1alpha.NetworkInterfaceInternetEgressReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: networkInterface.Generation,
	})
}

func internetEgressState(
	enforced bool,
	mode networkingv1alpha.NetworkInternetEgressMode,
	attachment *cloudv1alpha1.VPCAttachment,
	policies *interfacePolicies,
) (metav1.ConditionStatus, string, string) {
	disabled := mode == networkingv1alpha.NetworkInternetEgressDisabled

	if !enforced {
		if disabled {
			return metav1.ConditionFalse, networkingv1alpha.NetworkInterfaceInternetEgressReasonNotEnforced,
				"Internet access is turned off for this network, but this location does not block internet " +
					"traffic yet, so this instance can still reach the internet."
		}
		return metav1.ConditionTrue, networkingv1alpha.NetworkInterfaceInternetEgressReasonEnabled,
			"This instance can reach the internet."
	}

	node := attachment.Status.Node
	if node == "" {
		return metav1.ConditionUnknown, networkingv1alpha.NetworkInterfaceInternetEgressReasonPending,
			"Waiting for this instance to start."
	}

	var applied *galacticv1alpha1.NodeBindingStatus
	for i := range policies.binding.Status.Nodes {
		if policies.binding.Status.Nodes[i].Node == node {
			applied = &policies.binding.Status.Nodes[i]
			break
		}
	}
	pending := []string{}
	if applied == nil {
		pending = append(pending, "egress", "ingress")
	} else {
		for _, direction := range []struct {
			name    string
			applied *galacticv1alpha1.AppliedPolicy
			desired *galacticv1alpha1.TrafficPolicy
		}{
			{"egress", applied.Egress, policies.egress},
			{"ingress", applied.Ingress, policies.ingress},
		} {
			if direction.applied != nil && direction.applied.State == galacticv1alpha1.StateFailed {
				return metav1.ConditionFalse, networkingv1alpha.NetworkInterfaceInternetEgressReasonFailed,
					"The node running this instance could not apply this network's internet access setting."
			}
			if !appliedPolicyMatches(direction.applied, direction.desired) {
				pending = append(pending, direction.name)
			}
		}
	}
	if len(pending) > 0 {
		return metav1.ConditionUnknown, networkingv1alpha.NetworkInterfaceInternetEgressReasonPending,
			fmt.Sprintf("Waiting for the node running this instance to apply this network's internet access "+
				"setting (%s).", strings.Join(pending, ", "))
	}

	if disabled {
		return metav1.ConditionTrue, networkingv1alpha.NetworkInterfaceInternetEgressReasonDisabled,
			"Internet access is turned off for this network. This instance reaches other instances on the " +
				"network and nothing outside the platform."
	}
	return metav1.ConditionTrue, networkingv1alpha.NetworkInterfaceInternetEgressReasonEnabled,
		"This instance can reach the internet."
}

// appliedPolicyMatches reports whether a node has installed exactly the
// policy generation written here. A node still enforcing an earlier
// generation has not taken a mode change yet.
func appliedPolicyMatches(applied *galacticv1alpha1.AppliedPolicy, desired *galacticv1alpha1.TrafficPolicy) bool {
	return applied != nil &&
		applied.State == galacticv1alpha1.StateApplied &&
		applied.Policy != nil &&
		applied.Policy.UID == string(desired.UID) &&
		applied.Policy.Generation == desired.Generation
}

// interfacesForNetworkContext enqueues every interface realized in a context,
// so a mode change reaches instances that are already running.
func (r *NetworkInterfaceReconciler) interfacesForNetworkContext(ctx context.Context, obj client.Object) []ctrl.Request {
	var interfaces networkingv1alpha.NetworkInterfaceList
	if err := r.List(ctx, &interfaces, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	requests := []ctrl.Request{}
	for _, networkInterface := range interfaces.Items {
		if ref := networkInterface.Status.NetworkContextRef; ref != nil && ref.Name == obj.GetName() {
			requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&networkInterface)})
		}
	}
	return requests
}
