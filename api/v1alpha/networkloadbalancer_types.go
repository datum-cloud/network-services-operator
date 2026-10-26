// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	NetworkLoadBalancerAllocated    = "Allocated"
	NetworkLoadBalancerProgrammed   = "Programmed"
	NetworkLoadBalancerReady        = "Ready"
	NetworkLoadBalancerAnnounced    = "Announced"
	NetworkLoadBalancerDNSPublished = "DNSPublished"
)

// NetworkLoadBalancerLocationSource selects locations by name or follows all
// eligible locations as they become available.
// +kubebuilder:validation:Enum=Backends;All;Names
type NetworkLoadBalancerLocationSource string

const (
	NetworkLoadBalancerLocationSourceBackends NetworkLoadBalancerLocationSource = "Backends"
	NetworkLoadBalancerLocationSourceAll      NetworkLoadBalancerLocationSource = "All"
	NetworkLoadBalancerLocationSourceNames    NetworkLoadBalancerLocationSource = "Names"
)

// NetworkLoadBalancerProtocol is the transport accepted by a listener.
// +kubebuilder:validation:Enum=TCP;UDP;TLS
type NetworkLoadBalancerProtocol string

const (
	NetworkLoadBalancerProtocolTCP NetworkLoadBalancerProtocol = "TCP"
	NetworkLoadBalancerProtocolUDP NetworkLoadBalancerProtocol = "UDP"
	NetworkLoadBalancerProtocolTLS NetworkLoadBalancerProtocol = "TLS"
)

// NetworkLoadBalancerLocationSelection chooses where an address may be served.
// +kubebuilder:validation:XValidation:message="names must be set only when from is Names",rule="self.from == 'Names' ? has(self.names) && size(self.names) > 0 : !has(self.names)"
type NetworkLoadBalancerLocationSelection struct {
	// +kubebuilder:validation:Required
	From NetworkLoadBalancerLocationSource `json:"from"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +listType=set
	Names []string `json:"names,omitempty"`
}

// NetworkLoadBalancerAddressRequest reserves one public address per selected
// location, independently of whether a route has healthy backends.
type NetworkLoadBalancerAddressRequest struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=2
	// +listType=set
	Families []IPFamily `json:"families"`

	// Omission follows the effective backends' network interfaces.
	// +kubebuilder:validation:Optional
	Locations *NetworkLoadBalancerLocationSelection `json:"locations,omitempty"`
}

type NetworkLoadBalancerTLSMode string

const (
	NetworkLoadBalancerTLSModePassthrough NetworkLoadBalancerTLSMode = "Passthrough"
	NetworkLoadBalancerTLSModeTerminate   NetworkLoadBalancerTLSMode = "Terminate"
)

// +kubebuilder:validation:XValidation:message="Terminate requires a certificate; Passthrough omits it",rule="self.mode == 'Terminate' ? has(self.certificateRef) : !has(self.certificateRef)"
type NetworkLoadBalancerTLS struct {
	// +kubebuilder:validation:Enum=Passthrough;Terminate
	Mode NetworkLoadBalancerTLSMode `json:"mode"`
	// Secret references are admitted only after TLS termination is supported.
	CertificateRef *LocalSecretRef `json:"certificateRef,omitempty"`
}

type LocalSecretRef struct {
	// Name identifies a Secret in the listener's namespace.
	Name string `json:"name"`
	// Cross-namespace access is capability-gated until grants are supported.
	Namespace string `json:"namespace,omitempty"`
}

// NetworkLoadBalancerExposure contains the load balancer's address requests.
type NetworkLoadBalancerExposure struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +listType=map
	// +listMapKey=name
	Addresses []NetworkLoadBalancerAddressRequest `json:"addresses"`
}

// NetworkLoadBalancerListener accepts one transport and port on its named
// address requests. TCP and UDP may use the same port.
// +kubebuilder:validation:XValidation:rule="has(self.port) != has(self.portRange)",message="exactly one of port and portRange is required"
// +kubebuilder:validation:XValidation:rule="!has(self.portRange) || self.portRange.start == self.portRange.end || self.protocol == 'UDP'",message="true port ranges require UDP"
type NetworkLoadBalancerListener struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	Protocol NetworkLoadBalancerProtocol `json:"protocol"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port,omitempty"`
	// +optional
	PortRange *PortRange `json:"portRange,omitempty"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +listType=set
	AddressRefs []string `json:"addressRefs"`

	// +kubebuilder:validation:Optional
	Hostname string `json:"hostname,omitempty"`

	// +kubebuilder:validation:Optional
	TLS *NetworkLoadBalancerTLS `json:"tls,omitempty"`
}

// NetworkLoadBalancerSpec defines the network, public addresses, and listeners.
// +kubebuilder:validation:XValidation:message="each listener address reference must name an address request",rule="self.listeners.all(l, l.addressRefs.all(r, r in self.exposure.addresses.map(a, a.name)))"
// +kubebuilder:validation:XValidation:message="listeners sharing an address cannot have overlapping transport ports",rule="self.listeners.all(a, self.listeners.all(b, a.name == b.name || (a.protocol == 'UDP') != (b.protocol == 'UDP') || !a.addressRefs.exists(r, r in b.addressRefs) || (has(a.portRange) ? a.portRange.end : a.port) < (has(b.portRange) ? b.portRange.start : b.port) || (has(b.portRange) ? b.portRange.end : b.port) < (has(a.portRange) ? a.portRange.start : a.port)))"
// +kubebuilder:validation:XValidation:message="TLS configuration is required only for TLS listeners",rule="self.listeners.all(l, l.protocol == 'TLS' ? has(l.tls) : !has(l.tls))"
type NetworkLoadBalancerSpec struct {
	// The network must exist in the load balancer's namespace.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:message="networkRef is immutable",rule="self == oldSelf"
	NetworkRef LocalNetworkRef `json:"networkRef"`

	// +kubebuilder:validation:Required
	Exposure NetworkLoadBalancerExposure `json:"exposure"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +listType=map
	// +listMapKey=name
	Listeners []NetworkLoadBalancerListener `json:"listeners"`
}

