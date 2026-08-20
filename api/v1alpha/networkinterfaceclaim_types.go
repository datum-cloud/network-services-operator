// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// NetworkInterfaceClaimBound reports that an interface is bound to the claim
	// and named in status.networkInterfaceRef.
	NetworkInterfaceClaimBound = "Bound"

	// NetworkInterfaceClaimAllocated reports that every requested address family,
	// and every requested class, holds an address.
	NetworkInterfaceClaimAllocated = "Allocated"

	// NetworkInterfaceClaimProgrammed reports that the data plane carries the
	// claimed addresses.
	NetworkInterfaceClaimProgrammed = "Programmed"

	// NetworkInterfaceClaimReady reports that the claim is bound, allocated, and
	// programmed. A workload that needs the network should wait on this one
	// condition rather than on the three it summarizes.
	NetworkInterfaceClaimReady = "Ready"
)

const (
	// NetworkInterfaceClaimReasonNetworkNotAvailableInLocation means the network
	// exists and has not reached this location yet, which is a different answer
	// from a consumer naming a network that does not exist.
	NetworkInterfaceClaimReasonNetworkNotAvailableInLocation = "NetworkNotAvailableInLocation"

	// NetworkInterfaceClaimReasonAddressFamilyNotCarried means the network does
	// not carry an address family the claim asked for.
	NetworkInterfaceClaimReasonAddressFamilyNotCarried = "AddressFamilyNotCarried"

	// NetworkInterfaceClaimReasonAddressFamiliesUnknown means the network reached
	// this location without saying which families it carries.
	NetworkInterfaceClaimReasonAddressFamiliesUnknown = "AddressFamiliesUnknown"

	// NetworkInterfaceClaimReasonProjectNamespaceNotFound means the namespace
	// addresses are allocated in does not exist in the project's control plane.
	// The platform provisions it with the project, so its absence says the
	// control plane is not bootstrapped rather than that it is slow to appear.
	NetworkInterfaceClaimReasonProjectNamespaceNotFound = "ProjectNamespaceNotFound"
)

// NetworkInterfaceAddressRequest asks for one address beyond the ones the
// interface holds inside its network, such as a public IPv4 address in front of
// a private one.
type NetworkInterfaceAddressRequest struct {
	// class is the IPAM class to allocate from, such as public-ipv4.
	//
	// A class names a kind of address, and the platform decides which pool and
	// prefix length serve it. A class never names a pool, a prefix length, or a
	// CIDR, so a class cannot be used to ask for a particular address.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Class string `json:"class"`
}

