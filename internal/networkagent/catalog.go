// SPDX-License-Identifier: AGPL-3.0-only

// Package networkagent turns a project's networks and network services into
// answers an assistant can give a customer: a lookup table over every condition
// reason the network controllers write, keyed on the kind that carries it, and
// walks over the objects a network and a service are made of.
package networkagent

import (
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	networkingv1alpha "go.datum.net/network-services-operator/api/v1alpha"
	"go.datum.net/network-services-operator/internal/agenttime"
)

type Actionability string

const (
	ActionabilityUser          Actionability = "user"
	ActionabilityPlatform      Actionability = "platform"
	ActionabilityTransient     Actionability = "transient"
	ActionabilityStalled       Actionability = "stalled"
	ActionabilityInformational Actionability = "informational"
)

type Scope string

const (
	ScopeNetwork  Scope = "whole-network"
	ScopeLocation Scope = "one-location"
	ScopeService  Scope = "service"
	ScopeInstance Scope = "one-instance"
	ScopeDeletion Scope = "deletion-blocked"
)

func scopeRank(s Scope) int {
	switch s {
	case ScopeNetwork:
		return 0
	case ScopeLocation:
		return 1
	case ScopeService:
		return 2
	case ScopeInstance:
		return 3
	case ScopeDeletion:
		return 4
	default:
		return 5
	}
}

const (
	KindNetwork               = "Network"
	KindNetworkContext        = "NetworkContext"
	KindNetworkBinding        = "NetworkBinding"
	KindSubnet                = "Subnet"
	KindSubnetClaim           = "SubnetClaim"
	KindNetworkInterface      = "NetworkInterface"
	KindNetworkInterfaceClaim = "NetworkInterfaceClaim"
	KindNetworkService        = "NetworkService"
)

const (
	SkillNetworkCreate        = "network-create"
	SkillServiceCreate        = "network-service-create"
	SkillNetworkNotReady      = "network-not-ready"
	SkillNetworkInLocation    = "network-location-triage"
	SkillInterfaceTriage      = "network-interface-triage"
	SkillServiceNotServing    = "network-service-not-serving"
	SkillNetworkDeletionStuck = "network-deletion-stuck"
)

const (
	windowControlPlane = 5 * time.Minute
	windowDataPlane    = 10 * time.Minute
	windowRemoval      = 15 * time.Minute
)

const (
	remediationWait     = "Nothing to do here — this is normal and clears on its own."
	remediationEscalate = "Raise this with Datum. There is no change to your network or workloads that will clear it."
)

const (
	reasonPending                   = "Pending"
	reasonUnknown                   = "Unknown"
	reasonAllocated                 = "Allocated"
	reasonPrefixAllocated           = "PrefixAllocated"
	reasonAddressPoolExhausted      = "AddressPoolExhausted"
	reasonRetainedAddressConflict   = "RetainedAddressConflict"
	reasonAddressAllocationRejected = "AddressAllocationRejected"
	reasonAllocationFailed          = "AllocationFailed"
	reasonAddressAllocationMissing  = "AddressAllocationMissing"
	reasonSubnetNotReady            = "SubnetNotReady"
	reasonSubnetReady               = "SubnetReady"
	reasonBound                     = "Bound"
	reasonReady                     = "Ready"
	reasonNetworkMismatch           = "NetworkMismatch"
	reasonInterfaceNameMismatch     = "InterfaceNameMismatch"
	reasonAddressFamilyMissing      = "AddressFamilyMissing"
	reasonAddressClassMissing       = "AddressClassMissing"
	reasonReclaimPolicyMismatch     = "ReclaimPolicyMismatch"
	reasonInterfaceHeldByAnother    = "InterfaceHeldByAnotherClaim"
	reasonInvalidSelector           = "InvalidSelector"
	reasonResolved                  = "Resolved"
	conditionSubnetClaimReady       = "Ready"
	notPrefix                       = "Not"
)

type ReasonInfo struct {
	Kind             string        `json:"kind"`
	ConditionType    string        `json:"conditionType"`
	Reason           string        `json:"reason"`
	Actionability    Actionability `json:"actionability"`
	Scope            Scope         `json:"scope,omitempty"`
	Explanation      string        `json:"explanation"`
	Remediation      string        `json:"remediation,omitempty"`
	Skill            string        `json:"skill,omitempty"`
	ExpectedDuration time.Duration `json:"-"`
	ExpectedWithin   string        `json:"expectedWithin,omitempty"`
}

