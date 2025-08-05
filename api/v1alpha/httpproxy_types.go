// SPDX-License-Identifier: AGPL-3.0-only
// Significant documentation and validation rules are copied from the Gateway API.

package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// HTTPProxySpec defines the desired state of HTTPProxy.
type HTTPProxySpec struct {

	// Hostnames defines a set of hostnames that should match against the HTTP
	// Host header to select a HTTPProxy used to process the request.
	//
	// Valid values for Hostnames are determined by RFC 1123 definition of a
	// hostname with 1 notable exception:
	//
	// 1. IPs are not allowed.
	//
	// Hostnames must be verified before being programmed. This is accomplished
	// via the use of `Domain` resources. A hostname is considered verified if any
	// verified `Domain` resource exists in the same namespace where the
	// `spec.domainName` of the resource either exactly matches the hostname, or
	// is a suffix match of the hostname. That means that a Domain with a
	// `spec.domainName` of `example.com` will match a hostname of
	// `test.example.com`, `foo.test.example.com`, and exactly `example.com`, but
	// not a hostname of `test-example.com`. If a `Domain` resource does not exist
	// that matches a hostname, one will automatically be created when the system
	// attempts to program the HTTPProxy.
	//
	// In addition to verifying ownership, hostnames must be unique across the
	// platform. If a hostname is already programmed on another resource, a
	// conflict will be encountered and communicated in the `HostnamesReady`
	// condition.
	//
	// Hostnames which have been programmed will be listed in the
	// `status.hostnames` field. Any hostname which has not been programmed will
	// be listed in the `message` field of the `HostnamesReady` condition with
	// an indication as to why it was not programmed.
	//
	// The system may automatically generate and associate hostnames with the
	// HTTPProxy. In such cases, these will be listed in the `status.hostnames`
	// field and do not require additional configuration by the user.
	//
	// Wildcard hostnames are not supported at this time.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=16
	Hostnames []gatewayv1.Hostname `json:"hostnames,omitempty"`

	// Rules are a list of HTTP matchers, filters and actions.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:XValidation:message="Rule name must be unique within the route",rule="self.all(l1, !has(l1.name) || self.exists_one(l2, has(l2.name) && l1.name == l2.name))"
	// +kubebuilder:validation:XValidation:message="While 16 rules and 64 matches per rule are allowed, the total number of matches across all rules in a route must be less than 128",rule="(self.size() > 0 ? self[0].matches.size() : 0) + (self.size() > 1 ? self[1].matches.size() : 0) + (self.size() > 2 ? self[2].matches.size() : 0) + (self.size() > 3 ? self[3].matches.size() : 0) + (self.size() > 4 ? self[4].matches.size() : 0) + (self.size() > 5 ? self[5].matches.size() : 0) + (self.size() > 6 ? self[6].matches.size() : 0) + (self.size() > 7 ? self[7].matches.size() : 0) + (self.size() > 8 ? self[8].matches.size() : 0) + (self.size() > 9 ? self[9].matches.size() : 0) + (self.size() > 10 ? self[10].matches.size() : 0) + (self.size() > 11 ? self[11].matches.size() : 0) + (self.size() > 12 ? self[12].matches.size() : 0) + (self.size() > 13 ? self[13].matches.size() : 0) + (self.size() > 14 ? self[14].matches.size() : 0) + (self.size() > 15 ? self[15].matches.size() : 0) <= 128"
	Rules []HTTPProxyRule `json:"rules,omitempty"`
}