// NetworkInterfaceClaimSpec defines the desired state of NetworkInterfaceClaim.
// Every field states what the interface must be able to do, never which
// interface or which address to use.
//
// Most of the spec is immutable, because the addresses are allocated against
// it. To change one of those fields, delete the claim and create a new one,
// accepting that the workload gets new addresses unless the interface is
// retained.
//
// +kubebuilder:validation:XValidation:message="networkInterfaceName is immutable and cannot be set, changed, or cleared after creation",rule="has(self.networkInterfaceName) == has(oldSelf.networkInterfaceName) && (!has(self.networkInterfaceName) || self.networkInterfaceName == oldSelf.networkInterfaceName)"
// +kubebuilder:validation:XValidation:message="addresses is immutable and cannot be set, changed, or cleared after creation",rule="has(self.addresses) == has(oldSelf.addresses) && (!has(self.addresses) || self.addresses == oldSelf.addresses)"
type NetworkInterfaceClaimSpec struct {
	// network is the network the interface attaches to. The network must already
	// exist in the same namespace as the claim.
	//
	// Immutable. An interface that changed network would hold addresses from a
	// space it no longer belongs to, so move a workload by recreating the claim
	// against the other network.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:message="network is immutable and cannot be changed after creation",rule="self == oldSelf"
	Network LocalNetworkRef `json:"network"`

	// interfaceName is the device name the interface presents to the guest
	// operating system, such as eth0 or eth1. Set it when a workload has more
	// than one interface and the guest configuration names them.
	//
	// Immutable, because the guest is configured against it.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=15
	// +kubebuilder:default="eth0"
	// +kubebuilder:validation:XValidation:message="interfaceName is immutable and cannot be changed after creation",rule="self == oldSelf"
	InterfaceName string `json:"interfaceName,omitempty"`

	// attachmentMode is how the guest consumes this interface. Netns places it in
	// the workload's network namespace, which is what an ordinary container
	// expects. Hypervisor hands it to a hypervisor as a device, which is what a
	// virtual machine or microVM guest needs.
	//
	// It is copied to the bound interface and never interpreted here. Whoever
	// realizes the interface decides what each mode means on its data plane.
	//
	// Immutable, because the guest and the attachment are both built against it.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="Netns"
	// +kubebuilder:validation:XValidation:message="attachmentMode is immutable and cannot be changed after creation",rule="self == oldSelf"
	AttachmentMode NetworkInterfaceAttachmentMode `json:"attachmentMode,omitempty"`

	// ipFamilies are the address families the interface must carry, in priority
	// order. List [IPv6, IPv4] for a dual-stack interface. The first family
	// listed holds the interface's primary address, which is the one reported in
	// single-address fields such as an instance's network IP.
	//
	// Every family listed must be satisfiable or the claim does not bind. Asking
	// for a family the network does not carry fails the claim outright rather
	// than leaving it pending, and no partially addressed interface is ever
	// published.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:default={IPv6}
	// +kubebuilder:validation:XValidation:message="Each address family may be requested at most once",rule="self.all(f, self.exists_one(g, g == f))"
	// +kubebuilder:validation:XValidation:message="ipFamilies is immutable and cannot be changed after creation",rule="self == oldSelf"
	IPFamilies []IPFamily `json:"ipFamilies,omitempty"`

	// addresses request extra addresses by class, beyond the ones the interface
	// holds inside its network. Each appears in status.externalAddresses as a
	// bare address, mapped onto the interface address of the same family.
	//
	// Omit this field for ordinary private addressing, which is the common case.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=4
	// +kubebuilder:validation:XValidation:message="Each address class may be requested at most once",rule="self.all(a, self.exists_one(b, b.class == a.class))"
	Addresses []NetworkInterfaceAddressRequest `json:"addresses,omitempty"`

	// reclaimPolicy decides what becomes of the bound interface, and its
	// addresses, when this claim is deleted.
	//
	// Delete deletes the interface and returns its addresses to IPAM. A workload
	// recreated later comes back on different addresses.
	//
	// Retain keeps the interface, unbound and still holding its addresses, so a
	// later claim of this name binds it again and the workload returns to the
	// same addresses. Choose Retain when an address is published in DNS, allowed
	// through a firewall, or otherwise depended on from outside.
	//
	// A retained address is reserved, and billable, for as long as the interface
	// exists. Deleting the interface does not return it to the pool today, so
	// choose Retain for addresses worth holding rather than as a default.
	//
	// Both policies keep the addresses while the claim exists, including across
	// instance replacement. They differ only on scale-down and deletion.
	//
	// Immutable. An address keeps the policy it was allocated under, and a claim
	// asking for a policy the interface was not allocated under cannot bind it.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="Delete"
	// +kubebuilder:validation:XValidation:message="reclaimPolicy is immutable and cannot be changed after creation",rule="self == oldSelf"
	ReclaimPolicy NetworkInterfaceReclaimPolicy `json:"reclaimPolicy,omitempty"`

	// networkInterfaceName binds one specific interface by name, instead of the
	// interface named after this claim. The named interface must already carry
	// every family and class this claim asks for, under the same reclaim policy,
	// and must not be held by another claim.
	//
	// Leave it empty, which is the normal case. The claim then binds the
	// interface of its own name, retained by an earlier claim, or creates one.
	//
	// Immutable, including from empty to set. Rebinding a workload to a different
	// interface means a new claim.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	NetworkInterfaceName string `json:"networkInterfaceName,omitempty"`
}