// NetworkLoadBalancerIPClaimRef identifies the claim holding an allocated IP.
type NetworkLoadBalancerIPClaimRef struct {
	Project   string `json:"project"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// UID distinguishes a recreated claim with the same name from the old allocation.
	UID string `json:"uid,omitempty"`
}

// NetworkLoadBalancerAddressStatus describes one family at a location.
type NetworkLoadBalancerAddressStatus struct {
	Family            IPFamily                       `json:"family"`
	Value             string                         `json:"value,omitempty"`
	ClaimRef          *NetworkLoadBalancerIPClaimRef `json:"claimRef,omitempty"`
	WithdrawalPending bool                           `json:"withdrawalPending,omitempty"`
	Announced         bool                           `json:"announced,omitempty"`
	DNSPublished      bool                           `json:"dnsPublished,omitempty"`
	Reason            string                         `json:"reason,omitempty"`
	Message           string                         `json:"message,omitempty"`

	// Conditions include Allocated, Programmed, and Announced with
	// observedGeneration. An entry with no value reports a pending or failed
	// allocation for this request/location.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type NetworkLoadBalancerListenerStatus struct {
	Name string `json:"name"`
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// NetworkLoadBalancerEndpointStatus is stable for one address request and
// location, even while individual address families are unavailable.
type NetworkLoadBalancerEndpointStatus struct {
	// +listType=map
	// +listMapKey=name
	Listeners         []NetworkLoadBalancerListenerStatus `json:"listeners,omitempty"`
	RequestName       string                              `json:"requestName"`
	Location          string                              `json:"location"`
	CanonicalHostname string                              `json:"canonicalHostname,omitempty"`
	// +listType=map
	// +listMapKey=family
	Addresses []NetworkLoadBalancerAddressStatus `json:"addresses,omitempty"`
}

type NetworkLoadBalancerAddressRequestStatus struct {
	Name           string                            `json:"name"`
	LocationSource NetworkLoadBalancerLocationSource `json:"locationSource"`
	// +listType=set
	ResolvedLocations []string `json:"resolvedLocations,omitempty"`
}

// NetworkLoadBalancerStatus distinguishes address allocation from programming
// and announcement. Allocated IPs remain here during backend failures.
type NetworkLoadBalancerStatus struct {
	// Conditions include Allocated, Programmed, and Ready with observedGeneration.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +kubebuilder:validation:MaxItems=16
	// +listType=map
	// +listMapKey=name
	AddressRequests []NetworkLoadBalancerAddressRequestStatus `json:"addressRequests,omitempty"`

	// +kubebuilder:validation:MaxItems=1024
	// +listType=map
	// +listMapKey=requestName
	// +listMapKey=location
	Endpoints []NetworkLoadBalancerEndpointStatus `json:"endpoints,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// NetworkLoadBalancer reserves public IPs and accepts TCP or UDP traffic on
// named listeners. Route objects attach backends to those listeners.
type NetworkLoadBalancer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   NetworkLoadBalancerSpec   `json:"spec"`
	Status NetworkLoadBalancerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NetworkLoadBalancerList contains a list of NetworkLoadBalancer objects.
type NetworkLoadBalancerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkLoadBalancer `json:"items"`
}
