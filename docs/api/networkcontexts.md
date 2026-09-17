# API Reference

Packages:

- [networking.datumapis.com/v1alpha](#networkingdatumapiscomv1alpha)

# networking.datumapis.com/v1alpha

Resource Types:

- [NetworkContext](#networkcontext)




## NetworkContext
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha )</sup></sup>






NetworkContext is the Schema for the networkcontexts API

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
      <td>NetworkContext</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#networkcontextspec">spec</a></b></td>
        <td>object</td>
        <td>
          NetworkContextSpec defines the desired state of NetworkContext<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkcontextstatus">status</a></b></td>
        <td>object</td>
        <td>
          NetworkContextStatus defines the observed state of NetworkContext<br/>
          <br/>
            <i>Default</i>: map[conditions:[map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:Ready]]]<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkContext.spec
<sup><sup>[↩ Parent](#networkcontext)</sup></sup>



NetworkContextSpec defines the desired state of NetworkContext

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
        <td><b><a href="#networkcontextspeclocation">location</a></b></td>
        <td>object</td>
        <td>
          The location of where a network context exists.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkcontextspecnetwork">network</a></b></td>
        <td>object</td>
        <td>
          The attached network<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkcontextspecegress">egress</a></b></td>
        <td>object</td>
        <td>
          Egress is what the network reaches outside the platform from this
location, projected from the Network and resolved against the serving
class. Propagation to a cell carries spec and not status, so the
instruction a cell acts on lives here and the result it reports lives in
status.

A reader that finds this unset must refuse rather than assume: a context
written before this field existed carries nothing, which is not the same
as a network that reaches nothing.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>ipFamilies</b></td>
        <td>[]enum</td>
        <td>
          IP families the network carries, projected from the Network.

A reader that finds this unset must refuse rather than assume a family:
a context written before this field existed carries nothing, which is not
the same as a network that carries nothing.<br/>
          <br/>
            <i>Enum</i>: IPv4, IPv6<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>mtu</b></td>
        <td>integer</td>
        <td>
          MTU of interfaces on the network, projected from the Network.<br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Minimum</i>: 1300<br/>
            <i>Maximum</i>: 8856<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>networkGeneration</b></td>
        <td>integer</td>
        <td>
          The Network generation the projected fields were read from, so an operator
comparing this to the Network can tell whether this location has caught up.<br/>
          <br/>
            <i>Format</i>: int64<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkContext.spec.location
<sup><sup>[↩ Parent](#networkcontextspec)</sup></sup>



The location of where a network context exists.

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
          Name of a datum location<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkContext.spec.network
<sup><sup>[↩ Parent](#networkcontextspec)</sup></sup>



The attached network

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


### NetworkContext.spec.egress
<sup><sup>[↩ Parent](#networkcontextspec)</sup></sup>



Egress is what the network reaches outside the platform from this
location, projected from the Network and resolved against the serving
class. Propagation to a cell carries spec and not status, so the
instruction a cell acts on lives here and the result it reports lives in
status.

A reader that finds this unset must refuse rather than assume: a context
written before this field existed carries nothing, which is not the same
as a network that reaches nothing.

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
        <td><b><a href="#networkcontextspecegressinternet">internet</a></b></td>
        <td>object</td>
        <td>
          Internet is the internet egress this location is instructed to provide.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkContext.spec.egress.internet
<sup><sup>[↩ Parent](#networkcontextspecegress)</sup></sup>



Internet is the internet egress this location is instructed to provide.

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
        <td><b>className</b></td>
        <td>string</td>
        <td>
          ClassName is the InternetEgressClass resolved for this network,
including the case where the network named none and the default class
was selected. It is written resolved so class selection stays with the
single writer that reads the classes, and a location never repeats it.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>mode</b></td>
        <td>enum</td>
        <td>
          Mode is whether instances in this location reach the internet, copied
from the network.

It carries no default. A defaulted Disabled could not be told apart from
a field never projected, and a reader that cannot tell those apart must
refuse rather than withdraw egress a consumer asked for.<br/>
          <br/>
            <i>Enum</i>: Enabled, Disabled<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkcontextspecegressinternetparametersref">parametersRef</a></b></td>
        <td>object</td>
        <td>
          ParametersRef is the serving class's parametersRef, passed through
verbatim. Nothing on the path between the class and the controller named
in the class's controllerName interprets it.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>reach</b></td>
        <td>[]enum</td>
        <td>
          Reach are the destination address families this location is instructed
to reach, copied from the network and narrowed to what the serving class
reaches.

Only IPv6 is accepted, because a projection may not carry what its
source cannot declare.<br/>
          <br/>
            <i>Validations</i>:<li>self.all(f, f == 'IPv6'): Only IPv6 is accepted; reaching IPv4 destinations needs a resolver and a translator sharing a prefix, and the platform pairs neither</li><li>self.all(f, self.exists_one(g, g == f)): Each address family may be listed at most once</li>
            <i>Enum</i>: IPv4, IPv6<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>sharing</b></td>
        <td>enum</td>
        <td>
          Sharing is the serving class's sharing, carried so a location can report
the stability a consumer reads back on status without reading the class
itself.<br/>
          <br/>
            <i>Enum</i>: Shared, Dedicated<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkContext.spec.egress.internet.parametersRef
<sup><sup>[↩ Parent](#networkcontextspecegressinternet)</sup></sup>



ParametersRef is the serving class's parametersRef, passed through
verbatim. Nothing on the path between the class and the controller named
in the class's controllerName interprets it.

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
        <td><b>group</b></td>
        <td>string</td>
        <td>
          Group of the referent.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          Kind of the referent.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          Name of the referent.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkContext.status
<sup><sup>[↩ Parent](#networkcontext)</sup></sup>



NetworkContextStatus defines the observed state of NetworkContext

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
        <td><b><a href="#networkcontextstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          Represents the observations of a network context's current state.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkcontextstatusegress">egress</a></b></td>
        <td>object</td>
        <td>
          Egress reports what the network reaches outside the platform from this
location. Egress is realized per location, so a network present in two
locations reports an answer on each context rather than one answer on
the network.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkcontextstatusipam">ipam</a></b></td>
        <td>object</td>
        <td>
          IPAM reports the address space IPAM holds for this network in this
location.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkContext.status.conditions[index]
<sup><sup>[↩ Parent](#networkcontextstatus)</sup></sup>



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


### NetworkContext.status.egress
<sup><sup>[↩ Parent](#networkcontextstatus)</sup></sup>



Egress reports what the network reaches outside the platform from this
location. Egress is realized per location, so a network present in two
locations reports an answer on each context rather than one answer on
the network.

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
        <td><b><a href="#networkcontextstatusegressinternet">internet</a></b></td>
        <td>object</td>
        <td>
          Internet reports the internet egress realized for this location.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkContext.status.egress.internet
<sup><sup>[↩ Parent](#networkcontextstatusegress)</sup></sup>



Internet reports the internet egress realized for this location.

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
        <td><b>dns64Prefix</b></td>
        <td>string</td>
        <td>
          DNS64Prefix is the prefix the platform's resolver synthesizes addresses
under for names publishing no IPv6 record. Reaching an IPv4 destination
by name works only for instances using a resolver that shares this
prefix with the translator.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkcontextstatusegressinternetsourceaddressesindex">sourceAddresses</a></b></td>
        <td>[]object</td>
        <td>
          SourceAddresses are the addresses translation writes onto outbound
packets from this location, with the reliance each one carries.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkContext.status.egress.internet.sourceAddresses[index]
<sup><sup>[↩ Parent](#networkcontextstatusegressinternet)</sup></sup>



InternetEgressSourceAddress is one address outbound traffic leaves on.

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
        <td><b>address</b></td>
        <td>string</td>
        <td>
          Address is the source address translation writes, without a prefix
length.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>family</b></td>
        <td>enum</td>
        <td>
          Family is the address family of this source address.<br/>
          <br/>
            <i>Enum</i>: IPv4, IPv6<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>stability</b></td>
        <td>enum</td>
        <td>
          Stability states how far a consumer may rely on this address before
they act on it. It is the consumer-side projection of the serving
class's sharing.<br/>
          <br/>
            <i>Enum</i>: None, Network<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkContext.status.ipam
<sup><sup>[↩ Parent](#networkcontextstatus)</sup></sup>



IPAM reports the address space IPAM holds for this network in this
location.

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
        <td><b><a href="#networkcontextstatusipamipv6claimref">ipv6ClaimRef</a></b></td>
        <td>object</td>
        <td>
          IPv6ClaimRef names what holds the /64 in IPAM. Deleting the claim it
names releases what this operator holds.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkcontextstatusipamipv6subnetref">ipv6SubnetRef</a></b></td>
        <td>object</td>
        <td>
          IPv6SubnetRef names the Subnet publishing this location's /64.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkContext.status.ipam.ipv6ClaimRef
<sup><sup>[↩ Parent](#networkcontextstatusipam)</sup></sup>



IPv6ClaimRef names what holds the /64 in IPAM. Deleting the claim it
names releases what this operator holds.

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
        <td><b>claimName</b></td>
        <td>string</td>
        <td>
          ClaimName is the IPClaim this operator holds against the prefix.
Deleting it releases what the operator holds.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>namespace</b></td>
        <td>string</td>
        <td>
          Namespace is the project namespace holding the claim.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>poolName</b></td>
        <td>string</td>
        <td>
          PoolName is the IPPool IPAM provisioned for the prefix. Subnet and
endpoint addresses are drawn from it.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>project</b></td>
        <td>string</td>
        <td>
          Project is the control plane the objects live in.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkContext.status.ipam.ipv6SubnetRef
<sup><sup>[↩ Parent](#networkcontextstatusipam)</sup></sup>



IPv6SubnetRef names the Subnet publishing this location's /64.

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
      </tr></tbody>
</table>
