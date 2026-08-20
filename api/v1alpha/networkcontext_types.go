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

	// The Network generation the projected fields were read from, so an operator
	// comparing this to the Network can tell whether this location has caught up.
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

	// NetworkContextProgrammed indicates whether or not the network context has been programmed.
	NetworkContextProgrammed = "Programmed"
)

const (
	// NetworkContextProgrammedReasonNotProgrammed indicates that the network context is not ready because it has not been programmed.
	NetworkContextProgrammedReasonNotProgrammed = "NotProgrammed"

	// NetworkContextProgrammedReasonProgramming indicates that the network context is being programmed.
	NetworkContextProgrammedReasonProgrammingInProgress = "ProgrammingInProgress"

	// NetworkContextProgrammedReasonProgrammed indicates that the network context has been programmed.
	NetworkContextProgrammedReasonProgrammed = "Programmed"

	// NetworkContextReadyReasonReady indicates that the network context is ready for use.
	NetworkContextReadyReasonReady = "Ready"
)

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

	// +kubebuilder:default={conditions:{{type:"Programmed",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"},{type:"Ready",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"}}}
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
