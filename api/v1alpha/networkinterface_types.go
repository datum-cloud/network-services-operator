// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NetworkInterfaceReclaimPolicy decides what becomes of an interface, and the
// addresses it holds, when the claim bound to it is deleted.
//
// The two policies differ only when a workload goes away: on scale-down, on
// deletion, or whenever a claim is removed. While a workload is running, or
// while an instance is being replaced, both policies keep the addresses.
//
// +kubebuilder:validation:Enum=Delete;Retain
type NetworkInterfaceReclaimPolicy string

const (
	// NetworkInterfaceReclaimPolicyDelete deletes the interface and returns its
	// addresses to IPAM. A workload recreated later gets new addresses.
	NetworkInterfaceReclaimPolicyDelete NetworkInterfaceReclaimPolicy = "Delete"

	// NetworkInterfaceReclaimPolicyRetain keeps the interface and its addresses
	// after the claim is gone. The interface returns to the Available phase and
	// keeps holding the addresses, so a later claim of the same name binds the
	// same interface and the workload comes back on the same addresses. The
	// addresses stay reserved, and billable, for as long as the interface
	// exists. Deleting the interface releases its claim on them but does not
	// return them to the pool today, so a retained address is reclaimed by an
	// operator rather than automatically.
	NetworkInterfaceReclaimPolicyRetain NetworkInterfaceReclaimPolicy = "Retain"
)

// NetworkInterfaceAttachmentMode is how the guest consumes the NIC. It is set
// by the consumer, carried by the operator, and acted on by whoever realizes
// the interface.
//
// +kubebuilder:validation:Enum=Netns;Hypervisor
type NetworkInterfaceAttachmentMode string

const (
	// NetworkInterfaceAttachmentModeNetns means the interface is placed in the
	// workload's network namespace, which is what an ordinary container expects.
	NetworkInterfaceAttachmentModeNetns NetworkInterfaceAttachmentMode = "Netns"

	// NetworkInterfaceAttachmentModeHypervisor means the interface is handed to a
	// hypervisor as a device rather than placed in a namespace, which is what a
	// virtual machine or microVM guest expects.
	NetworkInterfaceAttachmentModeHypervisor NetworkInterfaceAttachmentMode = "Hypervisor"
)

// NetworkInterfacePhase reports whether an interface is held by a claim.
//
// +kubebuilder:validation:Enum=Available;Bound
type NetworkInterfacePhase string

const (
	// NetworkInterfacePhaseAvailable means the interface still holds its addresses
	// but no claim is bound to it. Retained interfaces wait here for a claim of
	// the matching name.
	NetworkInterfacePhaseAvailable NetworkInterfacePhase = "Available"

	// NetworkInterfacePhaseBound means the claim named in spec.claimRef holds the
	// interface.
	NetworkInterfacePhaseBound NetworkInterfacePhase = "Bound"
)

const (
	// NetworkInterfaceAllocated reports that every address the interface must
	// carry is allocated and recorded in spec.
	NetworkInterfaceAllocated = "Allocated"

	// NetworkInterfacePrepared reports that the data plane's pre-Pod artifacts
	// for this interface exist, so a workload that consumes it can be created.
	//
	// This is the condition to gate workload creation on. It becomes true before
	// any workload exists, which is what makes waiting on it safe.
	NetworkInterfacePrepared = "Prepared"

	// NetworkInterfaceProgrammed reports that the data plane carries the
	// interface's addresses. Traffic flows only once this is true.
	//
	// Never gate workload creation on this one. It becomes true when the
	// interface is attached, which happens while the workload's sandbox is being
	// created, so anything that withholds the workload until it is true waits for
	// something its own waiting prevents.
	NetworkInterfaceProgrammed = "Programmed"

	// NetworkInterfaceHolderAvailable reports that whatever holds this interface
	// says it is available to serve. It is written by the holder named in the
	// held-by label and never by the networking operator, which has no idea what
	// a holder is.
	//
	// This is the condition a service reads to decide whether a member takes
	// traffic. It says nothing about the interface: an interface with every
	// address allocated and programmed still carries this false while whatever
	// is behind it is starting, failing, or shutting down.
	//
	// Unrelated to status.phase, which reports whether a claim holds the
	// interface at all. A phase of Available means no claim holds it, which is
	// the opposite of anything being available to serve.
	NetworkInterfaceHolderAvailable = "HolderAvailable"
)

