// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	NetworkLoadBalancerAllocated  = "Allocated"
	NetworkLoadBalancerProgrammed = "Programmed"
	NetworkLoadBalancerReady      = "Ready"
	NetworkLoadBalancerAnnounced  = "Announced"
)

// NetworkLoadBalancerLocationSource selects locations by name or follows all
// eligible locations as they become available.
// +kubebuilder:validation:Enum=All;Names
type NetworkLoadBalancerLocationSource string

const (
	NetworkLoadBalancerLocationSourceAll   NetworkLoadBalancerLocationSource = "All"
	NetworkLoadBalancerLocationSourceNames NetworkLoadBalancerLocationSource = "Names"
)

// NetworkLoadBalancerProtocol is the transport accepted by a listener.
// +kubebuilder:validation:Enum=TCP;UDP
type NetworkLoadBalancerProtocol string

const (
	NetworkLoadBalancerProtocolTCP NetworkLoadBalancerProtocol = "TCP"
	NetworkLoadBalancerProtocolUDP NetworkLoadBalancerProtocol = "UDP"
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

	// IPv4 ingress is not supported by this API version.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=IPv6
	Family IPFamily `json:"family"`

	// +kubebuilder:validation:Required
	Locations NetworkLoadBalancerLocationSelection `json:"locations"`
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
type NetworkLoadBalancerListener struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	Protocol NetworkLoadBalancerProtocol `json:"protocol"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +listType=set
	AddressRefs []string `json:"addressRefs"`
}

// NetworkLoadBalancerSpec defines the network, public addresses, and listeners.
// +kubebuilder:validation:XValidation:message="each listener address reference must name an address request",rule="self.listeners.all(l, l.addressRefs.all(r, r in self.exposure.addresses.map(a, a.name)))"
// +kubebuilder:validation:XValidation:message="listeners sharing an address cannot use the same protocol and port",rule="self.listeners.all(l1, self.listeners.all(l2, l1.name == l2.name || l1.protocol != l2.protocol || l1.port != l2.port || !l1.addressRefs.exists(r, r in l2.addressRefs)))"
type NetworkLoadBalancerSpec struct {
	// The network must exist in the load balancer's namespace.
	// +kubebuilder:validation:Required
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
}

// NetworkLoadBalancerAddressStatus describes one allocation for one selected
// location. Its address is distinct from allocations in other locations.
type NetworkLoadBalancerAddressStatus struct {
	RequestName string `json:"requestName"`
	// +kubebuilder:validation:MinLength=1
	Location string                         `json:"location"`
	Family   IPFamily                       `json:"family"`
	Value    string                         `json:"value"`
	ClaimRef *NetworkLoadBalancerIPClaimRef `json:"claimRef,omitempty"`

	// Conditions include Allocated, Programmed, and Announced with
	// observedGeneration. An entry with no value reports a pending or failed
	// allocation for this request/location.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// NetworkLoadBalancerStatus distinguishes address allocation from programming
// and announcement. Allocated IPs remain here during backend failures.
type NetworkLoadBalancerStatus struct {
	// Conditions include Allocated, Programmed, and Ready with observedGeneration.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +kubebuilder:validation:MaxItems=1024
	// +listType=map
	// +listMapKey=requestName
	// +listMapKey=location
	Addresses []NetworkLoadBalancerAddressStatus `json:"addresses,omitempty"`
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
