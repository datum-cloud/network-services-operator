// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Domain",type="string",JSONPath=".spec.domainName"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=`.status.conditions[?(@.type=="Verification")].message`
// +kubebuilder:printcolumn:name="Registrar",type="string",JSONPath=".status.registrar.ianaName"
// +kubebuilder:printcolumn:name="DNSSEC",type="boolean",JSONPath=".status.registrar.dnssec.signed"
// +kubebuilder:printcolumn:name="Expires",type="date",JSONPath=".status.registrar.expirationDate"
// +kubebuilder:printcolumn:name="DNS-Record",type="string",JSONPath=".status.verification.requiredDNSRecords[0].name"
// +kubebuilder:printcolumn:name="DNS-Value",type="string",JSONPath=".status.verification.requiredDNSRecords[0].content"
// +kubebuilder:printcolumn:name="Last-Check",type="date",JSONPath=".status.verification.lastVerificationAttempt"

// Domain represents a domain name in the Datum system
type Domain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DomainSpec   `json:"spec,omitempty"`
	Status DomainStatus `json:"status,omitempty"`
}

// DomainSpec defines the desired state of Domain
type DomainSpec struct {
	// DomainName is the fully qualified domain name (FQDN) to be managed
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	DomainName string `json:"domainName"`
}

// DomainStatus defines the observed state of Domain
type DomainStatus struct {
	Verification *DomainVerificationStatus `json:"verification,omitempty"`
	Registrar    *DomainRegistrarStatus    `json:"registrar,omitempty"`
	Conditions   []metav1.Condition        `json:"conditions,omitempty"`
}

// DomainVerificationStatus represents the verification status of a domain
type DomainVerificationStatus struct {
	RequiredDNSRecords      []DNSVerificationExpectedRecord `json:"requiredDNSRecords,omitempty"`
	LastVerificationAttempt *metav1.Time                    `json:"lastVerificationAttempt,omitempty"`
}

// DomainRegistrarStatus represents the registrar information for a domain
type DomainRegistrarStatus struct {
	IANAID            string       `json:"ianaID,omitempty"`
	IANAName          string       `json:"ianaName,omitempty"`
	CreatedDate       string       `json:"createdDate,omitempty"`
	ModifiedDate      string       `json:"modifiedDate,omitempty"`
	ExpirationDate    string       `json:"expirationDate,omitempty"`
	Nameservers       []string     `json:"nameservers,omitempty"`
	DNSSEC            DNSSECStatus `json:"dnssec,omitempty"`
	ClientStatusCodes []string     `json:"clientStatusCodes,omitempty"`
	ServerStatusCodes []string     `json:"serverStatusCodes,omitempty"`
}

// DNSSECStatus represents the DNSSEC status of a domain
type DNSSECStatus struct {
	Signed bool `json:"signed"`
}

// DNSVerificationExpectedRecord represents a DNS record required for verification
type DNSVerificationExpectedRecord struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

// +kubebuilder:object:root=true

// DomainList contains a list of Domain
type DomainList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Domain `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Domain{}, &DomainList{})
}
