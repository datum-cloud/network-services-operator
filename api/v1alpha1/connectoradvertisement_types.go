// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Layer4ServiceAddress defines an address for a Layer 4 service.
//
// +kubebuilder:validation:MinLength=1
// +kubebuilder:validation:MaxLength=253
// TODO - Add validation for DNS names with optional wildcards and IP addresses.
type Layer4ServiceAddress string

type Protocol string

const (
	// ProtocolTCP is the TCP protocol.
	ProtocolTCP Protocol = "TCP"
	// ProtocolUDP is the UDP protocol.
	ProtocolUDP Protocol = "UDP"
)

// Layer4ServicePort represents a port for a Layer 4 service.
type Layer4ServicePort struct {
	// Named port for the service.
	//
	// +kubebuilder:validation:Required
	Name string `json:"name,omitempty"`

	// Port number for the service.
	//
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:validation:Required
	Port int32 `json:"port,omitempty"`

	// Protocol for port. Must be TCP or UDP, defaults to "TCP".
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:default=TCP
	Protocol Protocol `json:"protocol,omitempty"`
}

type ConnectorAdvertisementLayer4Service struct {
	// Address of the service.
	//
	// Can be an IPv4, IPv6, or a DNS address. A DNS address may contain
	// wildcards. A DNS address acts as an allow list for what addresses the
	// connector will allow to be requested through it.
	//
	// DNS resolution is the responsibility of the connector.
	//
	// +kubebuilder:validation:Required
	Address Layer4ServiceAddress `json:"address"`

	// Ports of the service.
	//
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:Required
	Ports []Layer4ServicePort `json:"ports,omitempty"`
}

type ConnectorAdvertisementLayer4 struct {
	// Name of the advertisement.
	//
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Layer 4 services being advertised.
	//
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:Required
	Services []ConnectorAdvertisementLayer4Service `json:"services,omitempty"`
}

// ConnectorAdvertisementSpec defines the desired state of ConnectorAdvertisement.
type ConnectorAdvertisementSpec struct {
	// ConnectorRef references the Connector being advertised.
	//
	// +kubebuilder:validation:Required
	ConnectorRef LocalConnectorReference `json:"connectorRef"`

	// Layer 4 services being advertised.
	//
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=16
	Layer4 []ConnectorAdvertisementLayer4 `json:"layer4,omitempty"`
}

// ConnectorAdvertisementStatus defines the observed state of ConnectorAdvertisement.
type ConnectorAdvertisementStatus struct {
	// Conditions describe the current conditions of the ConnectorAdvertisement.
	//
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

const (
	// ConnectorAdvertisementConditionAccepted indicates the connector reference is resolved.
	ConnectorAdvertisementConditionAccepted = "Accepted"
	// ConnectorAdvertisementReasonAccepted indicates the advertisement is accepted.
	ConnectorAdvertisementReasonAccepted = "Accepted"
	// ConnectorAdvertisementReasonPending indicates the advertisement has not been processed yet.
	ConnectorAdvertisementReasonPending = "Pending"
	// ConnectorAdvertisementReasonConnectorNotFound indicates the referenced connector is missing.
	ConnectorAdvertisementReasonConnectorNotFound = "ConnectorNotFound"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ConnectorAdvertisement is the Schema for the connectoradvertisements API.
type ConnectorAdvertisement struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of a ConnectorAdvertisement
	//
	// +kubebuilder:validation:Required
	Spec ConnectorAdvertisementSpec `json:"spec,omitempty"`

	// Status defines the observed state of a ConnectorAdvertisement
	//
	// +kubebuilder:default={conditions: {{type: "Accepted", status: "Unknown", reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"}}}
	Status ConnectorAdvertisementStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ConnectorAdvertisementList contains a list of ConnectorAdvertisement.
type ConnectorAdvertisementList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ConnectorAdvertisement `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ConnectorAdvertisement{}, &ConnectorAdvertisementList{})
}
