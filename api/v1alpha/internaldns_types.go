// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

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

// NetworkDNSObjectReference identifies a DNS resource by name.
type NetworkDNSObjectReference struct {
	// Name is the resource name in the network's namespace.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

type NetworkDNSResolverSettings struct {
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=3
	// +kubebuilder:validation:XValidation:rule="self.all(n, isIP(n))",message="nameservers must contain IP addresses"
	Nameservers []string `json:"nameservers"`
	// +kubebuilder:validation:MaxItems=6
	Searches []string `json:"searches,omitempty"`
}

// NetworkDNSSettings are verified resolver settings inherited by interfaces.
type NetworkDNSSettings struct {
	// ContextRef identifies the DNS serving and namespace lifetime.
	//
	// +kubebuilder:validation:Required
	ContextRef NetworkDNSObjectReference `json:"contextRef"`

	// AccessBindingRef identifies the regional DNS configuration.
	//
	// +kubebuilder:validation:Required
	AccessBindingRef NetworkDNSObjectReference `json:"accessBindingRef"`

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

	// AccessBindingRef identifies the DNS configuration used for the proof.
	AccessBindingRef NetworkDNSObjectReference `json:"accessBindingRef"`

	// ServiceDestination identifies this DNS context inside the shared service.
	//
	// +kubebuilder:validation:XValidation:rule="isIP(self)",message="serviceDestination must be an IP address"
	ServiceDestination string `json:"serviceDestination"`

	// Nameservers lists the resolver frontends whose paths were verified.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=3
	// +kubebuilder:validation:XValidation:rule="self.all(n, isIP(n))",message="nameservers must contain IP addresses"
	Nameservers []string `json:"nameservers,omitempty"`
}

const (
	NetworkContextDNSReady = "DNSReady"

	NetworkContextDNSReadyReasonReady                 = "Ready"
	NetworkContextDNSReadyReasonDisabled              = "Disabled"
	NetworkContextDNSReadyReasonPending               = "Pending"
	NetworkContextDNSReadyReasonAccessAbsent          = "AccessAbsent"
	NetworkContextDNSReadyReasonNetworkPathNotReady   = "NetworkPathNotReady"
	NetworkContextDNSReadyReasonNetworkIdentityAbsent = "NetworkIdentityAbsent"
	NetworkContextDNSReadyReasonNoAttachments         = "NoAttachments"
	NetworkContextDNSReadyReasonTerminating           = "Terminating"

	NetworkInterfaceDNSConfiguredReasonApplied                     = "Applied"
	NetworkInterfaceDNSConfiguredReasonUnsupportedMultipleContexts = "UnsupportedMultipleContexts"
)
