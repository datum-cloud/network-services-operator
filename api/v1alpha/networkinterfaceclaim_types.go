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

	// NetworkInterfaceClaimPrepared reports that the data plane's pre-Pod
	// artifacts for the bound interface exist, so a workload that consumes it can
	// be created.
	//
	// This is the condition to gate workload creation on. It becomes true before
	// any workload exists, which is what makes waiting on it safe.
	NetworkInterfaceClaimPrepared = "Prepared"

	// NetworkInterfaceClaimProgrammed reports that the data plane carries the
	// claimed addresses.
	//
	// Never gate workload creation on this one. It becomes true when the
	// interface is attached, which happens while the workload's sandbox is being
	// created, so anything that withholds the workload until it is true waits for
	// something its own waiting prevents.
	NetworkInterfaceClaimProgrammed = "Programmed"

	// NetworkInterfaceClaimReady reports that the claim is bound, allocated,
	// prepared, and programmed. A workload that needs the network should wait on this one
	// condition rather than on the three it summarizes.
	NetworkInterfaceClaimReady = "Ready"
)

// NetworkInterfaceClaimDetachFinalizer holds a physical claim until the
// realizer has taken the claim's network off its switch port. Only the
// operator adds it, and only the realizer, or the operator for a claim no
// port ever carried, removes it.
const NetworkInterfaceClaimDetachFinalizer = "networking.datumapis.com/physical-detach"

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

	// NetworkInterfaceClaimReasonSubnetGatewayUnusable means the location
	// published a subnet for an address family the claim holds, and no gateway
	// can be derived from it. An interface given addresses and no gateway routes
	// nothing, so this is reported rather than left for an operator to notice on
	// the interface.
	NetworkInterfaceClaimReasonSubnetGatewayUnusable = "SubnetGatewayUnusable"

	// NetworkInterfaceClaimReasonNetworkContextTerminating means the network's
	// presence in this location is being deleted. An address handed out against
	// it would pin a range that is being given back.
	NetworkInterfaceClaimReasonNetworkContextTerminating = "NetworkContextTerminating"

	// NetworkInterfaceClaimReasonSubnetTerminating means the subnet this
	// location is addressed from is being deleted, for the same reason.
	NetworkInterfaceClaimReasonSubnetTerminating = "SubnetTerminating"

	// NetworkInterfaceClaimReasonReleasing means the claim is being deleted and
	// its interface released, so it no longer offers one to a workload.
	NetworkInterfaceClaimReasonReleasing = "Releasing"

	// NetworkInterfaceClaimReasonReleaseUnverified means a physical claim is
	// being deleted and IPAM still holds one of its addresses, so the claim
	// stays until IPAM no longer does.
	NetworkInterfaceClaimReasonReleaseUnverified = "ReleaseUnverified"

	// NetworkInterfaceClaimReasonProjectNamespaceNotFound means the namespace
	// addresses are allocated in does not exist in the project's control plane.
	// The platform provisions it with the project, so its absence says the
	// control plane is not bootstrapped rather than that it is slow to appear.
	NetworkInterfaceClaimReasonProjectNamespaceNotFound = "ProjectNamespaceNotFound"

	// NetworkInterfaceClaimReasonAttachmentPointHeld means the switch port the
	// physical claim names is held by claims it cannot share the port with, so
	// the claim waits and nothing is allocated for it. The provider reads the
	// port's SwitchPortHold to see what holds it.
	NetworkInterfaceClaimReasonAttachmentPointHeld = "AttachmentPointHeld"

	// NetworkInterfaceClaimReasonPortChanging means the physical realizer is
	// changing what the claim's switch port carries, so the port is between
	// states until the realizer reads it back.
	NetworkInterfaceClaimReasonPortChanging = "PortChanging"

	// NetworkInterfaceClaimReasonBootAttachmentActive means a boot claim holds
	// the claim's switch port, so the port carries the boot network and not
	// this claim's network until the boot claim is deleted. The claim keeps
	// its addresses meanwhile.
	NetworkInterfaceClaimReasonBootAttachmentActive = "BootAttachmentActive"

	// NetworkInterfaceClaimReasonPortCarriesNetwork means the switch port
	// carries the claim's network, as read back from the switch.
	NetworkInterfaceClaimReasonPortCarriesNetwork = "PortCarriesNetwork"

	// NetworkInterfaceClaimReasonAttachmentPointAccepted means the physical
	// realizer accepted the claim's switch port and can build its segment.
	NetworkInterfaceClaimReasonAttachmentPointAccepted = "AttachmentPointAccepted"

	// NetworkInterfaceClaimReasonAwaitingDetach means a physical claim is being
	// deleted and the realizer has not yet taken its network off the switch
	// port, so its addresses stay held.
	NetworkInterfaceClaimReasonAwaitingDetach = "AwaitingDetach"
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
// +kubebuilder:validation:XValidation:message="attachmentPoint is immutable and cannot be set, changed, or cleared after creation",rule="has(self.attachmentPoint) == has(oldSelf.attachmentPoint) && (!has(self.attachmentPoint) || self.attachmentPoint == oldSelf.attachmentPoint)"
// +kubebuilder:validation:XValidation:message="purpose is immutable and cannot be set, changed, or cleared after creation",rule="has(self.purpose) == has(oldSelf.purpose) && (!has(self.purpose) || self.purpose == oldSelf.purpose)"
// +kubebuilder:validation:XValidation:message="isolation is immutable and cannot be set, changed, or cleared after creation",rule="has(self.isolation) == has(oldSelf.isolation) && (!has(self.isolation) || self.isolation == oldSelf.isolation)"
// +kubebuilder:validation:XValidation:message="attachmentPoint is required for attachmentMode Physical and not allowed for any other mode",rule="has(self.attachmentPoint) == (has(self.attachmentMode) && self.attachmentMode == 'Physical')"
// +kubebuilder:validation:XValidation:message="purpose is only allowed for attachmentMode Physical",rule="!has(self.purpose) || (has(self.attachmentMode) && self.attachmentMode == 'Physical')"
// +kubebuilder:validation:XValidation:message="isolation is only allowed for attachmentMode Physical",rule="!has(self.isolation) || (has(self.attachmentMode) && self.attachmentMode == 'Physical')"
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
	// virtual machine or microVM guest needs. HypervisorDeclared also hands it
	// to a hypervisor, and additionally has the realizer state the device to
	// that hypervisor instead of letting it discover the device from the node.
	// Physical is a physical NIC that reaches its network through the switch
	// port named in attachmentPoint.
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

	// attachmentPoint is the physical NIC and the switch port it is cabled to.
	// It is required for attachmentMode Physical and not allowed for any other
	// mode. It is copied to the bound interface and never checked against any
	// inventory, so only a provider writes it.
	//
	// Immutable. A server recabled to another port needs a new claim.
	//
	// +kubebuilder:validation:Optional
	AttachmentPoint *NetworkInterfaceAttachmentPoint `json:"attachmentPoint,omitempty"`

	// purpose is what a Physical claim is for. Boot attaches the port to a
	// boot network for as long as the claim exists, ahead of any other claim on
	// the port. Left unset, the claim attaches the port to its network whenever
	// no boot claim names the port.
	//
	// Only allowed for attachmentMode Physical. Immutable.
	//
	// +kubebuilder:validation:Optional
	Purpose NetworkInterfacePurpose `json:"purpose,omitempty"`

	// isolation is whether a Physical claim's port shares its network's segment
	// with the network's other members. Private gives the port a segment of its
	// own that reaches only the network's gateway. Shared joins the network's
	// shared segment. Left unset, a boot claim is private and any other claim is
	// shared; nothing is written back to the claim.
	//
	// Only allowed for attachmentMode Physical. Immutable.
	//
	// +kubebuilder:validation:Optional
	Isolation NetworkInterfaceIsolation `json:"isolation,omitempty"`

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

	// egress is what this interface reaches outside the platform. Only
	// Inherit is accepted, which follows the network's declaration.
	//
	// The field is reserved so the default is settled before consumers depend
	// on it: an interface written today records Inherit, so accepting Enabled
	// and Disabled later changes no existing interface.
	//
	// Unlike the rest of this spec it is mutable. No address is allocated
	// against it, and a rule pinning the only accepted value would have to be
	// dropped again when per-interface control widens the enum.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default={internet:{mode:Inherit}}
	Egress *NetworkInterfaceClaimEgress `json:"egress,omitempty"`
}

