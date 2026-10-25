// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// +kubebuilder:validation:Enum=TCP;HTTP;HTTPS
type NetworkLoadBalancerProbeProtocol string

const (
	NetworkLoadBalancerProbeTCP   NetworkLoadBalancerProbeProtocol = "TCP"
	NetworkLoadBalancerProbeHTTP  NetworkLoadBalancerProbeProtocol = "HTTP"
	NetworkLoadBalancerProbeHTTPS NetworkLoadBalancerProbeProtocol = "HTTPS"
)

type NetworkLoadBalancerBackendHealthRouteRef struct {
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`
	// +kubebuilder:validation:MinLength=1
	Name string    `json:"name"`
	UID  types.UID `json:"uid"`
	// +kubebuilder:validation:Minimum=1
	Generation int64 `json:"generation"`
}

type NetworkLoadBalancerBackendHealthParentRef struct {
	// +kubebuilder:validation:MinLength=1
	Name string    `json:"name"`
	UID  types.UID `json:"uid"`
	// +kubebuilder:validation:Minimum=1
	Generation int64 `json:"generation"`
	// +kubebuilder:validation:MinLength=1
	Listener string `json:"listener"`
}

// NetworkLoadBalancerProbe describes a resolved active check. Its port is a
// backend port number, not a listener port or a NetworkService port name.
// +kubebuilder:validation:XValidation:message="path is only valid for HTTP and HTTPS",rule="has(self.path) ? self.protocol in ['HTTP', 'HTTPS'] : true"
// +kubebuilder:validation:XValidation:message="serverName is only valid for HTTPS",rule="has(self.serverName) ? self.protocol == 'HTTPS' : true"
type NetworkLoadBalancerProbe struct {
	Protocol NetworkLoadBalancerProbeProtocol `json:"protocol"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^/.*$`
	// +kubebuilder:validation:MaxLength=256
	Path string `json:"path,omitempty"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=253
	ServerName string `json:"serverName,omitempty"`
}

// NetworkLoadBalancerBackendHealthTarget identifies one resolved interface
// endpoint and its probe. The source UID prevents a replacement interface
// reusing a name from inheriting a previous endpoint's health.
type NetworkLoadBalancerBackendHealthTarget struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`
	// +kubebuilder:validation:MinLength=1
	SourceNamespace string `json:"sourceNamespace"`
	// +kubebuilder:validation:MinLength=1
	SourceInterface string    `json:"sourceInterface"`
	SourceUID       types.UID `json:"sourceUID"`
	// +kubebuilder:validation:MinLength=1
	AttachmentName string    `json:"attachmentName"`
	AttachmentUID  types.UID `json:"attachmentUID"`
	// +kubebuilder:validation:MinLength=1
	Address string `json:"address"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	ServingPort int32                    `json:"servingPort"`
	Probe       NetworkLoadBalancerProbe `json:"probe"`
}

// NetworkLoadBalancerBackendHealthSpec carries one route parent and location's
// resolved targets into the cell that can reach them.
type NetworkLoadBalancerBackendHealthSpec struct {
	RouteRef  NetworkLoadBalancerBackendHealthRouteRef  `json:"routeRef"`
	ParentRef NetworkLoadBalancerBackendHealthParentRef `json:"parentRef"`
	// +kubebuilder:validation:MinLength=1
	Location string `json:"location"`
	// +kubebuilder:validation:MinLength=1
	NetworkName string `json:"networkName"`
	// +kubebuilder:validation:MinLength=1
	FabricVPCID string `json:"fabricVPCID"`
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=name
	Targets []NetworkLoadBalancerBackendHealthTarget `json:"targets"`
}

// NetworkLoadBalancerBackendHealthTargetStatus is usable only while its route,
// object generation, target fingerprint, and expiry match current intent.
type NetworkLoadBalancerBackendHealthTargetStatus struct {
	Name                 string      `json:"name"`
	ObservedGeneration   int64       `json:"observedGeneration"`
	RouteUID             types.UID   `json:"routeUID"`
	RouteGeneration      int64       `json:"routeGeneration"`
	TargetFingerprint    string      `json:"targetFingerprint"`
	Healthy              bool        `json:"healthy"`
	ConsecutiveSuccesses int32       `json:"consecutiveSuccesses"`
	LastProbeTime        metav1.Time `json:"lastProbeTime"`
	ValidUntil           metav1.Time `json:"validUntil"`
	Reason               string      `json:"reason,omitempty"`
}

type NetworkLoadBalancerBackendHealthStatus struct {
	// ProbesEvaluated remains Unknown until a VPC-capable runner reports results.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=name
	Targets []NetworkLoadBalancerBackendHealthTargetStatus `json:"targets,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NetworkLoadBalancerBackendHealth is an internal, cell-local probe contract.
// A target is eligible for data plane programming only after a fresh success.
type NetworkLoadBalancerBackendHealth struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              NetworkLoadBalancerBackendHealthSpec `json:"spec"`
	// +kubebuilder:default={conditions:{{type:"ProbesEvaluated",status:"Unknown",reason:"Pending",message:"Waiting for a VPC-capable probe runner",lastTransitionTime:"1970-01-01T00:00:00Z"}}}
	Status NetworkLoadBalancerBackendHealthStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type NetworkLoadBalancerBackendHealthList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkLoadBalancerBackendHealth `json:"items"`
}
