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

	// The location of where a network context exists.
	//
	// +kubebuilder:validation:Required
	Location LocationReference `json:"location,omitempty"`

	// IP families the network carries, projected from the Network.
	//
	// A reader that finds this unset must refuse rather than assume a family:
	// a context written before this field existed carries nothing, which is not
	// the same as a network that carries nothing.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=2
	IPFamilies []IPFamily `json:"ipFamilies,omitempty"`

	// MTU of interfaces on the network, projected from the Network.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1300
	// +kubebuilder:validation:Maximum=8856
	MTU int32 `json:"mtu,omitempty"`

	// The Network generation the projected fields were read from, so an operator
	// comparing this to the Network can tell whether this location has caught up.
	//
	// +kubebuilder:validation:Optional
	NetworkGeneration int64 `json:"networkGeneration,omitempty"`

	// Egress is what the network reaches outside the platform from this
	// location, projected from the Network and resolved against the serving
	// class. Propagation to a cell carries spec and not status, so the
	// instruction a cell acts on lives here and the result it reports lives in
	// status.
	//
	// A reader that finds this unset must refuse rather than assume: a context
	// written before this field existed carries nothing, which is not the same
	// as a network that reaches nothing.
	//
	// +kubebuilder:validation:Optional
	Egress *NetworkContextEgress `json:"egress,omitempty"`
}

// NetworkContextEgress is the outbound intent projected onto one location.
type NetworkContextEgress struct {
	// Internet is the internet egress this location is instructed to provide.
	//
	// +kubebuilder:validation:Optional
	Internet *NetworkContextInternetEgress `json:"internet,omitempty"`
}

// NetworkContextInternetEgress instructs one location to provide internet
// egress. Every field is resolved before it is written here, so nothing
// reading it selects a class, picks a default, or interprets parameters.
type NetworkContextInternetEgress struct {
	// Mode is whether instances in this location reach the internet, copied
	// from the network.
	//
	// It carries no default. A defaulted Disabled could not be told apart from
	// a field never projected, and a reader that cannot tell those apart must
	// refuse rather than withdraw egress a consumer asked for.
	//
	// +kubebuilder:validation:Optional
	Mode NetworkInternetEgressMode `json:"mode,omitempty"`

	// Reach are the destination address families this location is instructed
	// to reach, copied from the network and narrowed to what the serving class
	// reaches.
	//
	// Only IPv6 is accepted, because a projection may not carry what its
	// source cannot declare.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:XValidation:message="Only IPv6 is accepted; reaching IPv4 destinations needs a resolver and a translator sharing a prefix, and the platform pairs neither",rule="self.all(f, f == 'IPv6')"
	// +kubebuilder:validation:XValidation:message="Each address family may be listed at most once",rule="self.all(f, self.exists_one(g, g == f))"
	Reach []IPFamily `json:"reach,omitempty"`

	// ClassName is the InternetEgressClass resolved for this network,
	// including the case where the network named none and the default class
	// was selected. It is written resolved so class selection stays with the
	// single writer that reads the classes, and a location never repeats it.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ClassName string `json:"className,omitempty"`

	// Sharing is the serving class's sharing, carried so a location can report
	// the stability a consumer reads back on status without reading the class
	// itself.
	//
	// +kubebuilder:validation:Optional
	Sharing InternetEgressSharing `json:"sharing,omitempty"`

	// ParametersRef is the serving class's parametersRef, passed through
	// verbatim. Nothing on the path between the class and the controller named
	// in the class's controllerName interprets it.
	//
	// +kubebuilder:validation:Optional
	ParametersRef *InternetEgressClassParametersRef `json:"parametersRef,omitempty"`
}

// NetworkContextStatus defines the observed state of NetworkContext
type NetworkContextStatus struct {
	// Represents the observations of a network context's current state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// IPAM reports the address space IPAM holds for this network in this
	// location.
	//
	// +kubebuilder:validation:Optional
	IPAM *NetworkContextIPAMStatus `json:"ipam,omitempty"`

	// Egress reports what the network reaches outside the platform from this
	// location. Egress is realized per location, so a network present in two
	// locations reports an answer on each context rather than one answer on
	// the network.
	//
	// +kubebuilder:validation:Optional
	Egress *NetworkContextEgressStatus `json:"egress,omitempty"`
}

// NetworkContextEgressStatus reports the outbound paths realized for a network
// in one location.
type NetworkContextEgressStatus struct {
	// Internet reports the internet egress realized for this location.
	//
	// +kubebuilder:validation:Optional
	Internet *NetworkContextInternetEgressStatus `json:"internet,omitempty"`
}

// InternetEgressAddressStability is how far a consumer may rely on an egress
// address.
//
// +kubebuilder:validation:Enum=None;Network
type InternetEgressAddressStability string

const (
	// InternetEgressAddressStabilityNone means the address may change and
	// other networks share it. Allow-listing it admits traffic from other
	// networks and loses access when the address changes.
	InternetEgressAddressStabilityNone InternetEgressAddressStability = "None"

	// InternetEgressAddressStabilityNetwork means the address belongs to this
	// network and persists. Allow-listing it is safe.
	InternetEgressAddressStabilityNetwork InternetEgressAddressStability = "Network"
)