const (
	// NetworkInterfaceReasonHolderAvailable is what a holder reports on
	// HolderAvailable once it is serving.
	NetworkInterfaceReasonHolderAvailable = "HolderAvailable"

	// NetworkInterfaceReasonHolderUnavailable is what a holder reports on
	// HolderAvailable while it is starting, failing, or shutting down.
	NetworkInterfaceReasonHolderUnavailable = "HolderUnavailable"
)

// NetworkInterfaceAddress is an address the interface holds inside its network.
// These are the addresses configured on the NIC itself, and they always carry a
// prefix length.
type NetworkInterfaceAddress struct {
	// family is the address family of this entry.
	//
	// +kubebuilder:validation:Required
	Family IPFamily `json:"family"`

	// address is the address the interface holds, in CIDR notation, such as
	// 10.128.0.2/32 or 2001:db8:a001::1/128.
	//
	// For IPv6 this may be a block delegated to the interface rather than a
	// single address, such as 2001:db8:a001::/96. The interface owns the whole
	// block and assigns within it.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=45
	Address string `json:"address"`

	// gateway is the next hop the interface routes through for this family, such
	// as 10.128.0.1. It is resolved from the subnet backing the network in this
	// location, so nothing has to read the subnet to configure the NIC. It is
	// empty until that subnet exists.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=45
	Gateway string `json:"gateway,omitempty"`

	// primary marks the address projected into single-address fields, such as an
	// instance's reported network IP.
	//
	// Exactly one address is primary for the interface as a whole, not one per
	// family. It is the address of the first family the claim listed in
	// spec.ipFamilies.
	//
	// +kubebuilder:validation:Optional
	Primary bool `json:"primary,omitempty"`

	// class is the IPAM class this address was allocated from, such as
	// private-ipv6. It is empty for the addresses a claim requests by family
	// rather than by class.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=63
	Class string `json:"class,omitempty"`
}

// NetworkInterfaceExternalAddress is an address reachable from outside the
// network, mapped onto an address the interface holds inside it. A public IPv4
// address in front of a private address is the usual case.
//
// Unlike an interface address, an external address is a bare address with no
// prefix length, such as 203.0.113.10, because nothing configures it on the
// NIC. The data plane maps it onto the interface address of the same family.
type NetworkInterfaceExternalAddress struct {
	// family is the address family of this entry.
	//
	// +kubebuilder:validation:Required
	Family IPFamily `json:"family"`

	// address is the externally reachable address, such as 203.0.113.10. It
	// carries no prefix length.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=45
	Address string `json:"address"`

	// class is the IPAM class this address was allocated from, such as
	// public-ipv4. It matches the class the claim requested in spec.addresses.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Class string `json:"class"`
}

