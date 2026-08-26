# API Reference

Packages:

- [networking.datumapis.com/v1alpha](#networkingdatumapiscomv1alpha)

# networking.datumapis.com/v1alpha

Resource Types:

- [NetworkService](#networkservice)




## NetworkService
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha )</sup></sup>






NetworkService is a named set of endpoints spanning every location a consumer
runs in. Members are selected by label, and a proxy names the service as its
backend.

Anycast brings each request to the closest edge. The service covers the rest:
that edge ranks the service's locations by distance from itself, serves the
request from the best one, and moves to the next if it fails. None of that is
configured here.

A service selects network interfaces, which belong to the networking API.
Nothing in it names a workload, so anything that holds an interface can be
put behind one.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
      <td><b>apiVersion</b></td>
      <td>string</td>
      <td>networking.datumapis.com/v1alpha</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b>kind</b></td>
      <td>string</td>
      <td>NetworkService</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#networkservicespec">spec</a></b></td>
        <td>object</td>
        <td>
          NetworkServiceSpec defines the desired state of NetworkService. It names the
members and the ports they answer on, and nothing about where traffic should
go: the platform decides that from where each request arrived.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkservicestatus">status</a></b></td>
        <td>object</td>
        <td>
          NetworkServiceStatus defines the observed state of NetworkService. It answers
which location serves a consumer's users and whether any location is out of
rotation, which is what makes an over-broad or stale selector visible instead
of silent.<br/>
          <br/>
            <i>Default</i>: map[conditions:[map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:MembersResolved] map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:EndpointsReachable] map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:Ready]]]<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkService.spec
