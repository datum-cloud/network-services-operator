// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	NetworkLoadBalancerRouteAccepted     = "Accepted"
	NetworkLoadBalancerRouteResolvedRefs = "ResolvedRefs"
	NetworkLoadBalancerRouteProgrammed   = "Programmed"
)

// NetworkLoadBalancerRouteParentRef attaches a route to one named listener on
// a load balancer in the same namespace.
type NetworkLoadBalancerRouteParentRef struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Listener string `json:"listener"`
}

// NetworkLoadBalancerNetworkServiceRef names a backend service and one of its
// named ports in the route's namespace.
type NetworkLoadBalancerNetworkServiceRef struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	Port string `json:"port"`
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
}

// NetworkLoadBalancerRouteBackend forwards new connections or flows to a
// named NetworkService port. Weight zero disables selection.
type NetworkLoadBalancerRouteBackend struct {
	// +kubebuilder:validation:Required
	NetworkService NetworkLoadBalancerNetworkServiceRef `json:"networkService"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=256
	Weight *int32 `json:"weight,omitempty"`

	// +kubebuilder:validation:Optional
	HealthCheck *NetworkLoadBalancerHealthCheck `json:"healthCheck,omitempty"`
}

// NetworkLoadBalancerRouteSpec attaches weighted backends to named listeners.
type NetworkLoadBalancerRouteSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +listType=map
	// +listMapKey=name
	// +listMapKey=listener
	ParentRefs []NetworkLoadBalancerRouteParentRef `json:"parentRefs"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	Backends []NetworkLoadBalancerRouteBackend `json:"backends"`
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