// NetworkContextInternetEgressStatus reports the source addresses outbound
// traffic leaves this location on.
//
// It omits how the platform delivers egress — which node carries the traffic,
// how translation state is partitioned, which other networks share the path —
// because a consumer cannot act on those facts and some of them describe other
// consumers.
type NetworkContextInternetEgressStatus struct {
	// SourceAddresses are the addresses translation writes onto outbound
	// packets from this location, with the reliance each one carries.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=16
	SourceAddresses []InternetEgressSourceAddress `json:"sourceAddresses,omitempty"`

	// DNS64Prefix is the prefix the platform's resolver synthesizes addresses
	// under for names publishing no IPv6 record. Reaching an IPv4 destination
	// by name works only for instances using a resolver that shares this
	// prefix with the translator.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=43
	DNS64Prefix string `json:"dns64Prefix,omitempty"`
}

// InternetEgressSourceAddress is one address outbound traffic leaves on.
type InternetEgressSourceAddress struct {
	// Family is the address family of this source address.
	//
	// +kubebuilder:validation:Required
	Family IPFamily `json:"family"`

	// Address is the source address translation writes, without a prefix
	// length.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=39
	Address string `json:"address"`

	// Stability states how far a consumer may rely on this address before
	// they act on it. It is the consumer-side projection of the serving
	// class's sharing.
	//
	// +kubebuilder:validation:Required
	Stability InternetEgressAddressStability `json:"stability"`
}

// NetworkContextIPAMStatus reports what IPAM holds for a network in one
// location.
//
// The range itself is published on the Subnet this context owns, which is the
// API a consumer already reads a location's addressing from. What is recorded
// here is where that range came from, so the allocation can be audited and
// released without a second copy of it to keep in step.
type NetworkContextIPAMStatus struct {
	// IPv6SubnetRef names the Subnet publishing this location's /64.
	//
	// +kubebuilder:validation:Optional
	IPv6SubnetRef *LocalSubnetReference `json:"ipv6SubnetRef,omitempty"`

	// IPv6ClaimRef names what holds the /64 in IPAM. Deleting the claim it
	// names releases what this operator holds.
	//
	// +kubebuilder:validation:Optional
	IPv6ClaimRef *NetworkPrefixRef `json:"ipv6ClaimRef,omitempty"`
}

const (
	// NetworkContextReady indicates whether or not the network context is ready for use.
	NetworkContextReady = "Ready"

	// NetworkContextInternetEgressReady reports whether instances in this
	// location reach the internet destinations the network declared. Each
	// reason states a fact about the consumer's network; the specific cause,
	// such as the failing component or allocation, is carried by operator
	// events instead.
	NetworkContextInternetEgressReady = "InternetEgressReady"

	// NetworkContextInternetEgressReasonReady means instances in this location
	// reach the declared destinations.
	NetworkContextInternetEgressReasonReady = "Ready"

	// NetworkContextInternetEgressReasonAddressUnavailable means the platform
	// allocated no egress address for this location.
	NetworkContextInternetEgressReasonAddressUnavailable = "AddressUnavailable"

	// NetworkContextInternetEgressReasonUnavailable means no component in this
	// location provides egress.
	NetworkContextInternetEgressReasonUnavailable = "Unavailable"

	// NetworkContextInternetEgressReasonDegraded means egress works for some
	// declared families and not for others.
	NetworkContextInternetEgressReasonDegraded = "Degraded"
)

const (
	// NetworkContextReadyReasonReady indicates that the network context is ready for use.
	NetworkContextReadyReasonReady = "Ready"

	// NetworkContextReadyReasonTerminating means the context is being deleted.
	// Nothing may be bound to it, and nothing may adopt it.
	NetworkContextReadyReasonTerminating = "Terminating"
)

// NetworkContextUnclaimedSinceAnnotation records when the last consumer stopped
// declaring this presence, in RFC3339. A replaced workload leaves a gap of a few
// seconds with no consumer, and tearing the context down inside that gap takes
// the location's address space with it. The instant lives on the object so it
// survives a restart or a change of leader.
const NetworkContextUnclaimedSinceAnnotation = "networking.datumapis.com/unclaimed-since"

const (
	// NetworkContextIPAMAllocated reports whether IPAM holds this location's
	// subnet.
	NetworkContextIPAMAllocated = "IPAMAllocated"

	// NetworkContextReasonProjectNamespaceNotFound means the namespace the
	// platform provisions with a project is absent from its control plane, so
	// nothing can be allocated for it.
	NetworkContextReasonProjectNamespaceNotFound = "ProjectNamespaceNotFound"

	// NetworkContextReasonProjectUnresolved means the context's namespace names
	// no project, so no IPAM request can be addressed on its behalf.
	NetworkContextReasonProjectUnresolved = "ProjectUnresolved"

	// NetworkContextReasonRangeOccupied means this location's subnet cannot be
	// given back while addresses are still allocated inside it. The interfaces
	// holding them have to go first.
	NetworkContextReasonRangeOccupied = "RangeOccupied"

	// NetworkContextReasonRangeUnsupported means IPAM did not keep the request
	// for a range, so it would answer with a block from inside one. A block is
	// not this location's subnet and the addresses it hands out do not lie in
	// it.
	NetworkContextReasonRangeUnsupported = "RangeUnsupported"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NetworkContext is the Schema for the networkcontexts API
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="IPv6Subnet",type="string",JSONPath=".status.ipam.ipv6SubnetRef.name"
type NetworkContext struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec NetworkContextSpec `json:"spec,omitempty"`

	// +kubebuilder:default={conditions:{{type:"Ready",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"}}}
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
