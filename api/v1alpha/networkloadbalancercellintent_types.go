// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// NetworkLoadBalancerCellIntentState distinguishes desired backend validation
// from an explicit withdrawal. Neither state authorizes traffic programming.
// +kubebuilder:validation:Enum=Validate;Withdrawn
type NetworkLoadBalancerCellIntentState string

const (
	NetworkLoadBalancerCellIntentValidate  NetworkLoadBalancerCellIntentState = "Validate"
	NetworkLoadBalancerCellIntentWithdrawn NetworkLoadBalancerCellIntentState = "Withdrawn"
)

type NetworkLoadBalancerCellIntentSource struct {
	// +kubebuilder:validation:MinLength=1
	ProjectCluster string `json:"projectCluster"`
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`
	// +kubebuilder:validation:MinLength=1
	RouteName string `json:"routeName"`
	// +kubebuilder:validation:MinLength=1
	RouteUID        string `json:"routeUID"`
	RouteGeneration int64  `json:"routeGeneration"`
	// +kubebuilder:validation:MinLength=1
	ParentName string `json:"parentName"`
	// +kubebuilder:validation:MinLength=1
	ParentUID        string `json:"parentUID"`
	ParentGeneration int64  `json:"parentGeneration"`
	// +kubebuilder:validation:MinLength=1
	ListenerName string `json:"listenerName"`
	// +kubebuilder:validation:MinLength=1
	AddressRequest string `json:"addressRequest"`
}

type NetworkLoadBalancerCellIntentProbe struct {
	Protocol string `json:"protocol"`
	Port     int32  `json:"port"`
	Path     string `json:"path,omitempty"`
}

type NetworkLoadBalancerCellIntentBackend struct {
	// +kubebuilder:validation:MinLength=1
	SourceNamespace string `json:"sourceNamespace"`
	// +kubebuilder:validation:MinLength=1
	SourceName string `json:"sourceName"`
	// +kubebuilder:validation:MinLength=1
	SourceUID string `json:"sourceUID"`
	// +kubebuilder:validation:MinLength=1
	Address string                             `json:"address"`
	Port    int32                              `json:"port"`
	Weight  int32                              `json:"weight"`
	Probe   NetworkLoadBalancerCellIntentProbe `json:"probe"`
}

// NetworkLoadBalancerCellIntentSpec is a validation request to one location.
// The cell must independently verify the live interface, attachment, and VPC.
// +kubebuilder:validation:XValidation:message="Validate needs backends; Withdrawn must have none",rule="self.state == 'Validate' ? has(self.backends) && size(self.backends) > 0 : !has(self.backends) || size(self.backends) == 0"
type NetworkLoadBalancerCellIntentSpec struct {
	Source       NetworkLoadBalancerCellIntentSource `json:"source"`
	Location     string                              `json:"location"`
	NetworkName  string                              `json:"networkName"`
	VIP          string                              `json:"vip"`
	Protocol     NetworkLoadBalancerProtocol         `json:"protocol"`
	ListenerPort int32                               `json:"listenerPort"`
	State        NetworkLoadBalancerCellIntentState  `json:"state"`
	// +kubebuilder:validation:MaxItems=1024
	Backends []NetworkLoadBalancerCellIntentBackend `json:"backends,omitempty"`
}

// +kubebuilder:object:root=true
// NetworkLoadBalancerCellIntent carries a scoped, non-serving desired state
// from a project control plane through the hub to the serving cell.
type NetworkLoadBalancerCellIntent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec NetworkLoadBalancerCellIntentSpec `json:"spec"`
}

// +kubebuilder:object:root=true

type NetworkLoadBalancerCellIntentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkLoadBalancerCellIntent `json:"items"`
}
