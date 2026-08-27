// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:Enum=IPv4;IPv6
type IPFamily string

const (
	IPv4Protocol IPFamily = "IPv4"
	IPv6Protocol IPFamily = "IPv6"
)

// NetworkSpec defines the desired state of a Network
type NetworkSpec struct {

	// IPAM settings for the network.
	//
	// +kubebuilder:validation:Required
	IPAM NetworkIPAM `json:"ipam,omitempty"`

	// IP Families to permit on a network. Defaults to IPv6.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default={IPv6}
	IPFamilies []IPFamily `json:"ipFamilies,omitempty"`

	// Network MTU. May be between 1300 and 8856.
	//
	// +kubebuilder:validation:Minimum=1300
	// +kubebuilder:validation:Maximum=8856
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=1460
	MTU int32 `json:"mtu,omitempty"`
}

type NetworkIPAMMode string

const (
	// Automatically allocate subnets in the network
	NetworkIPAMModeAuto NetworkIPAMMode = "Auto"

	// Leverage allocation policies or manually created subnets
	NetworkIPAMModePolicy NetworkIPAMMode = "Policy"
)

type NetworkIPAM struct {
	// IPAM mode
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Auto;Policy
	Mode NetworkIPAMMode `json:"mode"`

	// IPv4 range to use in auto mode networks. Defaults to 10.128.0.0/9.
	//
	// +kubebuilder:validation:Optional
	IPV4Range *string `json:"ipv4Range,omitempty"`

	// IPv6 range to use in auto mode networks. Defaults to a /48 allocated from `fd20::/20`.
	//
	// +kubebuilder:validation:Optional
	IPV6Range *string `json:"ipv6Range,omitempty"`
}

const (
	// NetworkIPAMAllocated reports whether IPAM holds the network's address
	// space.
	NetworkIPAMAllocated = "IPAMAllocated"

	// NetworkReady reports whether the network holds everything it needs to be
	// used. A network addressed from the tenant pool is ready once IPAM holds
	// its range; one that claims no address space has nothing to wait for.
	NetworkReady = "Ready"

	// NetworkReadyReasonReady means the network is ready for use.
	NetworkReadyReasonReady = "Ready"

	// NetworkReasonProjectNamespaceNotFound means the namespace the platform
	// provisions with a project is absent from its control plane, so nothing
	// can be allocated for it.
	NetworkReasonProjectNamespaceNotFound = "ProjectNamespaceNotFound"

	// NetworkReasonProjectUnresolved means the network's namespace names no
	// project, so no IPAM request can be addressed on its behalf.
	NetworkReasonProjectUnresolved = "ProjectUnresolved"

	// NetworkReasonRangeOccupied means the network's range cannot be given back
	// while addresses are still allocated inside it. The interfaces holding
	// them have to go first.
	NetworkReasonRangeOccupied = "RangeOccupied"

	// NetworkReasonRangeUnsupported means IPAM did not keep the request for a
	// range, so it would answer with a block from inside one. A block is not
	// the network's range and the addresses it hands out do not lie in it.
	NetworkReasonRangeUnsupported = "RangeUnsupported"
)

const (
	// NetworkFabricIdentityAllocated reports whether the network holds the
	// identity the fabric knows it by. The type is bare because the fabric
	// reads this condition as the answer to "does this network have an
	// identity", not as one allocation among several.
	NetworkFabricIdentityAllocated = "Allocated"

	// NetworkFabricIdentityReasonAllocated means the network holds an identity.
	NetworkFabricIdentityReasonAllocated = "Allocated"

	// NetworkFabricIdentityReasonPending means nothing has been allocated yet
	// and the reason is not yet one of the ones below.
	NetworkFabricIdentityReasonPending = "Pending"

	// NetworkFabricIdentityReasonIdentitySpaceUnavailable means the identity
	// space did not answer, so the network has no identity to carry. It is
	// retried.
	NetworkFabricIdentityReasonIdentitySpaceUnavailable = "IdentitySpaceUnavailable"

	// NetworkFabricIdentityReasonIdentityUnusable means the identity space
	// answered with a block the identifier cannot be read out of. Handing out
	// the zero block is the case an operator hits first: zero is what an
	// unallocated network reads as, so it can never be an allocation.
	NetworkFabricIdentityReasonIdentityUnusable = "IdentityUnusable"
)

