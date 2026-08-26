// SPDX-License-Identifier: AGPL-3.0-only

package v1alpha

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// NetworkServiceMembersResolved reports that the selector was evaluated
	// against the network interfaces the consumer owns and produced a
	// membership. It is false when the selector matches nothing, which is the
	// ordinary state of a service written before the workload behind it, and
	// when the matched interfaces span more than one network.
	NetworkServiceMembersResolved = "MembersResolved"

	// NetworkServiceReady reports that the service has resolved membership and
	// at least one location is taking traffic, so a proxy naming it has
	// somewhere to send requests. Wait on this one rather than on what it
	// summarizes.
	NetworkServiceReady = "Ready"
)

const (
	// NetworkServiceReasonNoMatchingInterfaces means the selector matched no
	// network interface holding capacity for the consumer. A service is commonly
	// written before the workload behind it exists, so this is reported rather
	// than treated as an error, and it clears on its own once interfaces appear.
	//
	// An interface no workload holds any more reads the same way as one that
	// never existed: it is retired capacity and cannot serve.
	NetworkServiceReasonNoMatchingInterfaces = "NoMatchingInterfaces"

	// NetworkServiceReasonMultipleNetworks means the selector matched interfaces
	// on more than one network. A service spans one network, so the membership is
	// not resolved rather than silently narrowed to one of them.
	NetworkServiceReasonMultipleNetworks = "MultipleNetworks"

	// NetworkServiceReasonNoServingLocations means every location the service has
	// members in is out of rotation, so no location can take traffic.
	NetworkServiceReasonNoServingLocations = "NoServingLocations"
)

// NetworkServiceProtocol is the transport protocol a port carries.
//
// +kubebuilder:validation:Enum=TCP
type NetworkServiceProtocol string

const (
	// NetworkServiceProtocolTCP carries TCP, which is what every backend served
	// through a proxy uses.
	NetworkServiceProtocolTCP NetworkServiceProtocol = "TCP"
)

// NetworkServiceTrafficDistributionStrategy is how traffic is spread across the
// locations a service has members in.
//
// +kubebuilder:validation:Enum=Nearest
type NetworkServiceTrafficDistributionStrategy string

const (
	// NetworkServiceTrafficDistributionStrategyNearest serves each request from
	// the location closest to the edge that received it, and treats the
	// remaining locations as an ordered fallback. Every edge ranks the locations
	// for itself, so one service produces correct local behaviour everywhere
	// without the consumer expressing a matrix of locations against edges.
	NetworkServiceTrafficDistributionStrategyNearest NetworkServiceTrafficDistributionStrategy = "Nearest"
)

// NetworkServiceInterfaceSelector selects the network interfaces that make up a
// service's membership.
type NetworkServiceInterfaceSelector struct {
	// selector is a standard label selector matched against network interfaces.
	// It reaches only interfaces the consumer owns.
	//
	// Every interface carries a defined set of labels, so the facts worth
	// selecting on are present without labelling anything first. Networking sets
	// networking.datumapis.com/location on every interface; compute sets keys
	// such as compute.datumapis.com/workload-name on the interfaces its
	// workloads hold. Selecting a whole application by workload name is the
	// common case, and adding keys narrows the membership to one placement or
	// one location.
	//
	// Adding the location key restricts which interfaces are members. It does
	// not steer traffic: serving users from the location nearest them requires
	// no configuration.
	//
	// The selector must constrain something. An empty selector would make every
	// interface in the namespace a member, which is never what a service means.
	//
	// An interface no workload holds any more is never a member, whatever it is
	// labelled: its addresses are retired capacity and nothing answers on them.
	//
	// A selector matching interfaces across more than one network is a
	// configuration error, reported on the MembersResolved condition. A service
	// spans one network.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:message="selector must set matchLabels or matchExpressions",rule="(has(self.matchLabels) && size(self.matchLabels) > 0) || (has(self.matchExpressions) && size(self.matchExpressions) > 0)"
	Selector metav1.LabelSelector `json:"selector"`
}

// NetworkServicePort is one port the service's members answer on.
type NetworkServicePort struct {
	// name identifies the port within the service, and is what a backend
	// referencing this service names. Naming a port rather than a number lets
	// the reference survive a port change.
	//
	// Must be a DNS label and unique within the service.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`

	// port is the port number every member answers on. It is the port on the
	// member itself, not one the platform publishes.
	//
	// Unique within the service.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// protocol is the transport the port carries. TCP is the only value
	// accepted, and is what a backend served through a proxy uses. The field
	// exists so the same service can back a Layer 4 load balancer without
	// changing shape.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="TCP"
	Protocol NetworkServiceProtocol `json:"protocol,omitempty"`
}

// NetworkServiceTrafficDistribution is how the platform spreads traffic across
// the service's locations.
type NetworkServiceTrafficDistribution struct {
	// strategy is how a location is chosen for each request. Nearest is the only
	// value accepted today: each edge prefers the service's location closest to
	// itself and falls back down its own ranking when that location cannot
	// serve.
	//
	// The field carries one value deliberately. A consumer who sets it today
	// keeps working when further strategies are added.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="Nearest"
	Strategy NetworkServiceTrafficDistributionStrategy `json:"strategy,omitempty"`
}

