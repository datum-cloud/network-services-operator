// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

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
	//
	// Nothing reports it today. Translation runs on the node an instance
	// attached to, so the address follows the node, and an address that
	// follows a network needs a translation tier that does not exist yet.
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
	// they act on it.
	//
	// +kubebuilder:validation:Required
	Stability InternetEgressAddressStability `json:"stability"`
}