// NetworkStatus defines the observed state of Network
type NetworkStatus struct {
	// Represents the observations of a network's current state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// IPAM reports the address space IPAM holds for this network.
	//
	// +kubebuilder:validation:Optional
	IPAM *NetworkIPAMStatus `json:"ipam,omitempty"`

	// FabricIdentity is the identity the fabric knows this network by,
	// allocated once, platform-wide, and the same in every location the network
	// reaches. What consumes it derives the network's BGP Route Target from it,
	// which is what makes two locations of one network import each other's
	// routes rather than behave as two networks that share a name.
	//
	// It is an integer rather than an encoded string because the consumer
	// builds `ASN:<identity>`, and it is 32 bits wide because that is what
	// survives into the Route Target. A wider value would be uniqueness the
	// platform believes it has and the fabric does not.
	//
	// Zero means unallocated, so an unset field and a real allocation never
	// read alike. Once set it never changes: the fabric embeds it in import
	// policy in every location the network reaches, so a network that changed
	// identity would be a different network to everything already carrying its
	// traffic.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4294967295
	// +kubebuilder:validation:XValidation:rule="oldSelf == 0 || self == oldSelf",message="fabricIdentity is immutable once allocated"
	FabricIdentity int64 `json:"fabricIdentity,omitempty"`
}

// NetworkIPAMStatus reports what IPAM holds for a network.
type NetworkIPAMStatus struct {
	// IPv6Prefix is the /48 this network was assigned from the platform's
	// tenant ULA pool. Every subnet and endpoint address in the network is
	// carved from it.
	//
	// +kubebuilder:validation:Optional
	IPv6Prefix string `json:"ipv6Prefix,omitempty"`

	// IPv6PrefixRef names what holds the prefix in IPAM, so the allocation can
	// be audited and released.
	//
	// +kubebuilder:validation:Optional
	IPv6PrefixRef *NetworkPrefixRef `json:"ipv6PrefixRef,omitempty"`
}

// NetworkPrefixRef names the IPAM objects backing a network's prefix.
type NetworkPrefixRef struct {
	// Project is the control plane the objects live in.
	//
	// +kubebuilder:validation:Optional
	Project string `json:"project,omitempty"`

	// Namespace is the project namespace holding the claim.
	//
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`

	// ClaimName is the IPClaim this operator holds against the prefix.
	// Deleting it releases what the operator holds.
	//
	// +kubebuilder:validation:Optional
	ClaimName string `json:"claimName,omitempty"`

	// PoolName is the IPPool IPAM provisioned for the prefix. Subnet and
	// endpoint addresses are drawn from it.
	//
	// +kubebuilder:validation:Optional
	PoolName string `json:"poolName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="IPv6Prefix",type="string",JSONPath=".status.ipam.ipv6Prefix"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].reason"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="IPFamilies",type="string",JSONPath=".spec.ipFamilies",priority=1
// +kubebuilder:printcolumn:name="IPAM",type="string",JSONPath=".spec.ipam.mode",priority=1
// +kubebuilder:printcolumn:name="MTU",type="integer",JSONPath=".spec.mtu",priority=1
// +kubebuilder:printcolumn:name="FabricIdentity",type="integer",JSONPath=".status.fabricIdentity",priority=1

// Network is the Schema for the networks API
type Network struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec NetworkSpec `json:"spec,omitempty"`

	// +kubebuilder:default={conditions:{{type:"Ready",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"}}}
	Status NetworkStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NetworkList contains a list of Network
type NetworkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Network `json:"items"`
}

type NetworkRef struct {
	// The network namespace.
	//
	// Defaults to the namespace for the type the reference is embedded in.
	//
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`

	// The network name
	//
	// +kubebuilder:validation:Required
	Name string `json:"name,omitempty"`
}

type LocalNetworkRef struct {
	// The network name
	//
	// +kubebuilder:validation:Required
	Name string `json:"name,omitempty"`
}
