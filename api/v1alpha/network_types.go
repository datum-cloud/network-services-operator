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

const (
	// NetworkAllocated reports that the network holds every allocation it needs
	// to exist, which today is its routing identity.
	NetworkAllocated = "Allocated"

	// NetworkReady reports that the network is allocated and can be placed into
	// a location.
	NetworkReady = "Ready"
)

const (
	// NetworkReasonProjectNamespaceNotFound means the namespace an allocation
	// would be made in does not exist in the project's control plane. The
	// platform provisions it with the project, so its absence says the control
	// plane is not bootstrapped rather than that it is slow to appear.
	NetworkReasonProjectNamespaceNotFound = "ProjectNamespaceNotFound"
)

// NetworkSpec defines the desired state of a Network
type NetworkSpec struct {

	// IPAM settings for the network.
	//
	// +kubebuilder:validation:Required
	IPAM NetworkIPAM `json:"ipam,omitempty"`

	// IP Families to permit on a network. Defaults to IPv4.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default={IPv4}
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

// NetworkStatus defines the observed state of Network
type NetworkStatus struct {
	// Represents the observations of a network's current state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// routingIdentity is the identity this network is known by on the routed
	// fabric, in every location it reaches. It is allocated once, when the
	// network is created, and does not change while the network exists: the
	// fabric embeds it in forwarding state, and a network that changed identity
	// would be a different network to everything already carrying its traffic.
	//
	// The value is an IPv6 prefix whose low bits are the identifier. It is
	// drawn from a pool that is never routed, so it is not an address and
	// nothing is reachable at it.
	//
	// Consumers do not request this and cannot influence it.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:message="routingIdentity is immutable once allocated",rule="self == oldSelf"
	RoutingIdentity *NetworkRoutingIdentity `json:"routingIdentity,omitempty"`
}

// NetworkRoutingIdentity is a network's allocated identity on the routed
// fabric, together with the allocation backing it.
type NetworkRoutingIdentity struct {
	// prefix is the allocated IPv6 prefix carrying the identifier, such as
	// fd00:0:0:a3f2::/64. It is unique across the platform and identical in
	// every location the network reaches.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=43
	Prefix string `json:"prefix"`

	// claimRef names the IPAM claim holding the prefix, in the project's own
	// control plane. It is recorded so the allocation behind an identity can be
	// audited, and released, without anyone re-deriving its name.
	//
	// +kubebuilder:validation:Required
	ClaimRef IPClaimRef `json:"claimRef"`
}

// IPClaimRef references an IPClaim in a project's control plane. IPAM is a
// separate API server, so this is a breadcrumb rather than a reference the API
// resolves.
type IPClaimRef struct {
	// namespace is the namespace the claim lives in.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Namespace string `json:"namespace"`

	// name is the name of the claim.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Name",type="string",JSONPath=".metadata.name"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=`.status.conditions[?(@.type==\"Ready\")].status`
// +kubebuilder:printcolumn:name="Reason",type="string",JSONPath=`.status.conditions[?(@.type==\"Ready\")].reason`
// +kubebuilder:printcolumn:name="IPAM",type="string",JSONPath=".spec.ipam.mode"
// +kubebuilder:printcolumn:name="IPFamilies",type="string",JSONPath=".spec.ipFamilies"
// +kubebuilder:printcolumn:name="MTU",type="integer",JSONPath=".spec.mtu"
// +kubebuilder:printcolumn:name="Routing Identity",type="string",JSONPath=".status.routingIdentity.prefix"

// Network is the Schema for the networks API
type Network struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   NetworkSpec   `json:"spec,omitempty"`
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
