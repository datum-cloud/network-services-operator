// SPDX-License-Identifier: AGPL-3.0-only

// Package v1alpha contains API Schema definitions for the networking v1alpha API group.
// +kubebuilder:object:generate=true
// +groupName=networking.datumapis.com
package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is group version used to register these objects.
	GroupVersion = schema.GroupVersion{Group: "networking.datumapis.com", Version: "v1alpha"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&Domain{},
		&DomainList{},
		&HTTPProxy{},
		&HTTPProxyList{},
		&Location{},
		&LocationList{},
		&LocationBinding{},
		&LocationBindingList{},
		&Network{},
		&NetworkList{},
		&NetworkBinding{},
		&NetworkBindingList{},
		&NetworkContext{},
		&NetworkContextList{},
		&NetworkPolicy{},
		&NetworkPolicyList{},
		&Subnet{},
		&SubnetList{},
		&SubnetClaim{},
		&SubnetClaimList{},
		&TrafficProtectionPolicy{},
		&TrafficProtectionPolicyList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
