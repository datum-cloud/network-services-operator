// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	locationsv1alpha1 "go.miloapis.com/locations/api/v1alpha1"
)

// NetworkBindingSpec defines the desired state of NetworkBinding
type NetworkBindingSpec struct {
	// The network that the binding is for.
	//
	// Immutable: a binding whose network changed is a declaration about a
	// different presence. Delete and recreate instead, so the crossing is
	// observable.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec.network is immutable"
	Network NetworkRef `json:"network,omitempty"`

	// The location of where a network binding exists.
	//
	// Immutable, for the same reason as spec.network.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec.location is immutable"
	Location locationsv1alpha1.LocationReference `json:"location,omitempty"`

	// The resource that needs the network in this location.
	//
	// Nothing reads this to decide anything, and a binding is never held open
	// because of it. It records who asked in a form that does not depend on the
	// consumer being an object in this control plane, which is the only record
	// for a consumer that cannot be an owner.
	//
	// +kubebuilder:validation:Optional
	Consumer *NetworkBindingConsumer `json:"consumer,omitempty"`
}

// NetworkBindingConsumer names the resource that declared it needs a network in
// a location.
type NetworkBindingConsumer struct {
	// APIGroup of the consumer. Empty means the core group.
	//
	// +kubebuilder:validation:Optional
	APIGroup string `json:"apiGroup,omitempty"`

	// Kind of the consumer.
	//
	// +kubebuilder:validation:Required
	Kind string `json:"kind"`

	// Name of the consumer.
	//
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// NetworkBindingObjectReference contains sufficient information for
// controllers to leverage unstructured or structured clients to interact with
// the bound resources.
type NetworkBindingObjectReference struct {
	// API version of the referent.
	//
	// +kubebuilder:validation:Required
	APIVersion string `json:"apiVersion"`

	// Kind of the referent.
	//
	// +kubebuilder:validation:Required
	Kind string `json:"kind,omitempty"`

	// Namespace of the referent.
	//
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace,omitempty"`

	// Name of the referent.
	//
	// +kubebuilder:validation:Required
	Name string `json:"name,omitempty"`
}

// NetworkBindingStatus defines the observed state of NetworkBinding
type NetworkBindingStatus struct {
	NetworkContextRef *NetworkContextRef `json:"networkContextRef,omitempty"`

	// Represents the observations of a network binding's current state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

const (
	// NetworkBindingReady indicates that the network binding has been associated
	// with a NetworkContext and the owning resource should expect functional
	// network features.
	NetworkBindingReady = "Ready"
)

// Labels a consumer stamps on a NetworkBinding, and the presence controller
// stamps on the NetworkContext serving it. The network and location labels are
// what make counting the consumers of a presence a list rather than a stored
// number.
const (
	NetworkLabel    = "networking.datumapis.com/network"
	LocationLabel   = "networking.datumapis.com/location"
	NetworkUIDLabel = "networking.datumapis.com/network-uid"
)

// Reasons reported on a NetworkBinding's Ready condition.
const (
	// NetworkBindingReasonPending is the condition's default: the object exists
	// and no controller has looked at it.
	NetworkBindingReasonPending = "Pending"

	// NetworkBindingReasonProjectUnresolved means the binding's namespace does
	// not resolve to a project. No consumer can fix this.
	NetworkBindingReasonProjectUnresolved = "ProjectUnresolved"

	// NetworkBindingReasonNetworkNotFound means the named network does not exist
	// in the project.
	NetworkBindingReasonNetworkNotFound = "NetworkNotFound"

	// NetworkBindingReasonLocationNotAvailable means the project has no
	// LocationBinding for the location, so it cannot use it.
	NetworkBindingReasonLocationNotAvailable = "LocationNotAvailable"

	// NetworkBindingReasonNetworkContextNotReady means the presence exists and is
	// not usable yet.
	NetworkBindingReasonNetworkContextNotReady = "NetworkContextNotReady"

	// NetworkBindingReasonNetworkContextReady means the network is present in
	// this location.
	NetworkBindingReasonNetworkContextReady = "NetworkContextReady"

	// NetworkBindingReasonNetworkContextTerminating means the presence serving
	// this pair is being deleted. A dying context cannot be adopted, so the
	// binding waits for it to go and for a fresh one to be created.
	NetworkBindingReasonNetworkContextTerminating = "NetworkContextTerminating"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NetworkBinding is the Schema for the networkbindings API
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
type NetworkBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec NetworkBindingSpec `json:"spec,omitempty"`

	// +kubebuilder:default={conditions:{{type:"Ready",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"}}}
	Status NetworkBindingStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NetworkBindingList contains a list of NetworkBinding
type NetworkBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkBinding `json:"items"`
}