type Key struct {
	Kind          string
	ConditionType string
	Reason        string
}

type refusal struct {
	reason string
	info   ReasonInfo
}

func onEach(kind string, conditionTypes []string, refusals []refusal) []ReasonInfo {
	out := make([]ReasonInfo, 0, len(refusals)*len(conditionTypes))
	for _, r := range refusals {
		out = append(out, on(kind, conditionTypes, r.reason, r.info)...)
	}
	return out
}

func entry(kind, conditionType, reason string, info ReasonInfo) ReasonInfo {
	info.Kind, info.ConditionType, info.Reason = kind, conditionType, reason
	return info
}

func on(kind string, conditionTypes []string, reason string, info ReasonInfo) []ReasonInfo {
	out := make([]ReasonInfo, 0, len(conditionTypes))
	for _, ct := range conditionTypes {
		out = append(out, entry(kind, ct, reason, info))
	}
	return out
}

func notStarted(scope Scope, subject string) ReasonInfo {
	return ReasonInfo{
		Actionability:    ActionabilityTransient,
		Scope:            scope,
		Explanation:      "Datum has not looked at " + subject + " yet.",
		Remediation:      remediationWait,
		ExpectedDuration: windowControlPlane,
	}
}

func healthy(explanation string) ReasonInfo {
	return ReasonInfo{Actionability: ActionabilityInformational, Explanation: explanation}
}

func addressing(kind string, scope Scope, subject string) []ReasonInfo {
	conds := []string{networkingv1alpha.NetworkIPAMAllocated, networkingv1alpha.NetworkReady}
	occupied := ReasonInfo{
		Actionability: ActionabilityUser,
		Scope:         ScopeDeletion,
		Explanation: subject + " is being removed, and its address range cannot be given back while an " +
			"instance still holds an address in it.",
		Remediation: "Remove the workloads still attached to this network, or move them to another network. " +
			"Removal finishes on its own once the last one lets go.",
		Skill: SkillNetworkDeletionStuck,
	}
	if kind == KindNetworkContext {
		occupied.Actionability = ActionabilityTransient
		occupied.ExpectedDuration = windowRemoval
		occupied.Remediation = "Usually nothing: instances leaving this location give their addresses back " +
			"as they go. If it holds, remove the workloads still placed here on this network."
	}

	out := onEach(kind, conds, []refusal{
		{networkingv1alpha.NetworkReasonProjectNamespaceNotFound, ReasonInfo{
			Actionability: ActionabilityPlatform, Scope: scope,
			Explanation: "Your project is missing something Datum sets up when the project is created, so no " +
				"addresses can be set aside for " + subject + ".",
			Remediation: remediationEscalate, Skill: SkillNetworkNotReady,
		}},
		{networkingv1alpha.NetworkReasonProjectUnresolved, ReasonInfo{
			Actionability: ActionabilityPlatform, Scope: scope,
			Explanation: "Datum could not work out which project " + subject + " belongs to, so no addresses " +
				"can be set aside for it.",
			Remediation: remediationEscalate, Skill: SkillNetworkNotReady,
		}},
		{networkingv1alpha.NetworkReasonRangeUnsupported, ReasonInfo{
			Actionability: ActionabilityPlatform, Scope: scope,
			Explanation: "Datum's address management could not set aside a dedicated address range for " +
				subject + ".",
			Remediation: remediationEscalate, Skill: SkillNetworkNotReady,
		}},
		{reasonAddressPoolExhausted, ReasonInfo{
			Actionability: ActionabilityPlatform, Scope: scope,
			Explanation: "Datum has run out of addresses to give " + subject + ".",
			Remediation: remediationEscalate, Skill: SkillNetworkNotReady,
		}},
		{reasonRetainedAddressConflict, ReasonInfo{
			Actionability: ActionabilityPlatform, Scope: scope,
			Explanation: "An earlier allocation still holds the address range " + subject + " needs.",
			Remediation: remediationEscalate, Skill: SkillNetworkNotReady,
		}},
		{reasonAddressAllocationRejected, ReasonInfo{
			Actionability: ActionabilityPlatform, Scope: scope,
			Explanation: "Datum's address management refused to give " + subject + " an address range.",
			Remediation: remediationEscalate, Skill: SkillNetworkNotReady,
		}},
		{reasonAllocationFailed, ReasonInfo{
			Actionability: ActionabilityPlatform, Scope: scope,
			Explanation: "Setting aside addresses for " + subject + " failed.",
			Remediation: remediationEscalate, Skill: SkillNetworkNotReady,
		}},
		{networkingv1alpha.NetworkReasonRangeOccupied, occupied},
	})
	return append(out,
		entry(kind, networkingv1alpha.NetworkIPAMAllocated, reasonAllocated,
			healthy("An address range is set aside for "+subject+".")),
		entry(kind, networkingv1alpha.NetworkReady, reasonPending, notStarted(scope, subject)),
	)
}

