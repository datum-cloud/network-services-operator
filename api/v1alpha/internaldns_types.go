// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// +kubebuilder:validation:Enum=Managed;Disabled
type NetworkDNSMode string

const (
	NetworkDNSModeManaged  NetworkDNSMode = "Managed"
	NetworkDNSModeDisabled NetworkDNSMode = "Disabled"
)

// NetworkDNSPolicy selects managed resolver access for a network.
type NetworkDNSPolicy struct {
	// Mode controls whether network services maintain private resolver access.
	// An empty value uses Managed when the Internal DNS feature is enabled.
	//
	// +kubebuilder:validation:Optional
	Mode NetworkDNSMode `json:"mode,omitempty"`
}

// NetworkDNSObjectReference pins a DNS resource to its API-assigned lifetime.
// +kubebuilder:validation:XValidation:rule="self.uid.size() > 0",message="uid must not be empty"
type NetworkDNSObjectReference struct {
	// Name is the resource name in the network's namespace.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// UID prevents a resource recreated under the same name from inheriting
	// resolver access.
	//
	// +kubebuilder:validation:Required
	UID types.UID `json:"uid"`
}

// NetworkDNSSettings are verified resolver settings inherited by interfaces.
type NetworkDNSSettings struct {
	// ContextRef identifies the DNS serving and namespace lifetime.
	//
	// +kubebuilder:validation:Required
	ContextRef NetworkDNSObjectReference `json:"contextRef"`

	// AccessBindingRef identifies the regional DNS authorization lifetime.
	//
	// +kubebuilder:validation:Required
	AccessBindingRef NetworkDNSObjectReference `json:"accessBindingRef"`

	// WriterEpoch fences a previous access writer after ownership changes.
	//
	// +kubebuilder:validation:Minimum=1
	WriterEpoch int64 `json:"writerEpoch"`

	// Sequence is the exact DNS authorization revision these settings carry.
	//
	// +kubebuilder:validation:Minimum=1
	Sequence int64 `json:"sequence"`

	// ValidUntil is the earliest DNS or network authorization deadline.
	ValidUntil metav1.Time `json:"validUntil"`

	// Nameservers are resolver addresses reachable from this network.
	//
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=3
	// +kubebuilder:validation:XValidation:rule="self.all(n, isIP(n))",message="nameservers must contain IP addresses"
	Nameservers []string `json:"nameservers"`

	// Searches contains DNS search suffixes allocated to this network.
	//
	// +kubebuilder:validation:MaxItems=6
	Searches []string `json:"searches,omitempty"`
}

// NetworkDNSAccessStatus reports the private path verified for one context.
type NetworkDNSAccessStatus struct {
	// ContextRef identifies the DNS context used for the proof.
	ContextRef NetworkDNSObjectReference `json:"contextRef"`

	// AccessBindingRef identifies the DNS authorization used for the proof.
	AccessBindingRef NetworkDNSObjectReference `json:"accessBindingRef"`

	// WriterEpoch is the DNS authorization writer fenced by the proof.
	WriterEpoch int64 `json:"writerEpoch"`

	// Sequence is the exact ready DNS authorization revision.
	Sequence int64 `json:"sequence"`

	// ValidUntil is the earliest verified network-programming deadline.
	ValidUntil metav1.Time `json:"validUntil"`

	// ServiceDestination identifies this DNS context inside the shared service.
	//
	// +kubebuilder:validation:XValidation:rule="isIP(self)",message="serviceDestination must be an IP address"
	ServiceDestination string `json:"serviceDestination"`
}

const (
	NetworkContextDNSReady = "DNSReady"

	NetworkContextDNSReadyReasonReady                 = "Ready"
	NetworkContextDNSReadyReasonDisabled              = "Disabled"
	NetworkContextDNSReadyReasonPending               = "Pending"
	NetworkContextDNSReadyReasonAuthorizationExpired  = "AuthorizationExpired"
	NetworkContextDNSReadyReasonNetworkPathNotReady   = "NetworkPathNotReady"
	NetworkContextDNSReadyReasonNetworkIdentityAbsent = "NetworkIdentityAbsent"

	NetworkInterfaceDNSConfiguredReasonApplied                     = "Applied"
	NetworkInterfaceDNSConfiguredReasonExpired                     = "Expired"
	NetworkInterfaceDNSConfiguredReasonUnsupportedMultipleContexts = "UnsupportedMultipleContexts"
)
