// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NetworkContextSpec defines the desired state of NetworkContext
type NetworkContextSpec struct {
	// The attached network
	//
	// +kubebuilder:validation:Required
	Network LocalNetworkRef `json:"network"`

	// The location of where a network context exists.
	//
	// +kubebuilder:validation:Required
	Location LocationReference `json:"location,omitempty"`

	// IP families the network carries, projected from the Network.
	//
	// A reader that finds this unset must refuse rather than assume a family:
	// a context written before this field existed carries nothing, which is not
	// the same as a network that carries nothing.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=2
	IPFamilies []IPFamily `json:"ipFamilies,omitempty"`

	// MTU of interfaces on the network, projected from the Network.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1300
	// +kubebuilder:validation:Maximum=8856
	MTU int32 `json:"mtu,omitempty"`

	// FabricIdentity is the network's fabric identity, projected from the
	// Network's status. This is where a location reads it: cells cannot reach
	// project control planes, and propagation to them carries spec and not
	// status.
	//
	// Zero means the identity has not been projected here yet, either because
	// the network does not have one or because this location has not caught up
	// with the allocation. A reader that finds it unset must wait rather than
	// choose an identity of its own, which is what every location does today
	// and is the reason one network is two on the fabric.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4294967295
	FabricIdentity int64 `json:"fabricIdentity,omitempty"`

	// The Network generation the projected fields were read from, so an operator
	// comparing this to the Network can tell whether this location has caught up.
	//
	// The identity is allocated into the Network's status, so it lands without
	// advancing that generation. This still answers whether the location has
	// caught up with the network's spec; whether it has caught up with the
	// allocation is answered by the identity being present.
	//
	// +kubebuilder:validation:Optional
	NetworkGeneration int64 `json:"networkGeneration,omitempty"`
}

// NetworkContextStatus defines the observed state of NetworkContext
type NetworkContextStatus struct {
	// Represents the observations of a network context's current state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// IPAM reports the address space IPAM holds for this network in this
	// location.
	//
	// +kubebuilder:validation:Optional
	IPAM *NetworkContextIPAMStatus `json:"ipam,omitempty"`
}

// NetworkContextIPAMStatus reports what IPAM holds for a network in one
// location.
//
// The range itself is published on the Subnet this context owns, which is the
// API a consumer already reads a location's addressing from. What is recorded
// here is where that range came from, so the allocation can be audited and
// released without a second copy of it to keep in step.
type NetworkContextIPAMStatus struct {
	// IPv6SubnetRef names the Subnet publishing this location's /64.
	//
	// +kubebuilder:validation:Optional
	IPv6SubnetRef *LocalSubnetReference `json:"ipv6SubnetRef,omitempty"`

	// IPv6ClaimRef names what holds the /64 in IPAM. Deleting the claim it
	// names releases what this operator holds.
	//
	// +kubebuilder:validation:Optional
	IPv6ClaimRef *NetworkPrefixRef `json:"ipv6ClaimRef,omitempty"`
}

const (
	// NetworkContextReady indicates whether or not the network context is ready for use.
	NetworkContextReady = "Ready"
)

const (
	// NetworkContextReadyReasonReady indicates that the network context is ready for use.
	NetworkContextReadyReasonReady = "Ready"

	// NetworkContextReadyReasonTerminating means the context is being deleted.
	// Nothing may be bound to it, and nothing may adopt it.
	NetworkContextReadyReasonTerminating = "Terminating"
)

// NetworkContextUnclaimedSinceAnnotation records when the last consumer stopped
// declaring this presence, in RFC3339. A replaced workload leaves a gap of a few
// seconds with no consumer, and tearing the context down inside that gap takes
// the location's address space with it. The instant lives on the object so it
// survives a restart or a change of leader.
const NetworkContextUnclaimedSinceAnnotation = "networking.datumapis.com/unclaimed-since"

const (
	// NetworkContextIPAMAllocated reports whether IPAM holds this location's
	// subnet.
	NetworkContextIPAMAllocated = "IPAMAllocated"

	// NetworkContextReasonProjectNamespaceNotFound means the namespace the
	// platform provisions with a project is absent from its control plane, so
	// nothing can be allocated for it.
	NetworkContextReasonProjectNamespaceNotFound = "ProjectNamespaceNotFound"

	// NetworkContextReasonProjectUnresolved means the context's namespace names
	// no project, so no IPAM request can be addressed on its behalf.
	NetworkContextReasonProjectUnresolved = "ProjectUnresolved"

	// NetworkContextReasonRangeOccupied means this location's subnet cannot be
	// given back while addresses are still allocated inside it. The interfaces
	// holding them have to go first.
	NetworkContextReasonRangeOccupied = "RangeOccupied"

	// NetworkContextReasonRangeUnsupported means IPAM did not keep the request
	// for a range, so it would answer with a block from inside one. A block is
	// not this location's subnet and the addresses it hands out do not lie in
	// it.
	NetworkContextReasonRangeUnsupported = "RangeUnsupported"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NetworkContext is the Schema for the networkcontexts API
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="IPv6Subnet",type="string",JSONPath=".status.ipam.ipv6SubnetRef.name"
type NetworkContext struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec NetworkContextSpec `json:"spec,omitempty"`

	// +kubebuilder:default={conditions:{{type:"Ready",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"}}}
	Status NetworkContextStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NetworkContextList contains a list of NetworkContext
type NetworkContextList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkContext `json:"items"`
}

type NetworkContextRef struct {
	// The network context namespace
	//
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`

	// The network context name
	//
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

type LocalNetworkContextRef struct {
	// The network context name
	//
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}