var networkCatalog = append(addressing(KindNetwork, ScopeNetwork, "this network"),
	entry(KindNetwork, networkingv1alpha.NetworkReady, networkingv1alpha.NetworkReadyReasonReady,
		healthy("The network is ready for workloads.")),
	entry(KindNetwork, networkingv1alpha.NetworkReady, networkingv1alpha.NetworkReadyReasonIPv6Required, ReasonInfo{
		Actionability: ActionabilityUser,
		Scope:         ScopeNetwork,
		Explanation: "This network does not carry IPv6. Datum gives instances their addresses over IPv6, so " +
			"nothing placed on this network can be given one, and waiting will not change that.",
		Remediation: "Add IPv6 to the network's ipFamilies. Networks created today are IPv6-only.",
		Skill:       SkillNetworkNotReady,
	}),
)

var locationCatalog = append(addressing(KindNetworkContext, ScopeLocation, "this network in this location"),
	entry(KindNetworkContext, networkingv1alpha.NetworkContextReady, networkingv1alpha.NetworkContextReadyReasonReady,
		healthy("The network is ready in this location.")),
	entry(KindNetworkContext, networkingv1alpha.NetworkContextReady,
		networkingv1alpha.NetworkContextReadyReasonTerminating, ReasonInfo{
			Actionability:    ActionabilityTransient,
			Scope:            ScopeLocation,
			Explanation:      "The network is being removed from this location. Nothing new can join it here until that finishes.",
			Remediation:      remediationWait,
			Skill:            SkillNetworkInLocation,
			ExpectedDuration: windowRemoval,
		}),

	entry(KindNetworkBinding, networkingv1alpha.NetworkBindingReady, networkingv1alpha.NetworkBindingReasonPending,
		notStarted(ScopeLocation, "this workload's use of the network in this location")),
	entry(KindNetworkBinding, networkingv1alpha.NetworkBindingReady, reasonUnknown,
		notStarted(ScopeLocation, "this workload's use of the network in this location")),
	entry(KindNetworkBinding, networkingv1alpha.NetworkBindingReady,
		networkingv1alpha.NetworkBindingReasonProjectUnresolved, ReasonInfo{
			Actionability: ActionabilityPlatform,
			Scope:         ScopeLocation,
			Explanation:   "Datum could not work out which project this workload belongs to, so the network cannot be placed for it.",
			Remediation:   remediationEscalate,
			Skill:         SkillNetworkInLocation,
		}),
	entry(KindNetworkBinding, networkingv1alpha.NetworkBindingReady,
		networkingv1alpha.NetworkBindingReasonNetworkNotFound, ReasonInfo{
			Actionability: ActionabilityUser,
			Scope:         ScopeLocation,
			Explanation:   "A workload names a network that does not exist in this project, so it has nothing to attach to.",
			Remediation:   "Create a network with the name the workload uses, or change the workload to name one that exists.",
			Skill:         SkillNetworkCreate,
		}),
	entry(KindNetworkBinding, networkingv1alpha.NetworkBindingReady,
		networkingv1alpha.NetworkBindingReasonLocationNotAvailable, ReasonInfo{
			Actionability: ActionabilityUser,
			Scope:         ScopeLocation,
			Explanation:   "Your project has not been offered this location, so the network cannot be placed there.",
			Remediation:   "Place the workload in a location your project is offered, or ask Datum for access to this one.",
			Skill:         SkillNetworkInLocation,
		}),
	entry(KindNetworkBinding, networkingv1alpha.NetworkBindingReady,
		networkingv1alpha.NetworkBindingReasonNetworkContextNotReady, ReasonInfo{
			Actionability:    ActionabilityTransient,
			Scope:            ScopeLocation,
			Explanation:      "The network is still being set up in this location.",
			Remediation:      remediationWait,
			Skill:            SkillNetworkInLocation,
			ExpectedDuration: windowControlPlane,
		}),
	entry(KindNetworkBinding, networkingv1alpha.NetworkBindingReady,
		networkingv1alpha.NetworkBindingReasonNetworkContextReady,
		healthy("The network is in place for this workload in this location.")),
	entry(KindNetworkBinding, networkingv1alpha.NetworkBindingReady,
		networkingv1alpha.NetworkBindingReasonNetworkContextTerminating, ReasonInfo{
			Actionability: ActionabilityTransient,
			Scope:         ScopeLocation,
			Explanation: "The network is being removed from this location, so this workload waits for it to " +
				"go and for a fresh one to be set up.",
			Remediation:      remediationWait,
			Skill:            SkillNetworkInLocation,
			ExpectedDuration: windowRemoval,
		}),

	entry(KindSubnet, networkingv1alpha.SubnetAllocated, reasonPrefixAllocated,
		healthy("This location's address range is set aside.")),
	entry(KindSubnet, networkingv1alpha.SubnetAllocated, reasonPending, notStarted(ScopeLocation, "this location's address range")),
	entry(KindSubnet, networkingv1alpha.SubnetReady, networkingv1alpha.SubnetReadyReasonReady,
		healthy("This location's address range is in use.")),
	entry(KindSubnet, networkingv1alpha.SubnetReady, reasonPending, notStarted(ScopeLocation, "this location's address range")),
	entry(KindSubnet, networkingv1alpha.SubnetReady, networkingv1alpha.SubnetProgrammedReasonNotProgrammed, ReasonInfo{
		Actionability:    ActionabilityTransient,
		Scope:            ScopeLocation,
		Explanation:      "This location's address range is set aside but not yet in place on the network here.",
		Remediation:      remediationWait,
		Skill:            SkillNetworkInLocation,
		ExpectedDuration: windowDataPlane,
	}),
	entry(KindSubnet, networkingv1alpha.SubnetProgrammed, networkingv1alpha.SubnetProgrammedReasonProgrammed,
		healthy("This location's address range is in place on the network here.")),
	entry(KindSubnet, networkingv1alpha.SubnetProgrammed, networkingv1alpha.SubnetProgrammedReasonPending, ReasonInfo{
		Actionability:    ActionabilityTransient,
		Scope:            ScopeLocation,
		Explanation:      "This location has not yet reported whether its address range is in place on the network here.",
		Remediation:      remediationWait,
		Skill:            SkillNetworkInLocation,
		ExpectedDuration: windowDataPlane,
	}),
	entry(KindSubnet, networkingv1alpha.SubnetProgrammed, networkingv1alpha.SubnetProgrammedReasonNotProgrammed, ReasonInfo{
		Actionability:    ActionabilityTransient,
		Scope:            ScopeLocation,
		Explanation:      "This location reports its address range is not in place on the network here.",
		Remediation:      remediationWait,
		Skill:            SkillNetworkInLocation,
		ExpectedDuration: windowDataPlane,
	}),
	entry(KindSubnet, networkingv1alpha.SubnetProgrammed,
		networkingv1alpha.SubnetProgrammedReasonProgrammingInProgress, ReasonInfo{
			Actionability:    ActionabilityTransient,
			Scope:            ScopeLocation,
			Explanation:      "This location's address range is being put in place on the network here.",
			Remediation:      remediationWait,
			Skill:            SkillNetworkInLocation,
			ExpectedDuration: windowDataPlane,
		}),

	entry(KindSubnetClaim, conditionSubnetClaimReady, reasonSubnetReady,
		healthy("The address range this asked for is in use.")),
	entry(KindSubnetClaim, conditionSubnetClaimReady, reasonSubnetNotReady, ReasonInfo{
		Actionability:    ActionabilityTransient,
		Scope:            ScopeLocation,
		Explanation:      "The address range this asked for is not in use yet.",
		Remediation:      remediationWait,
		Skill:            SkillNetworkInLocation,
		ExpectedDuration: windowDataPlane,
	}),
)

