// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TopologyCityCodeKey is the topology key every location-scoped reader resolves
// its city from.
const TopologyCityCodeKey = "topology.datum.net/city-code"

// ServingLocationTopologyLabel is the cluster label a cell carries to claim the
// location it serves. The location never names its cell.
const ServingLocationTopologyLabel = "topology.datum.net/location"

// ServingLocationSpec carries the identity and locality of the location a cell
// serves. It deliberately omits the provider block, coordinates, location class
// and display name: a field earns its place here by having a reader at a cell.
type ServingLocationSpec struct {
	// Topology carries the locality of the location, keyed as it is on Location
	// and LocationBinding. An empty city code makes every location-scoped
	// placement fail with nothing catching it, so it is required here.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinProperties=1
	// +kubebuilder:validation:XValidation:message="topology must carry a non-empty topology.datum.net/city-code",rule="'topology.datum.net/city-code' in self && self['topology.datum.net/city-code'] != ''"
	Topology map[string]string `json:"topology"`

	// Source describes the record this copy was published from. It is declared
	// spec-side because the propagation layer strips status, so a reader that
	// must reason about staleness has nowhere else to read it.
	//
	// +kubebuilder:validation:Optional
	Source ServingLocationSource `json:"source,omitempty"`
}

type ServingLocationSource struct {
	// Generation is the metadata.generation of the Location this copy was
	// published from.
	//
	// +kubebuilder:validation:Optional
	Generation int64 `json:"generation,omitempty"`

	// PublishedAt is when the published content last changed, not when the
	// publisher last ran.
	//
	// +kubebuilder:validation:Optional
	PublishedAt metav1.Time `json:"publishedAt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="City",type="string",JSONPath=`.spec.topology.topology\.datum\.net/city-code`
// +kubebuilder:printcolumn:name="Source Generation",type="integer",JSONPath=".spec.source.generation"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ServingLocation names the location a cell serves. Its name is the name of the
// Location it was published from.
type ServingLocation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ServingLocationSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// ServingLocationList contains a list of ServingLocation.
type ServingLocationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServingLocation `json:"items"`
}

// CityCode returns the city code carried in the topology map.
func (l *ServingLocation) CityCode() string {
	return l.Spec.Topology[TopologyCityCodeKey]
}
