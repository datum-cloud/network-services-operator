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

	// The topology of where this context exists
	//
	// This may contain arbitrary topology keys. Some keys may be well known, such
	// as:
	//	- topology.datum.net/city-code
	//	- topology.datum.net/cluster-name
	//	- topology.datum.net/cluster-namespace
	//
	// The combined keys and values MUST be unique for contexts in the same
	// network.
	//
	// +kubebuilder:validation:Required
	Topology map[string]string `json:"topology"`
}

// NetworkContextStatus defines the observed state of NetworkContext
type NetworkContextStatus struct {
	// Represents the observations of a network context's current state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

const (
	// NetworkContextReady indicates that the network context is ready for use.
	NetworkContextReady = "Ready"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NetworkContext is the Schema for the networkcontexts API
type NetworkContext struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NetworkContextSpec   `json:"spec,omitempty"`
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

func init() {
	SchemeBuilder.Register(&NetworkContext{}, &NetworkContextList{})
}
