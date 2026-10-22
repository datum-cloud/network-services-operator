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
        <td><b><a href="#networkcontextspecdns">dns</a></b></td>
        <td>object</td>
        <td>
          DNS contains resolver settings whose DNS authorization and private path
have both been verified for this location.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkcontextspecegress">egress</a></b></td>
        <td>object</td>
        <td>
          Egress is what the network reaches outside the platform from this
location, projected from the Network. Propagation to a cell carries spec
and not status, so the instruction a cell acts on lives here.<br/>
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


### NetworkContext.spec.dns
<sup><sup>[↩ Parent](#networkcontextspec)</sup></sup>



DNS contains resolver settings whose DNS authorization and private path
have both been verified for this location.

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
        <td><b><a href="#networkcontextspecdnsaccessbindingref">accessBindingRef</a></b></td>
        <td>object</td>
        <td>
          AccessBindingRef identifies the regional DNS configuration.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkcontextspecdnscontextref">contextRef</a></b></td>
        <td>object</td>
        <td>
          ContextRef identifies the DNS serving and namespace lifetime.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>nameservers</b></td>
        <td>[]string</td>
        <td>
          Nameservers are resolver addresses reachable from this network.<br/>
          <br/>
            <i>Validations</i>:<li>self.all(n, isIP(n)): nameservers must contain IP addresses</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>searches</b></td>
        <td>[]string</td>
        <td>
          Searches contains DNS search suffixes allocated to this network.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkContext.spec.dns.accessBindingRef
<sup><sup>[↩ Parent](#networkcontextspecdns)</sup></sup>



AccessBindingRef identifies the regional DNS configuration.

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
          Name is the resource name in the network's namespace.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkContext.spec.dns.contextRef
<sup><sup>[↩ Parent](#networkcontextspecdns)</sup></sup>



ContextRef identifies the DNS serving and namespace lifetime.

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
          Name is the resource name in the network's namespace.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkContext.spec.egress
<sup><sup>[↩ Parent](#networkcontextspec)</sup></sup>



Egress is what the network reaches outside the platform from this
location, projected from the Network. Propagation to a cell carries spec
and not status, so the instruction a cell acts on lives here.

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
          Internet is the internet access this location is instructed to provide.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkContext.spec.egress.internet
<sup><sup>[↩ Parent](#networkcontextspecegress)</sup></sup>



Internet is the internet access this location is instructed to provide.

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
        <td><b>mode</b></td>
        <td>enum</td>
        <td>
          Mode is whether instances in this location reach the internet. It is the
network's mode, or the platform default when the network has not been
stamped with one yet, so a context always carries the mode its location
must enforce.

It carries no default of its own. A context projected before this field
existed carries nothing, and a cell reading one must keep internet access
rather than withdraw it.<br/>
          <br/>
            <i>Enum</i>: Enabled, Disabled<br/>
        </td>
        <td>false</td>
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
        <td><b><a href="#networkcontextstatusdnsaccess">dnsAccess</a></b></td>
        <td>object</td>
        <td>
          DNSAccess identifies the current private path proof. DNSReady=True applies
only to this exact context, binding revision, and deadline.<br/>
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


### NetworkContext.status.dnsAccess
<sup><sup>[↩ Parent](#networkcontextstatus)</sup></sup>



DNSAccess identifies the current private path proof. DNSReady=True applies
only to this exact context, binding revision, and deadline.

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
        <td><b><a href="#networkcontextstatusdnsaccessaccessbindingref">accessBindingRef</a></b></td>
        <td>object</td>
        <td>
          AccessBindingRef identifies the DNS configuration used for the proof.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkcontextstatusdnsaccesscontextref">contextRef</a></b></td>
        <td>object</td>
        <td>
          ContextRef identifies the DNS context used for the proof.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>serviceDestination</b></td>
        <td>string</td>
        <td>
          ServiceDestination identifies this DNS context inside the shared service.<br/>
          <br/>
            <i>Validations</i>:<li>isIP(self): serviceDestination must be an IP address</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>nameservers</b></td>
        <td>[]string</td>
        <td>
          Nameservers lists the resolver frontends whose paths were verified.<br/>
          <br/>
            <i>Validations</i>:<li>self.all(n, isIP(n)): nameservers must contain IP addresses</li>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkContext.status.dnsAccess.accessBindingRef
<sup><sup>[↩ Parent](#networkcontextstatusdnsaccess)</sup></sup>



AccessBindingRef identifies the DNS configuration used for the proof.

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
          Name is the resource name in the network's namespace.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkContext.status.dnsAccess.contextRef
<sup><sup>[↩ Parent](#networkcontextstatusdnsaccess)</sup></sup>



ContextRef identifies the DNS context used for the proof.

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
          Name is the resource name in the network's namespace.<br/>
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