// NetworkServiceSpec defines the desired state of NetworkService. It names the
// members and the ports they answer on, and nothing about where traffic should
// go: the platform decides that from where each request arrived.
type NetworkServiceSpec struct {
	// networkInterfaces selects the interfaces that make up the service's
	// membership. An interface joins when it matches and a workload holds it,
	// and it is healthy once whatever holds it reports itself available to serve.
	// It leaves when it stops matching, when its workload releases it, or when it
	// goes away.
	//
	// Membership tracks reality rather than a list, so instances appearing,
	// disappearing, and moving between locations need no edit here.
	//
	// +kubebuilder:validation:Required
	NetworkInterfaces NetworkServiceInterfaceSelector `json:"networkInterfaces"`

	// ports are the ports the service's members answer on. A backend referencing
	// this service names one of them.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:XValidation:message="Port name must be unique within the service",rule="self.all(p1, self.exists_one(p2, p2.name == p1.name))"
	// +kubebuilder:validation:XValidation:message="Port number must be unique within the service",rule="self.all(p1, self.exists_one(p2, p2.port == p1.port))"
	Ports []NetworkServicePort `json:"ports"`

	// trafficDistribution is how traffic is spread across the locations the
	// service has members in. Leave it unset: the default serves each request
	// from the location nearest the edge that received it, which is what a
	// consumer wants without saying so.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:default={strategy:"Nearest"}
	TrafficDistribution NetworkServiceTrafficDistribution `json:"trafficDistribution,omitempty"`
}

// NetworkServiceLocationStatus reports the members a service has in one
// location, and whether that location is taking traffic.
type NetworkServiceLocationStatus struct {
	// name is the location, as it appears in the
	// networking.datumapis.com/location label on the interfaces.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`

	// members is how many interfaces in this location are members of the
	// service.
	//
	// +kubebuilder:validation:Optional
	Members int32 `json:"members"`

	// healthy is how many of this location's members are currently taking
	// traffic. It falls below members when the edge ejects a member whose
	// requests are failing.
	//
	// +kubebuilder:validation:Optional
	Healthy int32 `json:"healthy"`

	// serving reports whether this location is in rotation. It goes false when
	// too few members are healthy for the location to be worth sending traffic
	// to, which is what moves traffic down each edge's ranking.
	//
	// +kubebuilder:validation:Optional
	Serving bool `json:"serving"`
}

// NetworkServiceSummary totals the per-location rollup, so the counts a
// consumer reads first do not require adding up a list.
type NetworkServiceSummary struct {
	// locations is how many locations the service has members in.
	//
	// +kubebuilder:validation:Optional
	Locations int32 `json:"locations"`

	// members is how many interfaces are members of the service, across every
	// location.
	//
	// +kubebuilder:validation:Optional
	Members int32 `json:"members"`

	// healthy is how many of those members are currently taking traffic.
	//
	// +kubebuilder:validation:Optional
	Healthy int32 `json:"healthy"`
}

// NetworkServiceStatus defines the observed state of NetworkService. It answers
// which location serves a consumer's users and whether any location is out of
// rotation, which is what makes an over-broad or stale selector visible instead
// of silent.
type NetworkServiceStatus struct {
	// summary totals the locations below.
	//
	// +kubebuilder:validation:Optional
	Summary NetworkServiceSummary `json:"summary,omitempty"`

	// locations report the members the service has in each location it reaches,
	// how many of them are healthy, and whether the location is taking traffic.
	//
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=name
	Locations []NetworkServiceLocationStatus `json:"locations,omitempty"`

	// conditions report the current state of the service. Wait on Ready, which
	// is true once membership has resolved and the edge is reaching the members
	// it resolved to.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// NetworkService is a named set of endpoints spanning every location a consumer
// runs in. Members are selected by label, and a proxy names the service as its
// backend.
//
// Anycast brings each request to the closest edge. The service covers the rest:
// that edge ranks the service's locations by distance from itself, serves the
// request from the best one, and moves to the next if it fails. None of that is
// configured here.
//
// A service selects network interfaces, which belong to the networking API.
// Nothing in it names a workload, so anything that holds an interface can be
// put behind one.
// +kubebuilder:printcolumn:name="Locations",type=integer,JSONPath=".status.summary.locations"
// +kubebuilder:printcolumn:name="Members",type=integer,JSONPath=".status.summary.members"
// +kubebuilder:printcolumn:name="Healthy",type=integer,JSONPath=".status.summary.healthy"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`,priority=1
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type NetworkService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +kubebuilder:validation:Required
	Spec NetworkServiceSpec `json:"spec,omitempty"`

	// +kubebuilder:default={conditions:{{type:"MembersResolved",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"},{type:"Ready",status:"Unknown",reason:"Pending", message:"Waiting for controller", lastTransitionTime: "1970-01-01T00:00:00Z"}}}
	Status NetworkServiceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NetworkServiceList contains a list of NetworkService.
type NetworkServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NetworkService `json:"items"`
}