func claimRefusals() []ReasonInfo {
	conds := []string{
		networkingv1alpha.NetworkInterfaceClaimReady,
		networkingv1alpha.NetworkInterfaceClaimBound,
		networkingv1alpha.NetworkInterfaceClaimAllocated,
	}
	retained := func(what string) ReasonInfo {
		return ReasonInfo{
			Actionability: ActionabilityUser,
			Scope:         ScopeInstance,
			Explanation: "This instance came back to find its kept network interface, and " + what + ". Its " +
				"addresses were kept on purpose, so Datum will not quietly hand them to a different setup.",
			Remediation: "Either bring the workload back with the network settings it had before, or delete the " +
				"kept network interface to give its addresses up for good and let a new one be made.",
			Skill: SkillInterfaceTriage,
		}
	}

	return onEach(KindNetworkInterfaceClaim, conds, []refusal{
		{networkingv1alpha.NetworkInterfaceClaimReasonNetworkNotAvailableInLocation, ReasonInfo{
			Actionability: ActionabilityTransient, Scope: ScopeInstance,
			Explanation:      "The network has not reached this instance's location yet, so its network interface is waiting for it.",
			Remediation:      remediationWait,
			Skill:            SkillNetworkInLocation,
			ExpectedDuration: windowDataPlane,
		}},
		{networkingv1alpha.NetworkInterfaceClaimReasonAddressFamilyNotCarried, ReasonInfo{
			Actionability: ActionabilityUser, Scope: ScopeInstance,
			Explanation: "This instance asks for a kind of address the network does not carry. Networks carry " +
				"IPv6, so an interface asking for IPv4 on one cannot be given an address.",
			Remediation: "Recreate the workload so its interface asks for IPv6 only. A workload asking for a " +
				"public IPv4 address asks for IPv4 too, and new networks cannot carry it; if it needs IPv4, " +
				"raise that with Datum.",
			Skill: SkillInterfaceTriage,
		}},
		{networkingv1alpha.NetworkInterfaceClaimReasonAddressFamiliesUnknown, ReasonInfo{
			Actionability: ActionabilityPlatform, Scope: ScopeLocation,
			Explanation: "The network reached this location without saying which kinds of address it carries, " +
				"so no instance here can be given one.",
			Remediation: remediationEscalate, Skill: SkillNetworkInLocation,
		}},
		{networkingv1alpha.NetworkInterfaceClaimReasonSubnetGatewayUnusable, ReasonInfo{
			Actionability: ActionabilityPlatform, Scope: ScopeLocation,
			Explanation: "This location's address range has no usable gateway, and an interface without one " +
				"cannot reach anything.",
			Remediation: remediationEscalate, Skill: SkillNetworkInLocation,
		}},
		{networkingv1alpha.NetworkInterfaceClaimReasonNetworkContextTerminating, ReasonInfo{
			Actionability: ActionabilityTransient, Scope: ScopeLocation,
			Explanation:      "The network is being removed from this location, so no new address can be given out here.",
			Remediation:      remediationWait,
			Skill:            SkillNetworkInLocation,
			ExpectedDuration: windowRemoval,
		}},
		{networkingv1alpha.NetworkInterfaceClaimReasonSubnetTerminating, ReasonInfo{
			Actionability: ActionabilityTransient, Scope: ScopeLocation,
			Explanation:      "This location's address range is being removed, so no new address can be given out from it.",
			Remediation:      remediationWait,
			Skill:            SkillNetworkInLocation,
			ExpectedDuration: windowRemoval,
		}},
		{networkingv1alpha.NetworkInterfaceClaimReasonProjectNamespaceNotFound, ReasonInfo{
			Actionability: ActionabilityPlatform, Scope: ScopeNetwork,
			Explanation: "Your project is missing something Datum sets up when the project is created, so no instance can be given an address.",
			Remediation: remediationEscalate, Skill: SkillInterfaceTriage,
		}},
		{reasonAddressPoolExhausted, ReasonInfo{
			Actionability: ActionabilityPlatform, Scope: ScopeLocation,
			Explanation: "Datum has run out of addresses to give instances in this location.",
			Remediation: remediationEscalate, Skill: SkillInterfaceTriage,
		}},
		{reasonRetainedAddressConflict, ReasonInfo{
			Actionability: ActionabilityPlatform, Scope: ScopeInstance,
			Explanation: "An earlier allocation still holds the address this instance needs.",
			Remediation: remediationEscalate, Skill: SkillInterfaceTriage,
		}},
		{reasonAddressAllocationRejected, ReasonInfo{
			Actionability: ActionabilityPlatform, Scope: ScopeInstance,
			Explanation: "Datum's address management refused to give this instance an address.",
			Remediation: remediationEscalate, Skill: SkillInterfaceTriage,
		}},
		{reasonAllocationFailed, ReasonInfo{
			Actionability: ActionabilityPlatform, Scope: ScopeInstance,
			Explanation: "Giving this instance an address failed.",
			Remediation: remediationEscalate, Skill: SkillInterfaceTriage,
		}},
		{reasonNetworkMismatch, retained("it holds addresses on a different network from the one the workload now names")},
		{reasonInterfaceNameMismatch, retained("it appears inside the instance under a different name from the one the workload now asks for")},
		{reasonAddressFamilyMissing, retained("it holds no address of a kind the workload now asks for")},
		{reasonAddressClassMissing, retained("it holds no address of a class the workload now asks for, such as a public address")},
		{reasonReclaimPolicyMismatch, retained("its addresses were kept under a different rule for what happens when the instance goes away")},
		{reasonInterfaceHeldByAnother, ReasonInfo{
			Actionability: ActionabilityPlatform, Scope: ScopeInstance,
			Explanation: "The network interface this instance should use is already held by another instance.",
			Remediation: remediationEscalate, Skill: SkillInterfaceTriage,
		}},
	})
}

