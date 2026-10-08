// SPDX-License-Identifier: AGPL-3.0-only

// Package v1alpha1 carries the certificates.miloapis.com/v1alpha1 types the
// operator consumes from the Milo certificate service. It mirrors the
// service's API until its module is importable.
//
// +kubebuilder:object:generate=true
// +kubebuilder:skip
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	GroupVersion = schema.GroupVersion{Group: "certificates.miloapis.com", Version: "v1alpha1"}

	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion, &TLSCertificate{}, &TLSCertificateList{})
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