<sup><sup>[↩ Parent](#networkservice)</sup></sup>



NetworkServiceSpec defines the desired state of NetworkService. It names the
members and the ports they answer on, and nothing about where traffic should
go: the platform decides that from where each request arrived.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#networkservicespecnetworkinterfaces">networkInterfaces</a></b></td>
        <td>object</td>
        <td>
          networkInterfaces selects the interfaces that make up the service's
membership. An interface joins when it matches and a workload holds it,
and it is healthy once whatever holds it reports itself available to serve.
It leaves when it stops matching, when its workload releases it, or when it
goes away.

Membership tracks reality rather than a list, so instances appearing,
disappearing, and moving between locations need no edit here.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkservicespecportsindex">ports</a></b></td>
        <td>[]object</td>
        <td>
          ports are the ports the service's members answer on. A backend referencing
this service names one of them.<br/>
          <br/>
            <i>Validations</i>:<li>self.all(p1, self.exists_one(p2, p2.name == p1.name)): Port name must be unique within the service</li><li>self.all(p1, self.exists_one(p2, p2.port == p1.port)): Port number must be unique within the service</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkservicespectrafficdistribution">trafficDistribution</a></b></td>
        <td>object</td>
        <td>
          trafficDistribution is how traffic is spread across the locations the
service has members in. Leave it unset: the default serves each request
from the location nearest the edge that received it, which is what a
consumer wants without saying so.<br/>
          <br/>
            <i>Default</i>: map[strategy:Nearest]<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkService.spec.networkInterfaces
<sup><sup>[↩ Parent](#networkservicespec)</sup></sup>



networkInterfaces selects the interfaces that make up the service's
membership. An interface joins when it matches and a workload holds it,
and it is healthy once whatever holds it reports itself available to serve.
It leaves when it stops matching, when its workload releases it, or when it
goes away.

Membership tracks reality rather than a list, so instances appearing,
disappearing, and moving between locations need no edit here.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#networkservicespecnetworkinterfacesselector">selector</a></b></td>
        <td>object</td>
        <td>
          selector is a standard label selector matched against network interfaces.
It reaches only interfaces the consumer owns.

Every interface carries a defined set of labels, so the facts worth
selecting on are present without labelling anything first. Networking sets
networking.datumapis.com/location on every interface; compute sets keys
such as compute.datumapis.com/workload-name on the interfaces its
workloads hold. Selecting a whole application by workload name is the
common case, and adding keys narrows the membership to one placement or
one location.

Adding the location key restricts which interfaces are members. It does
not steer traffic: serving users from the location nearest them requires
no configuration.

The selector must constrain something. An empty selector would make every
interface in the namespace a member, which is never what a service means.

An interface no workload holds any more is never a member, whatever it is
labelled: its addresses are retired capacity and nothing answers on them.

A selector matching interfaces across more than one network is a
configuration error, reported on the MembersResolved condition. A service
spans one network.<br/>
          <br/>
            <i>Validations</i>:<li>(has(self.matchLabels) && size(self.matchLabels) > 0) || (has(self.matchExpressions) && size(self.matchExpressions) > 0): selector must set matchLabels or matchExpressions</li>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkService.spec.networkInterfaces.selector
<sup><sup>[↩ Parent](#networkservicespecnetworkinterfaces)</sup></sup>



selector is a standard label selector matched against network interfaces.
It reaches only interfaces the consumer owns.

Every interface carries a defined set of labels, so the facts worth
selecting on are present without labelling anything first. Networking sets
networking.datumapis.com/location on every interface; compute sets keys
such as compute.datumapis.com/workload-name on the interfaces its
workloads hold. Selecting a whole application by workload name is the
common case, and adding keys narrows the membership to one placement or
one location.

Adding the location key restricts which interfaces are members. It does
not steer traffic: serving users from the location nearest them requires
no configuration.

The selector must constrain something. An empty selector would make every
interface in the namespace a member, which is never what a service means.

An interface no workload holds any more is never a member, whatever it is
labelled: its addresses are retired capacity and nothing answers on them.

A selector matching interfaces across more than one network is a
configuration error, reported on the MembersResolved condition. A service
spans one network.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#networkservicespecnetworkinterfacesselectormatchexpressionsindex">matchExpressions</a></b></td>
        <td>[]object</td>
        <td>
          matchExpressions is a list of label selector requirements. The requirements are ANDed.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>matchLabels</b></td>
        <td>map[string]string</td>
        <td>
          matchLabels is a map of {key,value} pairs. A single {key,value} in the matchLabels
map is equivalent to an element of matchExpressions, whose key field is "key", the
operator is "In", and the values array contains only "value". The requirements are ANDed.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkService.spec.networkInterfaces.selector.matchExpressions[index]
<sup><sup>[↩ Parent](#networkservicespecnetworkinterfacesselector)</sup></sup>



A label selector requirement is a selector that contains values, a key, and an operator that
relates the key and values.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>key</b></td>
        <td>string</td>
        <td>
          key is the label key that the selector applies to.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>operator</b></td>
        <td>string</td>
        <td>
          operator represents a key's relationship to a set of values.
Valid operators are In, NotIn, Exists and DoesNotExist.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>values</b></td>
        <td>[]string</td>
        <td>
          values is an array of string values. If the operator is In or NotIn,
the values array must be non-empty. If the operator is Exists or DoesNotExist,
the values array must be empty. This array is replaced during a strategic
merge patch.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkService.spec.ports[index]
<sup><sup>[↩ Parent](#networkservicespec)</sup></sup>



NetworkServicePort is one port the service's members answer on.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          name identifies the port within the service, and is what a backend
referencing this service names. Naming a port rather than a number lets
the reference survive a port change.

Must be a DNS label and unique within the service.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>port</b></td>
        <td>integer</td>
        <td>
          port is the port number every member answers on. It is the port on the
member itself, not one the platform publishes.

Unique within the service.<br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Minimum</i>: 1<br/>
            <i>Maximum</i>: 65535<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>protocol</b></td>
        <td>enum</td>
        <td>
          protocol is the transport the port carries. TCP is the only value
accepted, and is what a backend served through a proxy uses. The field
exists so the same service can back a Layer 4 load balancer without
changing shape.<br/>
          <br/>
            <i>Enum</i>: TCP<br/>
            <i>Default</i>: TCP<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkService.spec.trafficDistribution
<sup><sup>[↩ Parent](#networkservicespec)</sup></sup>



trafficDistribution is how traffic is spread across the locations the
service has members in. Leave it unset: the default serves each request
from the location nearest the edge that received it, which is what a
consumer wants without saying so.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>strategy</b></td>
        <td>enum</td>
        <td>
          strategy is how a location is chosen for each request. Nearest is the only
value accepted today: each edge prefers the service's location closest to
itself and falls back down its own ranking when that location cannot
serve.

The field carries one value deliberately. A consumer who sets it today
keeps working when further strategies are added.<br/>
          <br/>
            <i>Enum</i>: Nearest<br/>
            <i>Default</i>: Nearest<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkService.status
<sup><sup>[↩ Parent](#networkservice)</sup></sup>



NetworkServiceStatus defines the observed state of NetworkService. It answers
which location serves a consumer's users and whether any location is out of
rotation, which is what makes an over-broad or stale selector visible instead
of silent.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b><a href="#networkservicestatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          conditions report the current state of the service. Wait on Ready, which
is true once membership has resolved and the edge is reaching the members
it resolved to.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkservicestatuslocationsindex">locations</a></b></td>
        <td>[]object</td>
        <td>
          locations report the members the service has in each location it reaches,
how many of them are healthy, and whether the location is taking traffic.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkservicestatussummary">summary</a></b></td>
        <td>object</td>
        <td>
          summary totals the locations below.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkService.status.conditions[index]
<sup><sup>[↩ Parent](#networkservicestatus)</sup></sup>



Condition contains details for one aspect of the current state of this API Resource.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>lastTransitionTime</b></td>
        <td>string</td>
        <td>
          lastTransitionTime is the last time the condition transitioned from one status to another.
This should be when the underlying condition changed.  If that is not known, then using the time when the API field changed is acceptable.<br/>
          <br/>
            <i>Format</i>: date-time<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>message</b></td>
        <td>string</td>
        <td>
          message is a human readable message indicating details about the transition.
This may be an empty string.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>reason</b></td>
        <td>string</td>
        <td>
          reason contains a programmatic identifier indicating the reason for the condition's last transition.
Producers of specific condition types may define expected values and meanings for this field,
and whether the values are considered a guaranteed API.
The value should be a CamelCase string.
This field may not be empty.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>status</b></td>
        <td>enum</td>
        <td>
          status of the condition, one of True, False, Unknown.<br/>
          <br/>
            <i>Enum</i>: True, False, Unknown<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>type</b></td>
        <td>string</td>
        <td>
          type of condition in CamelCase or in foo.example.com/CamelCase.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>observedGeneration</b></td>
        <td>integer</td>
        <td>
          observedGeneration represents the .metadata.generation that the condition was set based upon.
For instance, if .metadata.generation is currently 12, but the .status.conditions[x].observedGeneration is 9, the condition is out of date
with respect to the current state of the instance.<br/>
          <br/>
            <i>Format</i>: int64<br/>
            <i>Minimum</i>: 0<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkService.status.locations[index]
<sup><sup>[↩ Parent](#networkservicestatus)</sup></sup>



NetworkServiceLocationStatus reports the members a service has in one
location, and whether that location is taking traffic.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          name is the location, as it appears in the
networking.datumapis.com/location label on the interfaces.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>healthy</b></td>
        <td>integer</td>
        <td>
          healthy is how many of this location's members are currently taking
traffic. It falls below members when the edge ejects a member whose
requests are failing.<br/>
          <br/>
            <i>Format</i>: int32<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>members</b></td>
        <td>integer</td>
        <td>
          members is how many interfaces in this location are members of the
service.<br/>
          <br/>
            <i>Format</i>: int32<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>serving</b></td>
        <td>boolean</td>
        <td>
          serving reports whether this location is in rotation. It goes false when
too few members are healthy for the location to be worth sending traffic
to, which is what moves traffic down each edge's ranking.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkService.status.summary
<sup><sup>[↩ Parent](#networkservicestatus)</sup></sup>



summary totals the locations below.

<table>
    <thead>
        <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Description</th>
            <th>Required</th>
        </tr>
    </thead>
    <tbody><tr>
        <td><b>healthy</b></td>
        <td>integer</td>
        <td>
          healthy is how many of those members are currently taking traffic.<br/>
          <br/>
            <i>Format</i>: int32<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>locations</b></td>
        <td>integer</td>
        <td>
          locations is how many locations the service has members in.<br/>
          <br/>
            <i>Format</i>: int32<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>members</b></td>
        <td>integer</td>
        <td>
          members is how many interfaces are members of the service, across every
location.<br/>
          <br/>
            <i>Format</i>: int32<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>
