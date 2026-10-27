// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// NetworkLoadBalancerCellIntentState distinguishes desired backend validation
// from an explicit withdrawal. Neither state authorizes traffic programming.
// +kubebuilder:validation:Enum=Validate;ValidateAllocation;Withdrawn
type NetworkLoadBalancerCellIntentState string

const (
	NetworkLoadBalancerCellIntentValidate           NetworkLoadBalancerCellIntentState = "Validate"
	NetworkLoadBalancerCellIntentValidateAllocation NetworkLoadBalancerCellIntentState = "ValidateAllocation"
	NetworkLoadBalancerCellIntentWithdrawn          NetworkLoadBalancerCellIntentState = "Withdrawn"
)

const (
	NetworkLoadBalancerCellIntentProgrammed        = "Programmed"
	NetworkLoadBalancerCellIntentAnnounced         = "Announced"
	NetworkLoadBalancerCellIntentAllocationRetired = "AllocationRetired"
)

type NetworkLoadBalancerCellIntentSource struct {
	// +kubebuilder:validation:MinLength=1
	ProjectCluster string `json:"projectCluster"`
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`
	// +optional
	// +kubebuilder:validation:MinLength=1
	RouteName string `json:"routeName,omitempty"`
	// +optional
	// +kubebuilder:validation:MinLength=1
	RouteUID        string `json:"routeUID,omitempty"`
	RouteGeneration int64  `json:"routeGeneration"`
	// +kubebuilder:validation:MinLength=1
	ParentName string `json:"parentName"`
	// +kubebuilder:validation:MinLength=1
	ParentUID        string `json:"parentUID"`
	ParentGeneration int64  `json:"parentGeneration"`
	// +optional
	// +kubebuilder:validation:MinLength=1
	ListenerName string `json:"listenerName,omitempty"`
	// +kubebuilder:validation:MinLength=1
	AddressRequest string `json:"addressRequest"`
}

type NetworkLoadBalancerCellIntentProbe struct {
	Protocol string `json:"protocol"`
	Port     int32  `json:"port"`
	Path     string `json:"path,omitempty"`
}

// +kubebuilder:validation:XValidation:message="exactly one port or portRange is required",rule="has(self.port) != has(self.portRange)"
type NetworkLoadBalancerCellIntentBackend struct {
	// +kubebuilder:validation:MinLength=1
	SourceNamespace string `json:"sourceNamespace"`
	// +kubebuilder:validation:MinLength=1
	SourceName string `json:"sourceName"`
	// +kubebuilder:validation:MinLength=1
	SourceUID string `json:"sourceUID"`
	// Family identifies the backend transport address independently of the
	// public VIP family. IPv4 ingress can use an IPv6 backend through a proxy.
	Family IPFamily `json:"family"`
	// +kubebuilder:validation:MinLength=1
	Address   string                             `json:"address"`
	Port      int32                              `json:"port,omitempty"`
	PortRange *PortRange                         `json:"portRange,omitempty"`
	Probe     NetworkLoadBalancerCellIntentProbe `json:"probe"`
}

type NetworkLoadBalancerCellIntentAddressGroup struct {
	UID          string `json:"uid"`
	Generation   int64  `json:"generation"`
	Revision     string `json:"revision"`
	ListenerName string `json:"listenerName"`
	// +kubebuilder:validation:MaxItems=16
	// +listType=map
	// +listMapKey=name
	Listeners []NetworkLoadBalancerCellIntentAddressListener `json:"listeners"`
}

// +kubebuilder:validation:XValidation:message="exactly one port or portRange is required",rule="has(self.port) != has(self.portRange)"
// +kubebuilder:validation:XValidation:message="true ranges require UDP",rule="!has(self.portRange) || self.portRange.start == self.portRange.end || self.protocol == 'UDP'"
type NetworkLoadBalancerCellIntentAddressListener struct {
	Name      string                      `json:"name"`
	Protocol  NetworkLoadBalancerProtocol `json:"protocol"`
	Port      int32                       `json:"port,omitempty"`
	PortRange *PortRange                  `json:"portRange,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self.uid == oldSelf.uid && self.claimUID == oldSelf.claimUID",message="allocation identity is immutable"
// +kubebuilder:validation:XValidation:rule="!oldSelf.retired || self == oldSelf",message="retired allocations cannot change"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.participants) || (has(self.participants) && oldSelf.participants.all(p, p in self.participants))",message="registered cells cannot be removed"
type NetworkLoadBalancerAllocationAuthority struct {
	Retired bool `json:"retired"`
	// +optional
	// +kubebuilder:validation:MaxItems=1024
	// +listType=set
	Participants []string `json:"participants,omitempty"`
	// +kubebuilder:validation:MinLength=1
	UID string `json:"uid"`
	// +kubebuilder:validation:MinLength=1
	ClaimUID string `json:"claimUID"`
}

// NetworkLoadBalancerCellIntentSpec is a validation request to one location.
// The cell must independently verify the live interface, attachment, and VPC.
// +kubebuilder:validation:XValidation:message="validation needs backends; Withdrawn must have none",rule="self.state != 'Withdrawn' ? has(self.backends) && size(self.backends) > 0 : !has(self.backends) || size(self.backends) == 0"
// +kubebuilder:validation:XValidation:message="coordinated validation requires ValidateAllocation",rule="self.state == 'Withdrawn' || ((self.state == 'ValidateAllocation') == has(self.addressGroup))"
// +kubebuilder:validation:XValidation:message="serving intents require source and listener fields",rule="has(self.allocation) || (has(self.source.routeName) && has(self.source.routeUID) && has(self.source.listenerName) && has(self.protocol) && (has(self.listenerPort) != has(self.listenerPortRange)))"
// +kubebuilder:validation:XValidation:message="retirement cannot contain serving configuration",rule="!has(self.allocation) || (self.state == 'Withdrawn' && !has(self.addressGroup) && !has(self.listenerPort) && !has(self.listenerPortRange) && !has(self.protocol) && !has(self.source.routeName) && !has(self.source.routeUID) && !has(self.source.listenerName))"
// +kubebuilder:validation:XValidation:message="allocation authority identity is immutable",rule="has(oldSelf.allocation) ? has(self.allocation) && self.source == oldSelf.source && self.location == oldSelf.location && self.networkName == oldSelf.networkName && self.vip == oldSelf.vip && self.vipFamily == oldSelf.vipFamily : !has(self.allocation)"
// +kubebuilder:validation:XValidation:message="true listener ranges require UDP and address inventory",rule="!has(self.listenerPortRange) || self.listenerPortRange.start == self.listenerPortRange.end || (self.protocol == 'UDP' && has(self.addressGroup))"
type NetworkLoadBalancerCellIntentSpec struct {
	// +optional
	Allocation   *NetworkLoadBalancerAllocationAuthority    `json:"allocation,omitempty"`
	AddressGroup *NetworkLoadBalancerCellIntentAddressGroup `json:"addressGroup,omitempty"`
	Source       NetworkLoadBalancerCellIntentSource        `json:"source"`
	Location     string                                     `json:"location"`
	NetworkName  string                                     `json:"networkName"`
	// VIPFamily selects the ingress packet path. It may differ from a backend's
	// family; the serving cell must support that family pair before programming.
	VIPFamily         IPFamily                           `json:"vipFamily"`
	VIP               string                             `json:"vip"`
	Protocol          NetworkLoadBalancerProtocol        `json:"protocol,omitempty"`
	ListenerPort      int32                              `json:"listenerPort,omitempty"`
	ListenerPortRange *PortRange                         `json:"listenerPortRange,omitempty"`
	State             NetworkLoadBalancerCellIntentState `json:"state"`
	// +kubebuilder:validation:MaxItems=1024
	Backends []NetworkLoadBalancerCellIntentBackend `json:"backends,omitempty"`
}

// NetworkLoadBalancerCellIntentStatus is written by the serving cell and
// aggregated onto the hub copy. Cells outside the intent's location leave it
// empty.
type NetworkLoadBalancerAllocationRetirementReceipt struct {
	CellUID            string `json:"cellUID"`
	ObservedGeneration int64  `json:"observedGeneration"`
}

type NetworkLoadBalancerCellIntentStatus struct {
	// +optional
	// +kubebuilder:validation:MaxItems=1024
	// +listType=map
	// +listMapKey=cellUID
	AllocationRetirements []NetworkLoadBalancerAllocationRetirementReceipt `json:"allocationRetirements,omitempty"`
	// ObservedGeneration is the intent generation the conditions describe.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions include Programmed and Announced.
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// NetworkLoadBalancerCellIntent carries a scoped, non-serving desired state
// from a project control plane through the hub to the serving cell.
type NetworkLoadBalancerCellIntent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NetworkLoadBalancerCellIntentSpec   `json:"spec"`
	Status NetworkLoadBalancerCellIntentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type NetworkLoadBalancerCellIntentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkLoadBalancerCellIntent `json:"items"`
}
