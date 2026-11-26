// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const switchPortHoldNameHashLength = 40

// SwitchPortHoldName is the name of the hold for a switch port. Port names can
// hold characters a Kubernetes name cannot, so the name is a hash. Each part is
// prefixed with its length, so no two pairs encode to the same bytes.
func SwitchPortHoldName(switchName, port string) string {
	encoded := strconv.Itoa(len(switchName)) + ":" + switchName + strconv.Itoa(len(port)) + ":" + port
	sum := sha256.Sum256([]byte(encoded))
	return "port-" + hex.EncodeToString(sum[:])[:switchPortHoldNameHashLength]
}

// SwitchPortHoldClaimRef names a physical claim recorded on a hold.
type SwitchPortHoldClaimRef struct {
	// namespace is the claim's namespace.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Namespace string `json:"namespace"`

	// name is the claim's name.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// network is the network the claim names, in the claim's namespace.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Network string `json:"network"`
}

// SwitchPortHoldConsumerRef names the project whose network claims hold a
// port.
type SwitchPortHoldConsumerRef struct {
	// apiGroup is the API group of the project type.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	APIGroup string `json:"apiGroup"`

	// kind is the project type.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Kind string `json:"kind"`

	// name is the project's name.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// SwitchPortHoldSpec records the claims that name one switch port.
//
// +kubebuilder:validation:XValidation:message="consumerRef is set exactly when networkClaims records a claim",rule="has(self.consumerRef) == (has(self.networkClaims) && size(self.networkClaims) > 0)"
type SwitchPortHoldSpec struct {
	// switch is the switch, stored in full. A hold whose stored pair differs
	// from a claim's is a hash collision, and the claim is refused.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:message="switch is immutable",rule="self == oldSelf"
	Switch string `json:"switch"`

	// port is the switch port, stored in full.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:message="port is immutable",rule="self == oldSelf"
	Port string `json:"port"`

	// boot is the one boot claim on the port. While it is set the port carries
	// its network and nothing else.
	//
	// +kubebuilder:validation:Optional
	Boot *SwitchPortHoldClaimRef `json:"boot,omitempty"`

	// consumerRef is the project whose network claims hold the port. It is set
	// with the first network claim and cleared with the last.
	//
	// +kubebuilder:validation:Optional
	ConsumerRef *SwitchPortHoldConsumerRef `json:"consumerRef,omitempty"`

	// networkClaims are the network claims on the port, all from the project
	// in consumerRef.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=256
	// +listType=atomic
	NetworkClaims []SwitchPortHoldClaimRef `json:"networkClaims,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Switch",type=string,JSONPath=".spec.switch"
// +kubebuilder:printcolumn:name="Port",type=string,JSONPath=".spec.port"
// +kubebuilder:printcolumn:name="Boot",type=string,JSONPath=".spec.boot.name"
// +kubebuilder:printcolumn:name="Consumer",type=string,JSONPath=".spec.consumerRef.name"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// SwitchPortHold records the physical claims that name one switch port, which
// decides what the port carries. The operator is its only writer and changes
// it only by compare-and-swap. The physical realizer reads it.
type SwitchPortHold struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec SwitchPortHoldSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// SwitchPortHoldList contains a list of SwitchPortHold.
type SwitchPortHoldList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SwitchPortHold `json:"items"`
}
