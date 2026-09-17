// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InternetEgressClassDefaultAnnotation marks the class a network gets when it
// names none. An operator sets it to "true" on one class.
const InternetEgressClassDefaultAnnotation = "networking.datumapis.com/is-default-class"

// InternetEgressSharing is how many networks share the egress address a class
// hands out.
//
// +kubebuilder:validation:Enum=Shared;Dedicated
type InternetEgressSharing string

const (
	// InternetEgressSharingShared serves every network the class places on one
	// path from the same address. A consumer reads stability None and may not
	// allow-list the address.
	InternetEgressSharingShared InternetEgressSharing = "Shared"

	// InternetEgressSharingDedicated gives a network an address of its own. A
	// consumer reads stability Network and may allow-list the address.
	InternetEgressSharingDedicated InternetEgressSharing = "Dedicated"
)

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

// InternetEgressClassParametersRef names the configuration serving a class.
//
// The referenced type is implementation-defined and owned by the controller
// named in controllerName. Nothing here interprets it, validates its kind, or
// depends on the group it lives in.
//
// The reference runs one way: the class names its parameters, and no
// data-plane resource names the class.
type InternetEgressClassParametersRef struct {
	// Group of the referent.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=253
	Group string `json:"group"`

	// Kind of the referent.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Kind string `json:"kind"`

	// Name of the referent.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// InternetEgressClassSpec defines the desired state of InternetEgressClass.
type InternetEgressClassSpec struct {
	// ControllerName is the name of the controller responsible for this
	// InternetEgressClass.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:default=networking.datumapis.com/cell-egress
	ControllerName string `json:"controllerName"`

	// Sharing is the operator-side decision a consumer reads back as
	// stability on a network interface: Shared reports None, and Dedicated
	// reports Network.
	//
	// +kubebuilder:validation:Required
	Sharing InternetEgressSharing `json:"sharing"`

	// Reach are the destination address families a network on this class
	// reaches. The class names destinations, never the translation that
	// delivers them.
	//
	// Only IPv6 is accepted. A class advertising IPv4 would promise what no
	// component in the platform can deliver, so the value is withheld until a
	// resolver and a translator sharing a prefix are paired.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:XValidation:message="Only IPv6 is accepted; reaching IPv4 destinations needs a resolver and a translator sharing a prefix, and the platform pairs neither",rule="self.all(f, f == 'IPv6')"
	// +kubebuilder:validation:XValidation:message="Each address family may be listed at most once",rule="self.all(f, self.exists_one(g, g == f))"
	Reach []IPFamily `json:"reach"`

	// ParametersRef names the configuration the controller serving this class
	// reads, such as the address class an egress address is drawn from. Its
	// type is defined by that controller.
	//
	// +kubebuilder:validation:Optional
	ParametersRef *InternetEgressClassParametersRef `json:"parametersRef,omitempty"`
}

// InternetEgressClassStatus defines the observed state of InternetEgressClass.
type InternetEgressClassStatus struct {
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Controller",type="string",JSONPath=".spec.controllerName"
// +kubebuilder:printcolumn:name="Sharing",type="string",JSONPath=".spec.sharing"
// +kubebuilder:printcolumn:name="Reach",type="string",JSONPath=".spec.reach"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// InternetEgressClass is the Schema for the internetegressclasses API.
//
// An operator defines a class and a consumer names it on a network. The class
// decides how a network reaches the internet, not which destinations it may
// reach.
type InternetEgressClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of an InternetEgressClass
	//
	// +kubebuilder:validation:Required
	Spec InternetEgressClassSpec `json:"spec,omitempty"`

	// Status defines the observed state of an InternetEgressClass
	Status InternetEgressClassStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// InternetEgressClassList contains a list of InternetEgressClass.
type InternetEgressClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InternetEgressClass `json:"items"`
}
