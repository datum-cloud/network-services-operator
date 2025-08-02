// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Domain represents a domain name in the Datum system
//
// +kubebuilder:printcolumn:name="Domain Name",type="string",JSONPath=".spec.domainName"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Verified",type="string",JSONPath=`.status.conditions[?(@.type=="Verified")].status`
// +kubebuilder:printcolumn:name="Verification Message",type="string",JSONPath=`.status.conditions[?(@.type=="Verified")].message`,priority=1
type Domain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec DomainSpec `json:"spec,omitempty"`

	// +kubebuilder:default={conditions: {{type: "Verified", status: "Unknown", reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"},{type: "VerifiedDNS", status: "Unknown", reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"},{type: "VerifiedHTTP", status: "Unknown", reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"}}}
	Status DomainStatus `json:"status,omitempty"`
}

// DomainSpec defines the desired state of Domain
type DomainSpec struct {
	// DomainName is the fully qualified domain name (FQDN) to be managed
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	// +kubebuilder:validation:XValidation:message="A domain name is immutable and cannot be changed after creation",rule="oldSelf == '' || self == oldSelf"
	// +kubebuilder:validation:XValidation:message="Must have at least two segments separated by dots",rule="self.indexOf('.') != -1"
	DomainName string `json:"domainName"`
}

// DomainStatus defines the observed state of Domain
type DomainStatus struct {
	Verification *DomainVerificationStatus `json:"verification,omitempty"`
	Registrar    *DomainRegistrarStatus    `json:"registrar,omitempty"`
	Conditions   []metav1.Condition        `json:"conditions,omitempty"`
}

const (
	// This condition is true when Domain ownership has been verified via either
	// DNS or HTTP.
	DomainConditionVerified = "Verified"

	// This condition tracks verification attempts via DNS.
	DomainConditionVerifiedDNS = "VerifiedDNS"

	// This condition tracks verification attempts via HTTP.
	DomainConditionVerifiedHTTP = "VerifiedHTTP"
)

const (
	// DomainReasonPendingVerification indicates domain verification is in
	// progress
	DomainReasonPendingVerification = "PendingVerification"

	// DomainReasonVerificationRecordContentMismatch indicates the
	// verification record content does not match expected values
	DomainReasonVerificationRecordContentMismatch = "VerificationRecordContentMismatch"

	// DomainReasonVerificationRecordNotFound indicates the verification record
	// was not found
	DomainReasonVerificationRecordNotFound = "RecordNotFound"

	// DomainReasonVerificationUnexpectedResponse indicates that an unexpected
	// response was encountered during verification.
	DomainReasonVerificationUnexpectedResponse = "UnexpectedResponse"

	// DomainReasonVerificationInternalError indicates that an internal error
	// was encountered during verification.
	DomainReasonVerificationInternalError = "InternalError"

	// DomainReasonVerified indicates domain ownership has been successfully
	// verified
	DomainReasonVerified = "Verified"
)

// DomainVerificationStatus represents the verification status of a domain
type DomainVerificationStatus struct {
	DNSRecord               DNSVerificationRecord `json:"dnsRecord,omitempty"`
	HTTPToken               HTTPVerificationToken `json:"httpToken,omitempty"`
	NextVerificationAttempt metav1.Time           `json:"nextVerificationAttempt,omitempty"`
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

// DNSVerificationRecord represents a DNS record required for verification
type DNSVerificationRecord struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

type HTTPVerificationToken struct {
	URL  string `json:"url"`
	Body string `json:"body"`
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
