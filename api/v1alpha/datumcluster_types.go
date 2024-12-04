// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TODO(jreese) move this definition out of network-services-operator. It's here
// right now for convenience as both workload-operator and infra-provider-gcp
// will need to leverage the type. We'll likely want an `infra-cluster-operator`
// project for this.

// DatumClusterSpec defines the desired state of DatumCluster.
type DatumClusterSpec struct {
	// The cluster class for the cluster.
	//
	// Valid values are:
	//	- datum-managed
	//	- self-managed
	//
	// +kubebuilder:validation:Required
	ClusterClassName string `json:"clusterClassName,omitempty"`

	// The topology of the cluster
	//
	// This may contain arbitrary topology keys. Some keys may be well known, such
	// as:
	//	- topology.datum.net/city-code
	//
	// +kubebuilder:validation:Required
	Topology map[string]string `json:"topology"`

	// The cluster provider
	//
	// +kubebuilder:validation:Required
	Provider DatumClusterProvider `json:"provider"`
}

type DatumClusterProvider struct {
	GCP *GCPClusterProvider `json:"gcp,omitempty"`
}

type GCPClusterProvider struct {
	// The GCP project servicing the cluster
	//
	// For clusters with the class of `datum-managed`, a service account will be
	// required for each unique GCP project ID across all clusters registered in a
	// namespace.
	//
	// +kubebuilder:validation:Required
	ProjectID string `json:"projectId,omitempty"`

	// The GCP region servicing the cluster
	//
	// +kubebuilder:validation:Required
	Region string `json:"region,omitempty"`

	// The GCP zone servicing the cluster
	//
	// +kubebuilder:validation:Required
	Zone string `json:"zone,omitempty"`
}

// DatumClusterStatus defines the observed state of DatumCluster.
type DatumClusterStatus struct {
	// Represents the observations of a cluster's current state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DatumCluster is the Schema for the datumclusters API.
type DatumCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DatumClusterSpec   `json:"spec,omitempty"`
	Status DatumClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DatumClusterList contains a list of DatumCluster.
type DatumClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DatumCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DatumCluster{}, &DatumClusterList{})
}