func waitingOn(step string) ReasonInfo {
	return ReasonInfo{
		Actionability:    ActionabilityTransient,
		Scope:            ScopeInstance,
		Explanation:      "This instance is waiting to " + step + ".",
		Remediation:      remediationWait,
		Skill:            SkillInterfaceTriage,
		ExpectedDuration: windowDataPlane,
	}
}

func interfaceProgress(kind string, allocated, prepared, programmed string) []ReasonInfo {
	return []ReasonInfo{
		entry(kind, allocated, reasonAllocated, healthy("Every address this interface needs is held.")),
		entry(kind, allocated, reasonPending, notStarted(ScopeInstance, "this instance's network interface")),
		entry(kind, allocated, reasonAddressAllocationMissing, ReasonInfo{
			Actionability: ActionabilityPlatform,
			Scope:         ScopeInstance,
			Explanation:   "An address this instance's network interface uses is no longer held for it.",
			Remediation:   remediationEscalate,
			Skill:         SkillInterfaceTriage,
		}),
		entry(kind, prepared, reasonPending, ReasonInfo{
			Actionability:    ActionabilityTransient,
			Scope:            ScopeInstance,
			Explanation:      "The network is getting ready for this instance in its location.",
			Remediation:      remediationWait,
			Skill:            SkillInterfaceTriage,
			ExpectedDuration: windowDataPlane,
		}),
		entry(kind, programmed, reasonPending, ReasonInfo{
			Actionability: ActionabilityTransient,
			Scope:         ScopeInstance,
			Explanation: "This instance's addresses are not carried on the network yet. That happens as the " +
				"instance starts, so it is expected while it is starting.",
			Remediation:      remediationWait,
			Skill:            SkillInterfaceTriage,
			ExpectedDuration: windowDataPlane,
		}),
	}
}