// NetworkInterfaceClaimEgress declares the outbound paths an interface carries.
type NetworkInterfaceClaimEgress struct {
	// internet is whether this interface reaches destinations outside the
	// platform.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default={mode:Inherit}
	Internet *NetworkInterfaceClaimInternetEgress `json:"internet,omitempty"`
}

// NetworkInterfaceClaimInternetEgressMode is whether an interface reaches the
// internet.
//
// +kubebuilder:validation:Enum=Inherit
type NetworkInterfaceClaimInternetEgressMode string

const (
	// NetworkInterfaceClaimInternetEgressInherit follows the network's
	// declaration. It is the only accepted value until per-interface control
	// ships.
	NetworkInterfaceClaimInternetEgressInherit NetworkInterfaceClaimInternetEgressMode = "Inherit"
)

// NetworkInterfaceClaimInternetEgress reserves per-interface internet access.
type NetworkInterfaceClaimInternetEgress struct {
	// mode is whether this interface reaches the internet. Only Inherit is
	// accepted, which follows the network's declaration.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=Inherit
	Mode NetworkInterfaceClaimInternetEgressMode `json:"mode,omitempty"`
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

	// vlanID is the VLAN a Physical claim's network arrives on at its switch
	// port, copied from the bound interface's status by the physical realizer.
	// Once Prepared is true an absent vlanID means the network arrives
	// untagged.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=4094
	VLANID *int32 `json:"vlanID,omitempty"`

	// conditions report the current state of the claim. Wait on Ready, which is
	// true once the claim is bound, its addresses are allocated, the data plane
	// is prepared for a workload, and the data plane carries the addresses.
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

	// +kubebuilder:default={conditions:{{type:"Bound",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"},{type:"Allocated",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"},{type:"Prepared",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"},{type:"Programmed",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"},{type:"Ready",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"}}}
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
