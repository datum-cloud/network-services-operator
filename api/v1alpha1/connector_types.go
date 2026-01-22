// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:MinLength=1
// +kubebuilder:validation:MaxLength=253
type ConnectorCapabilityType string

const (
	ConnectTCP ConnectorCapabilityType = "ConnectTCP"
)

type ConnectorCapabilityCommon struct {
	Disabled bool `json:"disabled,omitempty"`
}

type ConnectorCapabilityConnectTCP struct {
	ConnectorCapabilityCommon `json:",inline"`
}

type ConnectorCapability struct {
	// Type of capability
	//
	// +kubebuilder:validation:Required
	Type ConnectorCapabilityType `json:"type,omitempty"`

	ConnectTCP *ConnectorCapabilityConnectTCP `json:"connectTCP,omitempty"`
}

// ConnectorSpec defines the desired state of Connector.
type ConnectorSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ConnectorClassName string `json:"connectorClassName"`

	// Capabilities desired to be supported by the connector.
	//
	// A connector may choose to not support all requested capabilities, and may
	// also choose to support additional capabilities not requested here. The
	// condition of each capability will reflect whether the capability is supported
	// or not in the ConnectorStatus.
	//
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=16
	Capabilities []ConnectorCapability `json:"capabilities,omitempty"`
}

type PublicKeyDiscoveryMode string

const (
	DNSPublicKeyDiscoveryMode PublicKeyDiscoveryMode = "DNS"
)

// IPv4 or IPv6 address
// +kubebuilder:validation:MaxLength=39
// +kubebuilder:validation:XValidation:message="Must be an IP address.",rule="isIP(self)"
type PublicKeyConnectorAddress string

type ConnectorConnectionDetailsPublicKey struct {
	// The public key to dial and connect to
	Id string `json:"id,omitempty"`

	// The mode used to discover the public key
	//
	// +kubebuilder:default=DNS
	// +kubebuilder:validation:Enum=DNS
	DiscoveryMode PublicKeyDiscoveryMode `json:"discoveryMode,omitempty"`

	// Home Relay server of the connector
	//
	// Must be a valid URL
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:message="Must be a URL.",rule="isURL(self)"
	HomeRelay string `json:"homeRelay,omitempty"`

	// Addresses where the connector can be reached
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxItems=16
	Addresses []PublicKeyConnectorAddress `json:"addresses,omitempty"`
}

type ConnectorConnectionType string

const (
	PublicKeyConnectorConnectionType ConnectorConnectionType = "PublicKey"
)

// ConnectorConnectionDetails provides details on how to connect to the connector.
//
// +kubebuilder:validation:XValidation:message="publicKey field must be nil if the type is not PublicKey",rule="!(self.type != 'PublicKey' && has(self.publicKey))"
// +kubebuilder:validation:XValidation:message="publicKey field must be specified if the type is PublicKey",rule="self.type == 'PublicKey' && has(self.publicKey)"
type ConnectorConnectionDetails struct {
	// Type of connection details provided.
	//
	// +kubebuilder:validation:Enum=PublicKey
	// +kubebuilder:validation:Required
	Type ConnectorConnectionType `json:"type,omitempty"`

	// PublicKey connection details
	PublicKey *ConnectorConnectionDetailsPublicKey `json:"publicKey,omitempty"`
}

type ConnectorCapabilityStatus struct {
	// Type of capability
	//
	// +kubebuilder:validation:Required
	Type ConnectorCapabilityType `json:"type,omitempty"`

	// Conditions describe the current conditions of the capability.
	//
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ConnectorStatus defines the observed state of Connector.
type ConnectorStatus struct {
	// Capabilities describe the status of each capability of the connector.
	//
	// +listType=map
	// +listMapKey=type
	Capabilities []ConnectorCapabilityStatus `json:"capabilities,omitempty"`

	// Conditions describe the current conditions of the HTTPProxy.
	//
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ConnectionDetails provide details on how to connect to the connector.
	//
	// +kubebuilder:validation:Optional
	ConnectionDetails *ConnectorConnectionDetails `json:"connectionDetails,omitempty"`

	// LeaseRef references the Lease used to report connector liveness.
	//
	// +kubebuilder:validation:Optional
	LeaseRef *corev1.LocalObjectReference `json:"leaseRef,omitempty"`
}

const (
	// ConnectorConditionAccepted indicates whether the ConnectorClass is resolved.
	ConnectorConditionAccepted = "Accepted"
	// ConnectorConditionReady indicates whether the Connector is ready to tunnel traffic.
	ConnectorConditionReady = "Ready"
	// ConnectorReasonAccepted indicates the Connector is accepted by the controller.
	ConnectorReasonAccepted = "Accepted"
	// ConnectorReasonReady indicates the Connector is ready to tunnel traffic.
	ConnectorReasonReady = "ConnectorReady"
	// ConnectorReasonNotReady indicates the Connector is not ready to tunnel traffic.
	ConnectorReasonNotReady = "ConnectorNotReady"
	// ConnectorReasonPending indicates the Connector has not been processed yet.
	ConnectorReasonPending = "Pending"
	// ConnectorReasonConnectorClassNotFound indicates the referenced class is missing.
	ConnectorReasonConnectorClassNotFound = "ConnectorClassNotFound"
)

const ConnectorNameAnnotation = "networking.datum.org/connector-name"

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Connector is the Schema for the connectors API.
type Connector struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of a Connector
	//
	// +kubebuilder:validation:Required
	Spec ConnectorSpec `json:"spec,omitempty"`

	// Status defines the observed state of a Connector
	//
	// +kubebuilder:default={conditions: {{type: "Accepted", status: "Unknown", reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"}}}
	Status ConnectorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ConnectorList contains a list of Connector.
type ConnectorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Connector `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Connector{}, &ConnectorList{})
}