var interfaceCatalog = slices.Concat(
	claimRefusals(),
	interfaceProgress(KindNetworkInterfaceClaim, networkingv1alpha.NetworkInterfaceClaimAllocated,
		networkingv1alpha.NetworkInterfaceClaimPrepared, networkingv1alpha.NetworkInterfaceClaimProgrammed),
	interfaceProgress(KindNetworkInterface, networkingv1alpha.NetworkInterfaceAllocated,
		networkingv1alpha.NetworkInterfacePrepared, networkingv1alpha.NetworkInterfaceProgrammed),
	[]ReasonInfo{
		entry(KindNetworkInterfaceClaim, networkingv1alpha.NetworkInterfaceClaimBound, reasonPending,
			notStarted(ScopeInstance, "this instance's network interface")),
		entry(KindNetworkInterfaceClaim, networkingv1alpha.NetworkInterfaceClaimBound, reasonBound,
			healthy("This instance holds its network interface.")),
		entry(KindNetworkInterfaceClaim, networkingv1alpha.NetworkInterfaceClaimReady, reasonReady,
			healthy("This instance's network interface is addressed and carried on the network.")),
		entry(KindNetworkInterfaceClaim, networkingv1alpha.NetworkInterfaceClaimReady,
			notPrefix+networkingv1alpha.NetworkInterfaceClaimBound, waitingOn("hold its network interface")),
		entry(KindNetworkInterfaceClaim, networkingv1alpha.NetworkInterfaceClaimReady,
			notPrefix+networkingv1alpha.NetworkInterfaceClaimAllocated, waitingOn("be given its addresses")),
		entry(KindNetworkInterfaceClaim, networkingv1alpha.NetworkInterfaceClaimReady,
			notPrefix+networkingv1alpha.NetworkInterfaceClaimPrepared, waitingOn("have the network ready for it")),
		entry(KindNetworkInterfaceClaim, networkingv1alpha.NetworkInterfaceClaimReady,
			notPrefix+networkingv1alpha.NetworkInterfaceClaimProgrammed, waitingOn("have its addresses carried on the network")),
		entry(KindNetworkInterface, networkingv1alpha.NetworkInterfaceHolderAvailable,
			networkingv1alpha.NetworkInterfaceReasonHolderAvailable,
			healthy("The instance behind this interface says it is ready to serve.")),
		entry(KindNetworkInterface, networkingv1alpha.NetworkInterfaceHolderAvailable, reasonPending, ReasonInfo{
			Actionability:    ActionabilityTransient,
			Scope:            ScopeInstance,
			Explanation:      "The instance behind this interface has not said yet whether it is ready to serve.",
			Remediation:      remediationWait,
			Skill:            SkillServiceNotServing,
			ExpectedDuration: windowDataPlane,
		}),
		entry(KindNetworkInterface, networkingv1alpha.NetworkInterfaceHolderAvailable,
			networkingv1alpha.NetworkInterfaceReasonHolderUnavailable, ReasonInfo{
				Actionability: ActionabilityUser,
				Scope:         ScopeInstance,
				Explanation: "The instance behind this interface says it is not ready to serve: it is starting, " +
					"failing, or shutting down. The network is fine; the instance is the thing to look at.",
				Remediation: "Look at the workload this instance belongs to and why it is not available.",
				Skill:       SkillServiceNotServing,
			}),
		entry(KindNetworkInterface, networkingv1alpha.NetworkInterfaceHolderAvailable,
			networkingv1alpha.NetworkInterfaceReasonHolderReleased, ReasonInfo{
				Actionability: ActionabilityInformational,
				Explanation: "This interface was kept after its instance went away, and is waiting for the " +
					"instance to come back. Nothing is serving from it until then.",
			}),
	},
)

