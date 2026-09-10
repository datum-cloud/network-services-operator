// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EdgeReachabilityName is the name of the single record a namespace holds.
// One record answers for the whole namespace, so a reader gets its answer with
// a get rather than a list it has to decide is complete.
const EdgeReachabilityName = "default"

// EdgeReachabilitySpec is the set of workload addresses in one project
// namespace that an edge is expected to reach.
type EdgeReachabilitySpec struct {
	// addresses are the workload addresses currently behind a proxy, one entry
	// per address, with no prefix length. An empty list is a real answer: it
	// says the project publishes nothing, which is different from no record at
	// all.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=8192
	Addresses []string `json:"addresses,omitempty"`
}

// +kubebuilder:object:root=true

// EdgeReachability records which of a project's workload addresses are behind
// an HTTPProxy, so the platform carries a workload's location to the edges that
// serve it and stops carrying it everywhere else.
//
// It is written by the control plane onto the federation hub and read by the
// cells publishing into it. No consumer creates or edits one, and nothing in a
// project control plane holds one.
//
// Absence of the record means the control plane has not answered for this
// namespace yet, and a reader must keep publishing rather than treat silence as
// a withdrawal. An empty list is the answer that withdraws.
// +kubebuilder:printcolumn:name="Addresses",type=integer,JSONPath=".spec.addresses.length()"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type EdgeReachability struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Optional
	Spec EdgeReachabilitySpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// EdgeReachabilityList contains a list of EdgeReachability.
type EdgeReachabilityList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EdgeReachability `json:"items"`
}
