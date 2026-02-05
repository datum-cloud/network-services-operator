// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConnectorClassSpec defines the desired state of ConnectorClass.
type ConnectorClassSpec struct {
	// ControllerName is the name of the controller responsible for this ConnectorClass.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:default=networking.datumapis.com/datum-connect
	ControllerName string `json:"controllerName"`
}

// ConnectorClassStatus defines the observed state of ConnectorClass.
type ConnectorClassStatus struct {
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// ConnectorClass is the Schema for the connectorclasses API.
type ConnectorClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of a ConnectorClass
	//
	// +kubebuilder:validation:Required
	Spec ConnectorClassSpec `json:"spec,omitempty"`

	// Status defines the observed state of a ConnectorClass
	Status ConnectorClassStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ConnectorClassList contains a list of ConnectorClass.
type ConnectorClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ConnectorClass `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ConnectorClass{}, &ConnectorClassList{})
}
