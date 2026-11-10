// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// HTTPProxyVPCBindingSpec records, for one HTTPProxy on an edge, the tenant VPC
// each of its VPC backends resolves to there.
//
// The extension server writes it on the edge; Envoy Gateway watches it as an
// extension policy, so a change to the resolved VPCs re-runs translation.
//
// +kubebuilder:validation:XValidation:rule="self.targetRefs.all(ref, ref.group == 'gateway.networking.k8s.io' && ref.kind == 'Gateway')", message="targetRefs must name a Gateway"
type HTTPProxyVPCBindingSpec struct {
	// TargetRefs names the Gateway serving the HTTPProxy.
	//
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1
	TargetRefs []gatewayv1alpha2.LocalPolicyTargetReferenceWithSectionName `json:"targetRefs"`

	// Backends lists every VPC backend of the HTTPProxy, ordered by rule and
	// backend index.
	//
	// +optional
	// +listType=atomic
	Backends []HTTPProxyVPCBindingBackend `json:"backends,omitempty"`
}

// HTTPProxyVPCBindingBackend is the tenant one HTTPProxy backend resolves to.
type HTTPProxyVPCBindingBackend struct {
	// Rule is the index of the backend's rule in the HTTPProxy.
	//
	// +kubebuilder:validation:Minimum=0
	Rule int32 `json:"rule"`

	// Backend is the index of the backend within its rule.
	//
	// +kubebuilder:validation:Minimum=0
	Backend int32 `json:"backend"`

	// TenantID is the "<vpc>-<vpcAttachment>" identifier the backend resolves
	// to, empty while it has not resolved on this edge.
	//
	// +optional
	TenantID string `json:"tenantID,omitempty"`
}

// HTTPProxyVPCBindingStatus is written by Envoy Gateway.
type HTTPProxyVPCBindingStatus struct {
	gatewayv1alpha2.PolicyStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HTTPProxyVPCBinding is the Schema for the httpproxyvpcbindings API.
type HTTPProxyVPCBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HTTPProxyVPCBindingSpec   `json:"spec"`
	Status HTTPProxyVPCBindingStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HTTPProxyVPCBindingList contains a list of HTTPProxyVPCBinding.
type HTTPProxyVPCBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HTTPProxyVPCBinding `json:"items"`
}
