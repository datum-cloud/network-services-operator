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

// PublicKeyConnectorAddress defines an address and port for a connector.
type PublicKeyConnectorAddress struct {
	// IPv4 or IPv6 address.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=39
	// +kubebuilder:validation:XValidation:message="Must be an IP address.",rule="isIP(self)"
	Address string `json:"address,omitempty"`

	// Port where the connector can be reached.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port,omitempty"`
}

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
	// The connector controller creates the Lease when a Connector is created
	// and records it here. Connector implementations (agents) are expected to
	// periodically renew the Lease to indicate liveness.
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

// ConnectorLivenessAnnotation carries a compact snapshot of a Connector's
// authoritative upstream liveness down to edge member clusters.
//
// A Connector's Ready condition and ConnectionDetails are computed in the
// Project control plane and stored in the Connector's status subresource. The
// edge extension server reads connectors from the local member-cluster cache to
// decide whether a tunnel is online. Karmada propagates a resource template's
// spec and metadata (labels/annotations) to member clusters but NOT the status
// subresource, so the member-cluster Connector never carries Ready or
// ConnectionDetails. To bridge that gap the replicator stamps the liveness onto
// this annotation — which Karmada DOES propagate — and the extension server
// reads it from there, falling back to status when the annotation is absent.
//
// The value is a JSON-marshalled ConnectorLiveness. Keep the JSON schema in
// sync with that type.
const ConnectorLivenessAnnotation = "networking.datumapis.com/connector-liveness"

// ConnectorLiveness is the JSON payload stored in ConnectorLivenessAnnotation.
//
// It carries the upstream Connector's Ready classification plus its full
// ConnectionDetails. Embedding the complete ConnectionDetails — including the
// type discriminator and the type-specific block — lets the extension server
// derive the tunnel node ID for any connection type the API supports, today and
// in future, without changing this annotation's JSON schema. It deliberately
// does NOT include the tunnel TargetHost/TargetPort: those are derived from the
// referencing HTTPProxy backend endpoint URL, not from Connector status.
type ConnectorLiveness struct {
	// Ready mirrors the upstream Connector's Ready condition being True.
	Ready bool `json:"ready"`

	// ConnectionDetails is the upstream Connector's full
	// Status.ConnectionDetails, copied verbatim. Carrying the complete structure
	// keeps the data available for future connection types without an
	// annotation-schema change; consumers read the field they need directly. Nil
	// when the upstream connector has not yet published connection details.
	ConnectionDetails *ConnectorConnectionDetails `json:"connectionDetails,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:selectablefield:JSONPath=".status.connectionDetails.publicKey.id"

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
