// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	NetworkLoadBalancerRouteAccepted     = "Accepted"
	NetworkLoadBalancerRouteResolvedRefs = "ResolvedRefs"
	NetworkLoadBalancerRouteProgrammed   = "Programmed"
)

// NetworkLoadBalancerRouteParentRef attaches a route to a listener. Cross-
// namespace references are capability-gated initially. Either listener name or
// port selects a listener; status always reports its resolved name.
// +kubebuilder:validation:XValidation:message="exactly one of listener or port is required",rule="has(self.listener) != has(self.port)"
type NetworkLoadBalancerRouteParentRef struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// One of listener or port selects the parent listener.
	// +kubebuilder:validation:Optional
	Listener string `json:"listener,omitempty"`
	// +kubebuilder:validation:Optional
	Port *int32 `json:"port,omitempty"`
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
}

// NetworkLoadBalancerNetworkServiceRef names a backend service and one of its
// named ports in the route's namespace.
type NetworkLoadBalancerNetworkServiceRef struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	Port string `json:"port"`
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
}

// NetworkLoadBalancerHealthCheck checks an individual backend endpoint. UDP
// backends require a separate TCP, HTTP, or HTTPS health endpoint.
// +kubebuilder:validation:XValidation:message="path is only valid for HTTP and HTTPS probes",rule="has(self.path) ? self.protocol in ['HTTP', 'HTTPS'] : true"
// +kubebuilder:validation:XValidation:message="HTTP and HTTPS probes require a port",rule="self.protocol == 'TCP' || has(self.port)"
type NetworkLoadBalancerHealthCheck struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=TCP;HTTP;HTTPS
	Protocol string `json:"protocol"`

	// Name of the probe port on the referenced NetworkService. When omitted,
	// TCP probes use the backend's serving port.
	// +kubebuilder:validation:Optional
	Port string `json:"port,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^/.*$`
	Path string `json:"path,omitempty"`
	// +kubebuilder:validation:Minimum=1
	IntervalSeconds *int32 `json:"intervalSeconds,omitempty"`
	// +kubebuilder:validation:Minimum=1
	TimeoutSeconds *int32 `json:"timeoutSeconds,omitempty"`
	// +kubebuilder:validation:Minimum=1
	HealthyThreshold *int32 `json:"healthyThreshold,omitempty"`
	// +kubebuilder:validation:Minimum=1
	UnhealthyThreshold *int32 `json:"unhealthyThreshold,omitempty"`
}

// NetworkLoadBalancerRouteBackend forwards new connections or flows to a
// named NetworkService port.
type NetworkLoadBalancerRouteBackend struct {
	// +kubebuilder:validation:Required
	NetworkService NetworkLoadBalancerNetworkServiceRef `json:"networkService"`

	// +kubebuilder:validation:Optional
	HealthCheck *NetworkLoadBalancerHealthCheck `json:"healthCheck,omitempty"`
	// Backend TLS requires a proxy-capable serving path.
	// +kubebuilder:validation:Optional
	Transport *NetworkLoadBalancerBackendTransport `json:"transport,omitempty"`
}

type NetworkLoadBalancerBackendTransport struct {
	// +kubebuilder:validation:Enum=TCP;TLS
	Protocol    string          `json:"protocol"`
	ServerName  string          `json:"serverName,omitempty"`
	CABundleRef *LocalSecretRef `json:"caBundleRef,omitempty"`
}

// NetworkLoadBalancerRouteSpec attaches backends to named listeners.
type NetworkLoadBalancerRouteSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +listType=atomic
	ParentRefs []NetworkLoadBalancerRouteParentRef `json:"parentRefs"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	Backends []NetworkLoadBalancerRouteBackend `json:"backends"`

	// SNI routing is capability-gated until TLS listeners are served.
	// +kubebuilder:validation:Optional
	SNIHostnames []string `json:"sniHostnames,omitempty"`
}

// NetworkLoadBalancerRouteParentStatus reports attachment and effectiveness
// for one parent listener. The oldest accepted catch-all route is effective.
type NetworkLoadBalancerRouteParentStatus struct {
	Name      string `json:"name"`
	Listener  string `json:"listener"`
	Effective bool   `json:"effective"`

	// Conditions include Accepted, ResolvedRefs, and Programmed with
	// observedGeneration.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type NetworkLoadBalancerRouteStatus struct {
	// +kubebuilder:validation:MaxItems=16
	// +listType=map
	// +listMapKey=name
	// +listMapKey=listener
	Parents []NetworkLoadBalancerRouteParentStatus `json:"parents,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// NetworkLoadBalancerRoute connects load balancer listeners to NetworkService
// backends. References are restricted to the route's namespace.
type NetworkLoadBalancerRoute struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec   NetworkLoadBalancerRouteSpec   `json:"spec"`
	Status NetworkLoadBalancerRouteStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NetworkLoadBalancerRouteList contains a list of NetworkLoadBalancerRoute objects.
type NetworkLoadBalancerRouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkLoadBalancerRoute `json:"items"`
}
