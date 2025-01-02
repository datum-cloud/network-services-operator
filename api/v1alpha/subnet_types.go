// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SubnetSpec defines the desired state of Subnet
type SubnetSpec struct {
	// The class of subnet
	//
	// +kubebuilder:validation:Required
	SubnetClass string `json:"subnetClass"`

	// A subnet's network context
	//
	// +kubebuilder:validation:Required
	NetworkContext LocalNetworkContextRef `json:"networkContext"`

	// The location which a subnet is associated with
	//
	// +kubebuilder:validation:Required
	Location LocationReference `json:"location,omitempty"`

	// The IP family of a subnet
	//
	// +kubebuilder:validation:Required
	IPFamily IPFamily `json:"ipFamily"`

	// The start address of a subnet
	//
	// +kubebuilder:validation:Required
	StartAddress string `json:"startAddress"`

	// The prefix length of a subnet
	//
	// +kubebuilder:validation:Required
	PrefixLength int32 `json:"prefixLength"`
}

// SubnetStatus defines the observed state of a Subnet
type SubnetStatus struct {
	// The start address of a subnet
	StartAddress *string `json:"startAddress,omitempty"`

	// The prefix length of a subnet
	PrefixLength *int32 `json:"prefixLength,omitempty"`

	// Represents the observations of a subnet's current state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Subnet is the Schema for the subnets API
type Subnet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SubnetSpec   `json:"spec,omitempty"`
	Status SubnetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SubnetList contains a list of Subnet
type SubnetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Subnet `json:"items"`
}

type LocalSubnetReference struct {
	Name string `json:"name"`
}

func init() {
	SchemeBuilder.Register(&Subnet{}, &SubnetList{})
}