// NetworkInterfaceClaimRef identifies the claim holding an interface.
type NetworkInterfaceClaimRef struct {
	// name is the name of the NetworkInterfaceClaim, in the same namespace as the
	// interface. A claim name stays with the workload slot it serves, so a
	// replacement instance binds this same interface and its addresses.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// LocalNetworkInterfaceRef references a NetworkInterface in the same namespace.
type LocalNetworkInterfaceRef struct {
	// name is the network interface name.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// NetworkInterfaceAttachmentRef references the provider resource realizing an
// interface on the data plane, such as an instance NIC attachment. It tells an
// operator what is carrying the interface, and it is written by the provider
// rather than by a user.
type NetworkInterfaceAttachmentRef struct {
	// apiGroup is the API group of the referent, such as
	// compute.datumapis.com.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	APIGroup string `json:"apiGroup"`

	// kind is the kind of the referent.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Kind string `json:"kind"`

	// name is the name of the referent.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// NetworkInterfaceSpec defines the desired state of NetworkInterface. It is
// written by the operator when a claim is fulfilled, and it carries everything
// a provider needs to configure a NIC without reading any other resource.
type NetworkInterfaceSpec struct {
	// network is the network this interface belongs to, in the same namespace as
	// the interface. It comes from the claim and does not change.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:message="network is immutable and cannot be changed after creation",rule="self == oldSelf"
	Network LocalNetworkRef `json:"network"`

	// claimRef is the claim currently holding this interface. It is empty while a
	// retained interface waits, unbound, for a claim of its name to return.
	//
	// +kubebuilder:validation:Optional
	ClaimRef *NetworkInterfaceClaimRef `json:"claimRef,omitempty"`

	// interfaceName is the device name the interface presents to the guest
	// operating system, such as eth0 or eth1. It comes from the claim.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=15
	// +kubebuilder:default="eth0"
	InterfaceName string `json:"interfaceName,omitempty"`

	// attachmentMode is how the guest consumes this interface. It comes from the
	// claim, and the operator carries it without interpreting it.
	//
	// Netns places the interface in the workload's network namespace. Hypervisor
	// hands it to a hypervisor as a device, which is what a virtual machine or
	// microVM guest needs.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="Netns"
	AttachmentMode NetworkInterfaceAttachmentMode `json:"attachmentMode,omitempty"`

	// mtu is the MTU, in bytes, the interface must be configured with. It is
	// resolved from the network, so a provider never has to read the network to
	// configure the NIC.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1300
	// +kubebuilder:validation:Maximum=8856
	MTU int32 `json:"mtu,omitempty"`

	// addresses are the addresses the interface holds inside its network, at most
	// one per address family, exactly one of them primary. Each carries a prefix
	// length and, once the location has a subnet, the gateway to route through.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=4
	// +kubebuilder:validation:XValidation:message="Exactly one address must be primary",rule="size(self) == 0 || self.filter(a, has(a.primary) && a.primary).size() == 1"
	// +kubebuilder:validation:XValidation:message="Only one address may be held per address family",rule="self.all(a, self.exists_one(b, b.family == a.family))"
	Addresses []NetworkInterfaceAddress `json:"addresses,omitempty"`

	// externalAddresses are the addresses the interface is reachable at from
	// outside the network, each mapped onto the interface address of the same
	// family. They come from the classes the claim requested, and they are absent
	// for a workload that only needs private addressing.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=4
	// +kubebuilder:validation:XValidation:message="External addresses must be unique",rule="self.all(a, self.exists_one(b, b.address == a.address))"
	// +kubebuilder:validation:XValidation:message="Only one external address may be held per address class",rule="self.all(a, self.exists_one(b, b.class == a.class))"
	ExternalAddresses []NetworkInterfaceExternalAddress `json:"externalAddresses,omitempty"`

	// reclaimPolicy decides what becomes of this interface, and its addresses,
	// when the claim holding it is deleted. It comes from the claim, and a claim
	// asking for a different policy cannot bind this interface.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="Delete"
	ReclaimPolicy NetworkInterfaceReclaimPolicy `json:"reclaimPolicy,omitempty"`
}

// NetworkInterfaceStatus defines the observed state of NetworkInterface: which
// claim holds it, what realizes it on the data plane, and whether programming
// has succeeded.
type NetworkInterfaceStatus struct {
	// phase reports whether a claim holds the interface. Bound means the claim in
	// spec.claimRef holds it. Available means it is retained and holding its
	// addresses with no claim bound.
	//
	// +kubebuilder:validation:Optional
	Phase NetworkInterfacePhase `json:"phase,omitempty"`

	// networkContextRef is the network's presence in this location, resolved or
	// created while fulfilling the claim. It is a breadcrumb for operators
	// tracing where a network landed, and nothing needs it to configure a NIC.
	//
	// +kubebuilder:validation:Optional
	NetworkContextRef *LocalNetworkContextRef `json:"networkContextRef,omitempty"`

	// attachmentRef is the data-plane resource realizing this interface. The
	// provider sets it once an attachment exists.
	//
	// +kubebuilder:validation:Optional
	AttachmentRef *NetworkInterfaceAttachmentRef `json:"attachmentRef,omitempty"`

	// vpc is the base62 identifier of the VPC backing this network in this
	// location, matching the identifier the fabric keys on. The provider records
	// it when the attachment is programmed.
	//
	// +kubebuilder:validation:Optional
	VPC string `json:"vpc,omitempty"`

	// conditions report the current state of the interface. Allocated means every
	// address is held. Prepared means the data plane is ready for a workload to
	// consume it. Programmed means the data plane carries the addresses.
	// HolderAvailable means whatever holds the interface reports itself available
	// to serve, and it is the only one of the four a service reads.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NetworkInterface is an interface on a network, together with the addresses it
// holds. It is the unit that owns addresses: as long as the interface exists,
// its addresses stay allocated to it.
//
// You do not create a NetworkInterface. Ask for one with a
// NetworkInterfaceClaim, and the operator creates the interface, allocates its
// addresses, and binds the two. A provider then reads the interface to
// configure a NIC, and reports what it programmed in status.
//
// An interface outlives the instance using it. Whether it outlives the claim
// that asked for it depends on spec.reclaimPolicy.
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Network",type=string,JSONPath=".spec.network.name"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Claim",type=string,JSONPath=".spec.claimRef.name"
// +kubebuilder:printcolumn:name="Allocated",type=string,JSONPath=`.status.conditions[?(@.type=="Allocated")].status`
// +kubebuilder:printcolumn:name="Prepared",type=string,JSONPath=`.status.conditions[?(@.type=="Prepared")].status`
// +kubebuilder:printcolumn:name="Programmed",type=string,JSONPath=`.status.conditions[?(@.type=="Programmed")].status`
// +kubebuilder:printcolumn:name="HolderAvailable",type=string,JSONPath=`.status.conditions[?(@.type=="HolderAvailable")].status`
type NetworkInterface struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec NetworkInterfaceSpec `json:"spec,omitempty"`

	// +kubebuilder:default={conditions:{{type:"Allocated",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"},{type:"Prepared",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"},{type:"Programmed",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"},{type:"HolderAvailable",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"}}}
	Status NetworkInterfaceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NetworkInterfaceList contains a list of NetworkInterface.
type NetworkInterfaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkInterface `json:"items"`
}

const (
	// NetworkInterfaceProjectionLabel marks a copy of an interface published for
	// visibility outside the cell holding the original. Nothing configures a NIC
	// from a copy, and the cell never reads one back.
	NetworkInterfaceProjectionLabel = "networking.datumapis.com/network-interface-projection"

	// NetworkInterfaceLocationLabel names the location whose cell holds the
	// original. It tells a consumer where the interface is, and it tells the cell
	// which copies are its own to collect.
	NetworkInterfaceLocationLabel = "networking.datumapis.com/location"

	// NetworkInterfaceHolderLabel names the claim holding the interface. It is a
	// label rather than a reference because the claim lives in the cell and does
	// not exist where a copy is published.
	NetworkInterfaceHolderLabel = "networking.datumapis.com/held-by"

	// NetworkInterfaceSourceNamespaceLabel names the namespace the copy was
	// published from, so a collector can find the source of a copy it is looking
	// at.
	NetworkInterfaceSourceNamespaceLabel = "networking.datumapis.com/source-namespace"
)
