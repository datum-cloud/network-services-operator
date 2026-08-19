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

	// The network's identity on the routed fabric, projected from
	// Network.status.routingIdentity. Every location carrying the network
	// carries the same value, which is what makes a network that spans two
	// locations one network.
	//
	// A reader that finds this unset must refuse rather than route without it:
	// a context written before this field existed reads the same as one written
	// before the identity was allocated, and neither says the network has no
	// identity.
	//
	// Unlike the other projected fields this one is read from the Network's
	// status, which does not move NetworkGeneration when it appears. The context
	// is rewritten when the allocation lands, so NetworkGeneration still answers
	// whether this location has caught up with the Network's spec, and this
	// field being set answers whether it has caught up with the allocation.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=43
	RoutingIdentity string `json:"routingIdentity,omitempty"`

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

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NetworkContext is the Schema for the networkcontexts API
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
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
