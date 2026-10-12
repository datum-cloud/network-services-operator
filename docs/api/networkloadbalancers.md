# API Reference

Packages:

- [networking.datumapis.com/v1alpha](#networkingdatumapiscomv1alpha)

# networking.datumapis.com/v1alpha

Resource Types:

- [NetworkLoadBalancer](#networkloadbalancer)




## NetworkLoadBalancer
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha )</sup></sup>






NetworkLoadBalancer reserves public IPs and accepts TCP or UDP traffic on
named listeners. Route objects attach backends to those listeners.

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
      <td>NetworkLoadBalancer</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#networkloadbalancerspec">spec</a></b></td>
        <td>object</td>
        <td>
          NetworkLoadBalancerSpec defines the network, public addresses, and listeners.<br/>
          <br/>
            <i>Validations</i>:<li>self.listeners.all(l, l.addressRefs.all(r, r in self.exposure.addresses.map(a, a.name))): each listener address reference must name an address request</li><li>self.listeners.all(l1, self.listeners.all(l2, l1.name == l2.name || l1.protocol != l2.protocol || l1.port != l2.port || !l1.addressRefs.exists(r, r in l2.addressRefs))): listeners sharing an address cannot use the same protocol and port</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkloadbalancerstatus">status</a></b></td>
        <td>object</td>
        <td>
          NetworkLoadBalancerStatus distinguishes address allocation from programming
and announcement. Allocated IPs remain here during backend failures.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkLoadBalancer.spec
<sup><sup>[↩ Parent](#networkloadbalancer)</sup></sup>



NetworkLoadBalancerSpec defines the network, public addresses, and listeners.

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
        <td><b><a href="#networkloadbalancerspecexposure">exposure</a></b></td>
        <td>object</td>
        <td>
          NetworkLoadBalancerExposure contains the load balancer's address requests.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkloadbalancerspeclistenersindex">listeners</a></b></td>
        <td>[]object</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkloadbalancerspecnetworkref">networkRef</a></b></td>
        <td>object</td>
        <td>
          The network must exist in the load balancer's namespace.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkLoadBalancer.spec.exposure
<sup><sup>[↩ Parent](#networkloadbalancerspec)</sup></sup>



NetworkLoadBalancerExposure contains the load balancer's address requests.

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
        <td><b><a href="#networkloadbalancerspecexposureaddressesindex">addresses</a></b></td>
        <td>[]object</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkLoadBalancer.spec.exposure.addresses[index]
<sup><sup>[↩ Parent](#networkloadbalancerspecexposure)</sup></sup>



NetworkLoadBalancerAddressRequest reserves one public address per selected
location, independently of whether a route has healthy backends.

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
        <td><b>family</b></td>
        <td>string</td>
        <td>
          IPv4 ingress is not supported by this API version.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkloadbalancerspecexposureaddressesindexlocations">locations</a></b></td>
        <td>object</td>
        <td>
          NetworkLoadBalancerLocationSelection chooses where an address may be served.<br/>
          <br/>
            <i>Validations</i>:<li>self.from == 'Names' ? has(self.names) && size(self.names) > 0 : !has(self.names): names must be set only when from is Names</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkLoadBalancer.spec.exposure.addresses[index].locations
<sup><sup>[↩ Parent](#networkloadbalancerspecexposureaddressesindex)</sup></sup>



NetworkLoadBalancerLocationSelection chooses where an address may be served.

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
        <td><b>from</b></td>
        <td>enum</td>
        <td>
          NetworkLoadBalancerLocationSource selects locations by name or follows all
eligible locations as they become available.<br/>
          <br/>
            <i>Enum</i>: All, Names<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>names</b></td>
        <td>[]string</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkLoadBalancer.spec.listeners[index]
<sup><sup>[↩ Parent](#networkloadbalancerspec)</sup></sup>



NetworkLoadBalancerListener accepts one transport and port on its named
address requests. TCP and UDP may use the same port.

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
        <td><b>addressRefs</b></td>
        <td>[]string</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>port</b></td>
        <td>integer</td>
        <td>
          <br/>
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
          NetworkLoadBalancerProtocol is the transport accepted by a listener.<br/>
          <br/>
            <i>Enum</i>: TCP, UDP<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkLoadBalancer.spec.networkRef
<sup><sup>[↩ Parent](#networkloadbalancerspec)</sup></sup>



The network must exist in the load balancer's namespace.

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
          The network name<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkLoadBalancer.status
<sup><sup>[↩ Parent](#networkloadbalancer)</sup></sup>



NetworkLoadBalancerStatus distinguishes address allocation from programming
and announcement. Allocated IPs remain here during backend failures.

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
        <td><b><a href="#networkloadbalancerstatusaddressesindex">addresses</a></b></td>
        <td>[]object</td>
        <td>
          <br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkloadbalancerstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions include Allocated, Programmed, and Ready with observedGeneration.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkLoadBalancer.status.addresses[index]
<sup><sup>[↩ Parent](#networkloadbalancerstatus)</sup></sup>



NetworkLoadBalancerAddressStatus describes one allocation for one selected
location. Its address is distinct from allocations in other locations.

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
        <td><b>family</b></td>
        <td>enum</td>
        <td>
          <br/>
          <br/>
            <i>Enum</i>: IPv4, IPv6<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>location</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>requestName</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>value</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkloadbalancerstatusaddressesindexclaimref">claimRef</a></b></td>
        <td>object</td>
        <td>
          NetworkLoadBalancerIPClaimRef identifies the claim holding an allocated IP.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkloadbalancerstatusaddressesindexconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Conditions include Allocated, Programmed, and Announced with
observedGeneration. An entry with no value reports a pending or failed
allocation for this request/location.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkLoadBalancer.status.addresses[index].claimRef
<sup><sup>[↩ Parent](#networkloadbalancerstatusaddressesindex)</sup></sup>



NetworkLoadBalancerIPClaimRef identifies the claim holding an allocated IP.

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
          <br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>project</b></td>
        <td>string</td>
        <td>
          <br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkLoadBalancer.status.addresses[index].conditions[index]
<sup><sup>[↩ Parent](#networkloadbalancerstatusaddressesindex)</sup></sup>



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


### NetworkLoadBalancer.status.conditions[index]
<sup><sup>[↩ Parent](#networkloadbalancerstatus)</sup></sup>



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
