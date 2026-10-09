// SPDX-License-Identifier: AGPL-3.0-only

// Package v1alpha1 contains API Schema definitions for the networking v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=networking.datumapis.com
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "networking.datumapis.com", Version: "v1alpha1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// UpstreamStatusAnnotation carries a verbatim copy of a resource's upstream
// .status subresource down to edge member clusters.
//
// A resource's status is computed authoritatively in the Project control plane.
// Karmada propagates a resource template's spec and metadata
// (labels/annotations) to member clusters but NOT the status subresource, so a
// member-cluster object never carries its upstream status. For resource types
// whose downstream consumer needs that status (e.g. the edge extension server
// reading Connector liveness), the replicator mirrors the full upstream status
// JSON into this annotation — which Karmada DOES propagate — and the consumer
// parses it back, falling back to the live status when the annotation is absent.
//
// The value is the resource's .status object marshalled to JSON verbatim; it is
// resource-agnostic and carries no bespoke schema.
const UpstreamStatusAnnotation = "networking.datumapis.com/upstream-status"

// UpstreamGenerationAnnotation carries a resource's upstream metadata.generation
// to its downstream copy, written in the same update as the spec it belongs to.
//
// Each API server counts generation on its own, so a downstream copy's own
// generation does not say which upstream spec it holds. A consumer that compares
// the two, such as the TrafficProtectionPolicy Programmed check, reads this
// annotation. Karmada propagates it to member clusters with the other metadata.
const UpstreamGenerationAnnotation = "networking.datumapis.com/upstream-generation"

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&Connector{},
		&ConnectorList{},
		&ConnectorAdvertisement{},
		&ConnectorAdvertisementList{},
		&ConnectorClass{},
		&ConnectorClassList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
