// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TopologyCityCodeKey is the topology key holding a location's city.
const TopologyCityCodeKey = "topology.datum.net/city-code"

// ServingLocationTopologyLabel is the cluster label a cell carries to claim the
// location it serves.
const ServingLocationTopologyLabel = "topology.datum.net/location"

// ServingLocationSpec describes the location a cell serves.
type ServingLocationSpec struct {
	// Topology describes where in the world this location is. Workloads placed
	// at this location inherit it, and placement rules that ask for a city or a
	// region are answered from these keys.
	//
	// The map holds arbitrary keys. Some keys are well known:
	//
	//	topology.datum.net/city-code: IAD
	//	topology.datum.net/region: us-east-1
	//
	// You must supply topology.datum.net/city-code, and it must not be empty.
	// A location with no city code cannot serve placement requests that name a
	// city, so the API rejects it. Any other key you set is carried through
	// unchanged and is available to workloads at this location.
	//
	// This field copies the topology of the Location it was published from.
	// Edit the Location, not this copy.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinProperties=1
	// +kubebuilder:validation:XValidation:message="topology must carry a non-empty topology.datum.net/city-code",rule="'topology.datum.net/city-code' in self && self['topology.datum.net/city-code'] != ''"
	Topology map[string]string `json:"topology"`

	// Source identifies the Location this copy came from. Use it to tell how
	// current the copy is: compare it against the Location of the same name to
	// see whether an edit has reached this cell yet.
	//
	// The publisher sets this field. Leave it alone.
	//
	// +kubebuilder:validation:Optional
	Source ServingLocationSource `json:"source,omitempty"`
}

// ServingLocationSource records which version of a Location a ServingLocation
// was copied from.
type ServingLocationSource struct {
	// Generation is the metadata.generation of the Location this copy came
	// from. When it is lower than the Location's current generation, an edit
	// has not reached this cell yet.
	//
	// +kubebuilder:validation:Optional
	Generation int64 `json:"generation,omitempty"`

	// PublishedAt is when the content of this copy last changed. A copy that is
	// re-checked but not changed keeps its original timestamp, so an old
	// timestamp means the location has been stable, not that publishing has
	// stalled.
	//
	// +kubebuilder:validation:Optional
	PublishedAt metav1.Time `json:"publishedAt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="City",type="string",JSONPath=`.spec.topology.topology\.datum\.net/city-code`
// +kubebuilder:printcolumn:name="Region",type="string",JSONPath=`.spec.topology.topology\.datum\.net/region`
// +kubebuilder:printcolumn:name="Source Generation",type="integer",JSONPath=".spec.source.generation"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ServingLocation tells a cell which location it serves.
//
// A cell is a cluster that runs workloads at one physical location. It cannot
// tell where it is on its own, so the platform delivers it a ServingLocation:
// a read-only copy of a Location, carrying the name and topology of the place
// the cell sits in. Everything the cell does that depends on where it is,
// such as claiming network addresses, resolves through this object.
//
// A ServingLocation takes the name of the Location it was copied from. Expect
// exactly one on a cell. Two or more means more than one location has been
// delivered to the same cell, and the cell refuses to guess between them.
//
// This object is managed for you. Create and edit Locations on the platform
// control plane; the copies follow.
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