func serviceResolution() []ReasonInfo {
	conds := []string{networkingv1alpha.NetworkServiceMembersResolved, networkingv1alpha.NetworkServiceReady}
	return slices.Concat(
		on(KindNetworkService, conds, networkingv1alpha.NetworkServiceReasonNoMatchingInterfaces, ReasonInfo{
			Actionability: ActionabilityUser,
			Scope:         ScopeService,
			Explanation: "Nothing matches what this service selects. That is normal for a service written before " +
				"the workload behind it, but until something matches it has nowhere to send traffic.",
			Remediation: "Start the workload behind this service, or check that what the service selects matches " +
				"the labels that workload's instances actually carry.",
			Skill: SkillServiceNotServing,
		}),
		on(KindNetworkService, conds, networkingv1alpha.NetworkServiceReasonMultipleNetworks, ReasonInfo{
			Actionability: ActionabilityUser,
			Scope:         ScopeService,
			Explanation: "This service selects instances on more than one network. A service covers one network, " +
				"so rather than quietly pick one, Datum serves none of them.",
			Remediation: "Narrow what the service selects so everything it matches is on one network.",
			Skill:       SkillServiceNotServing,
		}),
		on(KindNetworkService, conds, reasonInvalidSelector, ReasonInfo{
			Actionability: ActionabilityUser,
			Scope:         ScopeService,
			Explanation:   "What this service selects cannot be read as a label selector, so nothing can match it.",
			Remediation:   "Fix the service's selector; the message names what is wrong with it.",
			Skill:         SkillServiceCreate,
		}),
		on(KindNetworkService, conds, reasonPending, notStarted(ScopeService, "this service")),
	)
}