// NetworkInterfaceClaimStatus defines the observed state of
// NetworkInterfaceClaim. It repeats the bound interface's addresses so a
// consumer reads one object rather than following the reference.
type NetworkInterfaceClaimStatus struct {
	// networkInterfaceRef is the interface bound to this claim, in the same
	// namespace. Read it to reach fields the claim does not repeat, such as the
	// MTU and the data-plane attachment.
	//
	// +kubebuilder:validation:Optional
	NetworkInterfaceRef *LocalNetworkInterfaceRef `json:"networkInterfaceRef,omitempty"`

	// addresses are the addresses the bound interface holds inside its network,
	// each with its prefix length and, once the location has a subnet, its
	// gateway. They are copied from the interface, which remains the source of
	// truth.
	//
	// +kubebuilder:validation:Optional
	Addresses []NetworkInterfaceAddress `json:"addresses,omitempty"`

	// externalAddresses are the addresses the bound interface is reachable at from
	// outside the network, one per class the claim requested. Each is a bare
	// address with no prefix length. They are copied from the interface.
	//
	// +kubebuilder:validation:Optional
	ExternalAddresses []NetworkInterfaceExternalAddress `json:"externalAddresses,omitempty"`

	// conditions report the current state of the claim. Wait on Ready, which is
	// true once the claim is bound, its addresses are allocated, and the data
	// plane carries them.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NetworkInterfaceClaim asks for an interface on a network. It is the resource
// a user creates. The operator finds or creates a NetworkInterface that
// satisfies it, allocates the addresses, and reports them in status.
//
// A claim describes what the interface must be able to do, never which
// interface or address to use. One claim holds at most one interface, and one
// interface is held by at most one claim.
//
// A claim's name is what makes addresses stable. It names the slot in a
// workload rather than the instance filling it, so an instance replaced by
// another that asks for the same claim name comes back on the same interface
// and the same addresses. What happens when the claim itself is deleted is
// spec.reclaimPolicy.
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Network",type=string,JSONPath=".spec.network.name"
// +kubebuilder:printcolumn:name="Interface",type=string,JSONPath=".status.networkInterfaceRef.name"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
type NetworkInterfaceClaim struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec NetworkInterfaceClaimSpec `json:"spec,omitempty"`

	// +kubebuilder:default={conditions:{{type:"Bound",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"},{type:"Allocated",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"},{type:"Programmed",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"},{type:"Ready",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"}}}
	Status NetworkInterfaceClaimStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NetworkInterfaceClaimList contains a list of NetworkInterfaceClaim.
type NetworkInterfaceClaimList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkInterfaceClaim `json:"items"`
}

// NetworkInterfaceClaimTemplate describes an interface a workload needs, from
// which one NetworkInterfaceClaim is created per slot. It is embedded in a
// workload API the way a StatefulSet embeds volumeClaimTemplates, and serves
// the same purpose: the workload declares the interface once, and every slot
// gets a claim of its own that outlives the instance filling it.
//
// Each claim is named from the template and the slot, and both parts stay the
// same for the life of that slot, so a replacement instance derives the same
// name, finds the claim already there, and returns to the addresses it was
// already holding.
type NetworkInterfaceClaimTemplate struct {
	// metadata is the standard object metadata for the claims this template
	// produces.
	//
	// The name distinguishes one interface of a slot from another and becomes
	// part of every claim's name. Labels and annotations are copied onto every
	// claim produced. No other field is honoured.
	//
	// +kubebuilder:validation:Optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is the claim created for each slot. It is copied onto every claim the
	// template produces, so every slot gets the same network, families, classes,
	// and reclaim policy.
	//
	// +kubebuilder:validation:Required
	Spec NetworkInterfaceClaimSpec `json:"spec"`
}