// HTTPProxyRule defines semantics for matching an HTTP request based on
// conditions (matches), processing it (filters), and forwarding the request to
// backends.
//
// +kubebuilder:validation:XValidation:message="RequestRedirect filter must not be used together with backends",rule="(has(self.backends) && size(self.backends) > 0) ? (!has(self.filters) || self.filters.all(f, !has(f.requestRedirect))): true"
// +kubebuilder:validation:XValidation:message="When using RequestRedirect filter with path.replacePrefixMatch, exactly one PathPrefix match must be specified",rule="(has(self.filters) && self.filters.exists_one(f, has(f.requestRedirect) && has(f.requestRedirect.path) && f.requestRedirect.path.type == 'ReplacePrefixMatch' && has(f.requestRedirect.path.replacePrefixMatch))) ? ((size(self.matches) != 1 || !has(self.matches[0].path) || self.matches[0].path.type != 'PathPrefix') ? false : true) : true"
// +kubebuilder:validation:XValidation:message="When using URLRewrite filter with path.replacePrefixMatch, exactly one PathPrefix match must be specified",rule="(has(self.filters) && self.filters.exists_one(f, has(f.urlRewrite) && has(f.urlRewrite.path) && f.urlRewrite.path.type == 'ReplacePrefixMatch' && has(f.urlRewrite.path.replacePrefixMatch))) ? ((size(self.matches) != 1 || !has(self.matches[0].path) || self.matches[0].path.type != 'PathPrefix') ? false : true) : true"
// +kubebuilder:validation:XValidation:message="Within backends, when using RequestRedirect filter with path.replacePrefixMatch, exactly one PathPrefix match must be specified",rule="(has(self.backends) && self.backends.exists_one(b, (has(b.filters) && b.filters.exists_one(f, has(f.requestRedirect) && has(f.requestRedirect.path) && f.requestRedirect.path.type == 'ReplacePrefixMatch' && has(f.requestRedirect.path.replacePrefixMatch))) )) ? ((size(self.matches) != 1 || !has(self.matches[0].path) || self.matches[0].path.type != 'PathPrefix') ? false : true) : true"
// +kubebuilder:validation:XValidation:message="Within backends, When using URLRewrite filter with path.replacePrefixMatch, exactly one PathPrefix match must be specified",rule="(has(self.backends) && self.backends.exists_one(b, (has(b.filters) && b.filters.exists_one(f, has(f.urlRewrite) && has(f.urlRewrite.path) && f.urlRewrite.path.type == 'ReplacePrefixMatch' && has(f.urlRewrite.path.replacePrefixMatch))) )) ? ((size(self.matches) != 1 || !has(self.matches[0].path) || self.matches[0].path.type != 'PathPrefix') ? false : true) : true"
type HTTPProxyRule struct {
	// Name is the name of the route rule. This name MUST be unique within a Route
	// if it is set.
	Name *gatewayv1.SectionName `json:"name,omitempty"`

	// Matches define conditions used for matching the rule against incoming
	// HTTP requests. Each match is independent, i.e. this rule will be matched
	// if **any** one of the matches is satisfied.
	//
	// See documentation for the `matches` field in the `HTTPRouteRule` type at
	// https://gateway-api.sigs.k8s.io/reference/spec/#httprouterule
	//
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:default={{path:{ type: "PathPrefix", value: "/"}}}
	Matches []gatewayv1.HTTPRouteMatch `json:"matches,omitempty"`

	// Filters define the filters that are applied to requests that match
	// this rule.
	//
	// See documentation for the `filters` field in the `HTTPRouteRule` type at
	// https://gateway-api.sigs.k8s.io/reference/spec/#httprouterule
	//
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:XValidation:message="May specify either requestRedirect or urlRewrite, but not both",rule="!(self.exists(f, f.type == 'RequestRedirect') && self.exists(f, f.type == 'URLRewrite'))"
	// +kubebuilder:validation:XValidation:message="RequestHeaderModifier filter cannot be repeated",rule="self.filter(f, f.type == 'RequestHeaderModifier').size() <= 1"
	// +kubebuilder:validation:XValidation:message="ResponseHeaderModifier filter cannot be repeated",rule="self.filter(f, f.type == 'ResponseHeaderModifier').size() <= 1"
	// +kubebuilder:validation:XValidation:message="RequestRedirect filter cannot be repeated",rule="self.filter(f, f.type == 'RequestRedirect').size() <= 1"
	// +kubebuilder:validation:XValidation:message="URLRewrite filter cannot be repeated",rule="self.filter(f, f.type == 'URLRewrite').size() <= 1"
	Filters []gatewayv1.HTTPRouteFilter `json:"filters,omitempty"`

	// Backends defines the backend(s) where matching requests should be
	// sent.
	//
	// Note: While this field is a list, only a single element is permitted at
	// this time due to underlying Gateway limitations. Once addressed, MaxItems
	// will be increased to allow for multiple backends on any given route.
	//
	// +kubebuilder:validation:MinItems=0
	// +kubebuilder:validation:MaxItems=1
	Backends []HTTPProxyRuleBackend `json:"backends,omitempty"`
}