var serviceCatalog = append(serviceResolution(),
	entry(KindNetworkService, networkingv1alpha.NetworkServiceMembersResolved, reasonResolved,
		healthy("The service has found the instances it selects.")),
	entry(KindNetworkService, networkingv1alpha.NetworkServiceReady, reasonReady,
		healthy("The service has at least one location taking traffic.")),
	entry(KindNetworkService, networkingv1alpha.NetworkServiceReady,
		networkingv1alpha.NetworkServiceReasonNoServingLocations, ReasonInfo{
			Actionability: ActionabilityUser,
			Scope:         ScopeService,
			Explanation: "The service has instances, and none of them says it is ready to serve, so it has " +
				"nowhere to send traffic. The network is fine; the instances are what to look at.",
			Remediation: "Find out why the workload's instances are not available: they may still be starting, " +
				"or failing.",
			Skill: SkillServiceNotServing,
		}),
)

var catalog = slices.Concat(networkCatalog, locationCatalog, interfaceCatalog, serviceCatalog)

var (
	byKey    map[Key]ReasonInfo
	byReason map[string][]ReasonInfo
)

func init() {
	byKey = make(map[Key]ReasonInfo, len(catalog))
	byReason = make(map[string][]ReasonInfo, len(catalog))
	for i := range catalog {
		catalog[i].ExpectedWithin = agenttime.HumanDuration(catalog[i].ExpectedDuration)
		byKey[keyOf(catalog[i])] = catalog[i]
		byReason[catalog[i].Reason] = append(byReason[catalog[i].Reason], catalog[i])
	}
}

func keyOf(info ReasonInfo) Key {
	return Key{Kind: info.Kind, ConditionType: info.ConditionType, Reason: info.Reason}
}

func ExplainReason(kind, conditionType, reason string) (ReasonInfo, bool) {
	info, ok := byKey[Key{Kind: kind, ConditionType: conditionType, Reason: reason}]
	return info, ok
}

func ExplainAnyReason(reason string) []ReasonInfo {
	return slices.Clone(byReason[reason])
}

func AllReasons() []ReasonInfo {
	return slices.Clone(catalog)
}

var pointerKeys = map[Key]struct{}{
	{KindNetworkBinding, networkingv1alpha.NetworkBindingReady, networkingv1alpha.NetworkBindingReasonNetworkContextNotReady}:                {},
	{KindNetworkInterfaceClaim, networkingv1alpha.NetworkInterfaceClaimReady, notPrefix + networkingv1alpha.NetworkInterfaceClaimBound}:      {},
	{KindNetworkInterfaceClaim, networkingv1alpha.NetworkInterfaceClaimReady, notPrefix + networkingv1alpha.NetworkInterfaceClaimAllocated}:  {},
	{KindNetworkInterfaceClaim, networkingv1alpha.NetworkInterfaceClaimReady, notPrefix + networkingv1alpha.NetworkInterfaceClaimPrepared}:   {},
	{KindNetworkInterfaceClaim, networkingv1alpha.NetworkInterfaceClaimReady, notPrefix + networkingv1alpha.NetworkInterfaceClaimProgrammed}: {},
}

func isPointer(kind, conditionType, reason string) bool {
	_, ok := pointerKeys[Key{kind, conditionType, reason}]
	return ok
}

func Failing(c metav1.Condition) bool {
	return c.Status != metav1.ConditionTrue
}

func ActionabilityAt(info ReasonInfo, lastTransition string, now time.Time) Actionability {
	if info.Actionability != ActionabilityTransient || info.ExpectedDuration <= 0 {
		return info.Actionability
	}
	elapsed, ok := agenttime.Age(lastTransition, now)
	if !ok || elapsed <= info.ExpectedDuration {
		return info.Actionability
	}
	return ActionabilityStalled
}
