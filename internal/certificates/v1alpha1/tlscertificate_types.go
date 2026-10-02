// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IssuanceMode selects how the certificate authority validates control of the
// requested names.
type IssuanceMode string

const (
	IssuanceModeAuto   IssuanceMode = "Auto"
	IssuanceModeHTTP01 IssuanceMode = "HTTP01"
	IssuanceModeDNS01  IssuanceMode = "DNS01"
)

// ChallengeType is a resolved issuance mode: the ACME challenge type used to
// validate the names.
type ChallengeType string

const (
	ChallengeTypeHTTP01 ChallengeType = "HTTP01"
	ChallengeTypeDNS01  ChallengeType = "DNS01"
)

// DNSRecordPurpose explains why a DNS record must be published.
type DNSRecordPurpose string

const (
	DNSRecordPurposeRouting     DNSRecordPurpose = "Routing"
	DNSRecordPurposeCertificate DNSRecordPurpose = "Certificate"
)

// ChallengeState is the lifecycle state of an ACME challenge.
type ChallengeState string

const (
	ChallengeStatePending ChallengeState = "Pending"
	ChallengeStateValid   ChallengeState = "Valid"
	ChallengeStateInvalid ChallengeState = "Invalid"
)

// Condition types reported on a TLSCertificate.
const (
	ConditionAccepted           = "Accepted"
	ConditionDNSDelegationReady = "DNSDelegationReady"
	ConditionIssuing            = "Issuing"
	ConditionReady              = "Ready"
)

// TLSCertificateSpec defines the desired state of TLSCertificate. The service
// does not verify that the project controls the names; the caller creates a
// TLSCertificate only for names whose ownership it has already verified.
type TLSCertificateSpec struct {
	DNSNames   []DNSName    `json:"dnsNames"`
	Issuance   IssuanceMode `json:"issuance,omitempty"`
	SecretName string       `json:"secretName,omitempty"`
}

// DNSName is a lowercase RFC 1123 hostname, optionally prefixed with "*.".
type DNSName string

// SecretReference names a Secret in the TLSCertificate's namespace.
type SecretReference struct {
	Name string `json:"name"`
}

// ServiceSecretReference locates the issued Secret on the cluster that runs
// the certificate service.
type ServiceSecretReference struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// RequiredDNSRecord is a DNS record the name's owner must publish before
// issuance can complete.
type RequiredDNSRecord struct {
	Name    string           `json:"name"`
	Type    string           `json:"type"`
	Content string           `json:"content"`
	Purpose DNSRecordPurpose `json:"purpose"`
}

// ACMEChallenge is a live ACME challenge for one name. For HTTP01, the
// consumer serves GET /.well-known/acme-challenge/<token> with the body <key>
// on dnsName.
type ACMEChallenge struct {
	DNSName string         `json:"dnsName"`
	Type    ChallengeType  `json:"type"`
	Token   string         `json:"token"`
	Key     string         `json:"key"`
	State   ChallengeState `json:"state"`
}

// TLSCertificateStatus defines the observed state of TLSCertificate.
type TLSCertificateStatus struct {
	Issuance           ChallengeType           `json:"issuance,omitempty"`
	SecretRef          *SecretReference        `json:"secretRef,omitempty"`
	ServiceSecretRef   *ServiceSecretReference `json:"serviceSecretRef,omitempty"`
	DelegationTarget   string                  `json:"delegationTarget,omitempty"`
	NotBefore          *metav1.Time            `json:"notBefore,omitempty"`
	NotAfter           *metav1.Time            `json:"notAfter,omitempty"`
	RenewalTime        *metav1.Time            `json:"renewalTime,omitempty"`
	RequiredDNSRecords []RequiredDNSRecord     `json:"requiredDNSRecords,omitempty"`
	Challenges         []ACMEChallenge         `json:"challenges,omitempty"`
	Conditions         []metav1.Condition      `json:"conditions,omitempty"`
	ObservedGeneration int64                   `json:"observedGeneration,omitempty"`
}

// TLSCertificate requests a publicly trusted TLS certificate for a set of
// hostnames and delivers it as a kubernetes.io/tls Secret in the same
// namespace. Its name is at most 63 characters.
//
// +kubebuilder:object:root=true
type TLSCertificate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TLSCertificateSpec   `json:"spec"`
	Status TLSCertificateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type TLSCertificateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TLSCertificate `json:"items"`
}