type HTTPProxyRuleBackend struct {
	// Endpoint for the backend. Must be a valid URL.
	//
	// Supports http and https protocols, IPs or DNS addresses in the host, custom
	// ports, and paths.
	//
	// +kubebuilder:validation:Required
	Endpoint string `json:"endpoint,omitempty"`

	// Filters defined at this level should be executed if and only if the
	// request is being forwarded to the backend defined here.
	//
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:XValidation:message="May specify either requestRedirect or urlRewrite, but not both",rule="!(self.exists(f, f.type == 'RequestRedirect') && self.exists(f, f.type == 'URLRewrite'))"
	// +kubebuilder:validation:XValidation:message="RequestHeaderModifier filter cannot be repeated",rule="self.filter(f, f.type == 'RequestHeaderModifier').size() <= 1"
	// +kubebuilder:validation:XValidation:message="ResponseHeaderModifier filter cannot be repeated",rule="self.filter(f, f.type == 'ResponseHeaderModifier').size() <= 1"
	// +kubebuilder:validation:XValidation:message="RequestRedirect filter cannot be repeated",rule="self.filter(f, f.type == 'RequestRedirect').size() <= 1"
	// +kubebuilder:validation:XValidation:message="URLRewrite filter cannot be repeated",rule="self.filter(f, f.type == 'URLRewrite').size() <= 1"
	Filters []gatewayv1.HTTPRouteFilter `json:"filters,omitempty"`
}

// HTTPProxyStatus defines the observed state of HTTPProxy.
type HTTPProxyStatus struct {
	// Addresses lists the network addresses that have been bound to the
	// HTTPProxy.
	//
	// This field will not contain custom hostnames defined in the HTTPProxy. See
	// the `hostnames` field
	//
	// +kubebuilder:validation:MaxItems=16
	Addresses []gatewayv1.GatewayStatusAddress `json:"addresses,omitempty"`

	// Hostnames lists the hostnames that have been bound to the HTTPProxy.
	//
	// If this list does not match that defined in the HTTPProxy, see the
	// `HostnamesReady` condition message for details.
	Hostnames []gatewayv1.Hostname `json:"hostnames,omitempty"`

	// Conditions describe the current conditions of the HTTPProxy.
	//
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

const (
	// This condition is true when the HTTPProxy configuration has been determined
	// to be valid, and can be programmed into the underlying Gateway resources.
	HTTPProxyConditionAccepted = "Accepted"

	// This condition is true when the HTTPProxy configuration has been successfully
	// programmed into underlying Gateway resources, and those resources have also
	// been programmed.
	HTTPProxyConditionProgrammed = "Programmed"

	// This condition is true when all hostnames defined in an HTTPProxy are have
	// been programmed.
	HTTPProxyConditionHostnamesReady = "HostnamesReady"
)

const (

	// HTTPProxyReasonAccepted indicates that the HTTP proxy has been accepted.
	HTTPProxyReasonAccepted = "Accepted"

	// HTTPProxyReasonProgrammed indicates that the HTTP proxy has been programmed.
	HTTPProxyReasonProgrammed = "Programmed"

	// HTTPProxyReasonConflict indicates that the HTTP proxy encountered a conflict
	// when being programmed.
	HTTPProxyReasonConflict = "Conflict"

	// This reason is used with the "Accepted" and "Programmed"
	// conditions when the status is "Unknown" and no controller has reconciled
	// the HTTPProxy.
	HTTPProxyReasonPending = "Pending"

	// This reason is used with the "HostnamesReady" condition when all hostnames
	// defined in an HTTPProxy have been verified and programmed.
	HTTPProxyReasonHostnamesAccepted = "HostnamesAccepted"

	// This reason is used with the "HostnamesReady" condition when a hostname
	// defined in an HTTPProxy has not been verified.
	HTTPProxyReasonHostnameUnverifiedHostnamesPresent = "UnverifiedHostnamesPresent"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// An HTTPProxy builds on top of Gateway API resources to provide a more convenient
// method to manage simple reverse proxy use cases.
//
// +kubebuilder:printcolumn:name="Hostname",type=string,JSONPath=`.status.hostnames[*]`
// +kubebuilder:printcolumn:name="Programmed",type=string,JSONPath=`.status.conditions[?(@.type=="Programmed")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type HTTPProxy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of an HTTPProxy.
	// +kubebuilder:validation:Required
	Spec HTTPProxySpec `json:"spec,omitempty"`

	// Status defines the current state of an HTTPProxy.
	//
	// +kubebuilder:default={conditions: {{type: "Accepted", status: "Unknown", reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"},{type: "Programmed", status: "Unknown", reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"}}}
	Status HTTPProxyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HTTPProxyList contains a list of HTTPProxy.
type HTTPProxyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HTTPProxy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HTTPProxy{}, &HTTPProxyList{})
}
