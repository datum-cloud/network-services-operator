# API Reference

Packages:

- [networking.datumapis.com/v1alpha](#networkingdatumapiscomv1alpha)

# networking.datumapis.com/v1alpha

Resource Types:

- [NetworkInterface](#networkinterface)




## NetworkInterface
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha )</sup></sup>






NetworkInterface is an interface on a network, together with the addresses it
holds. It is the unit that owns addresses: as long as the interface exists,
its addresses stay allocated to it.

You do not create a NetworkInterface. Ask for one with a
NetworkInterfaceClaim, and the operator creates the interface, allocates its
addresses, and binds the two. A provider then reads the interface to
configure a NIC, and reports what it programmed in status.

An interface outlives the instance using it. Whether it outlives the claim
that asked for it depends on spec.reclaimPolicy.

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
      <td>NetworkInterface</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#networkinterfacespec">spec</a></b></td>
        <td>object</td>
        <td>
          NetworkInterfaceSpec defines the desired state of NetworkInterface. It is
written by the operator when a claim is fulfilled, and it carries everything
a provider needs to configure a NIC without reading any other resource.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkinterfacestatus">status</a></b></td>
        <td>object</td>
        <td>
          NetworkInterfaceStatus defines the observed state of NetworkInterface: which
claim holds it, what realizes it on the data plane, and whether programming
has succeeded.<br/>
          <br/>
            <i>Default</i>: map[conditions:[map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:Allocated] map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:Prepared] map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:Programmed] map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:HolderAvailable]]]<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkInterface.spec
<sup><sup>[↩ Parent](#networkinterface)</sup></sup>



NetworkInterfaceSpec defines the desired state of NetworkInterface. It is
written by the operator when a claim is fulfilled, and it carries everything
a provider needs to configure a NIC without reading any other resource.

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
        <td><b><a href="#networkinterfacespecnetwork">network</a></b></td>
        <td>object</td>
        <td>
          network is the network this interface belongs to, in the same namespace as
the interface. It comes from the claim and does not change.<br/>
          <br/>
            <i>Validations</i>:<li>self == oldSelf: network is immutable and cannot be changed after creation</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkinterfacespecaddressesindex">addresses</a></b></td>
        <td>[]object</td>
        <td>
          addresses are the addresses the interface holds inside its network, at most
one per address family, exactly one of them primary. Each carries a prefix
length and, once the location has a subnet, the gateway to route through.<br/>
          <br/>
            <i>Validations</i>:<li>size(self) == 0 || self.filter(a, has(a.primary) && a.primary).size() == 1: Exactly one address must be primary</li><li>self.all(a, self.exists_one(b, b.family == a.family)): Only one address may be held per address family</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>attachmentMode</b></td>
        <td>enum</td>
        <td>
          attachmentMode is how the guest consumes this interface. It comes from the
claim, and the operator carries it without interpreting it.

Netns places the interface in the workload's network namespace. Hypervisor
hands it to a hypervisor as a device, which is what a virtual machine or
microVM guest needs.<br/>
          <br/>
            <i>Enum</i>: Netns, Hypervisor<br/>
            <i>Default</i>: Netns<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkinterfacespecclaimref">claimRef</a></b></td>
        <td>object</td>
        <td>
          claimRef is the claim currently holding this interface. It is empty while a
retained interface waits, unbound, for a claim of its name to return.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkinterfacespecexternaladdressesindex">externalAddresses</a></b></td>
        <td>[]object</td>
        <td>
          externalAddresses are the addresses the interface is reachable at from
outside the network, each mapped onto the interface address of the same
family. They come from the classes the claim requested, and they are absent
for a workload that only needs private addressing.<br/>
          <br/>
            <i>Validations</i>:<li>self.all(a, self.exists_one(b, b.address == a.address)): External addresses must be unique</li><li>self.all(a, self.exists_one(b, b.class == a.class)): Only one external address may be held per address class</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>interfaceName</b></td>
        <td>string</td>
        <td>
          interfaceName is the device name the interface presents to the guest
operating system, such as eth0 or eth1. It comes from the claim.<br/>
          <br/>
            <i>Default</i>: eth0<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>mtu</b></td>
        <td>integer</td>
        <td>
          mtu is the MTU, in bytes, the interface must be configured with. It is
resolved from the network, so a provider never has to read the network to
configure the NIC.<br/>
          <br/>
            <i>Format</i>: int32<br/>
            <i>Minimum</i>: 1300<br/>
            <i>Maximum</i>: 8856<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>reclaimPolicy</b></td>
        <td>enum</td>
        <td>
          reclaimPolicy decides what becomes of this interface, and its addresses,
when the claim holding it is deleted. It comes from the claim, and a claim
asking for a different policy cannot bind this interface.<br/>
          <br/>
            <i>Enum</i>: Delete, Retain<br/>
            <i>Default</i>: Delete<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkInterface.spec.network
<sup><sup>[↩ Parent](#networkinterfacespec)</sup></sup>



network is the network this interface belongs to, in the same namespace as
the interface. It comes from the claim and does not change.

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


### NetworkInterface.spec.addresses[index]
<sup><sup>[↩ Parent](#networkinterfacespec)</sup></sup>



NetworkInterfaceAddress is an address the interface holds inside its network.
These are the addresses configured on the NIC itself, and they always carry a
prefix length.

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
          address is the address the interface holds, in CIDR notation, such as
10.128.0.2/32 or 2001:db8:a001::1/128.

For IPv6 this may be a block delegated to the interface rather than a
single address, such as 2001:db8:a001::/96. The interface owns the whole
block and assigns within it.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>family</b></td>
        <td>enum</td>
        <td>
          family is the address family of this entry.<br/>
          <br/>
            <i>Enum</i>: IPv4, IPv6<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>class</b></td>
        <td>string</td>
        <td>
          class is the IPAM class this address was allocated from, such as
private-ipv6. It is empty for the addresses a claim requests by family
rather than by class.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>gateway</b></td>
        <td>string</td>
        <td>
          gateway is the next hop the interface routes through for this family, such
as 10.128.0.1. It is resolved from the subnet backing the network in this
location, so nothing has to read the subnet to configure the NIC. It is
empty until that subnet exists.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>primary</b></td>
        <td>boolean</td>
        <td>
          primary marks the address projected into single-address fields, such as an
instance's reported network IP.

Exactly one address is primary for the interface as a whole, not one per
family. It is the address of the first family the claim listed in
spec.ipFamilies.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkInterface.spec.claimRef
<sup><sup>[↩ Parent](#networkinterfacespec)</sup></sup>



claimRef is the claim currently holding this interface. It is empty while a
retained interface waits, unbound, for a claim of its name to return.

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
          name is the name of the NetworkInterfaceClaim, in the same namespace as the
interface. A claim name stays with the workload slot it serves, so a
replacement instance binds this same interface and its addresses.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkInterface.spec.externalAddresses[index]
<sup><sup>[↩ Parent](#networkinterfacespec)</sup></sup>



NetworkInterfaceExternalAddress is an address reachable from outside the
network, mapped onto an address the interface holds inside it. A public IPv4
address in front of a private address is the usual case.

Unlike an interface address, an external address is a bare address with no
prefix length, such as 203.0.113.10, because nothing configures it on the
NIC. The data plane maps it onto the interface address of the same family.

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
          address is the externally reachable address, such as 203.0.113.10. It
carries no prefix length.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>class</b></td>
        <td>string</td>
        <td>
          class is the IPAM class this address was allocated from, such as
public-ipv4. It matches the class the claim requested in spec.addresses.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>family</b></td>
        <td>enum</td>
        <td>
          family is the address family of this entry.<br/>
          <br/>
            <i>Enum</i>: IPv4, IPv6<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkInterface.status
<sup><sup>[↩ Parent](#networkinterface)</sup></sup>



NetworkInterfaceStatus defines the observed state of NetworkInterface: which
claim holds it, what realizes it on the data plane, and whether programming
has succeeded.

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
        <td><b><a href="#networkinterfacestatusattachmentref">attachmentRef</a></b></td>
        <td>object</td>
        <td>
          attachmentRef is the data-plane resource realizing this interface. The
provider sets it once an attachment exists.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkinterfacestatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          conditions report the current state of the interface. Allocated means every
address is held. Prepared means the data plane is ready for a workload to
consume it. Programmed means the data plane carries the addresses.
HolderAvailable means whatever holds the interface reports itself available
to serve, and it is the only one of the four a service reads.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkinterfacestatusnetworkcontextref">networkContextRef</a></b></td>
        <td>object</td>
        <td>
          networkContextRef is the network's presence in this location, resolved or
created while fulfilling the claim. It is a breadcrumb for operators
tracing where a network landed, and nothing needs it to configure a NIC.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>phase</b></td>
        <td>enum</td>
        <td>
          phase reports whether a claim holds the interface. Bound means the claim in
spec.claimRef holds it. Available means it is retained and holding its
addresses with no claim bound.<br/>
          <br/>
            <i>Enum</i>: Available, Bound<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>vpc</b></td>
        <td>string</td>
        <td>
          vpc is the base62 identifier of the VPC backing this network in this
location, matching the identifier the fabric keys on. The provider records
it when the attachment is programmed.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkInterface.status.attachmentRef
<sup><sup>[↩ Parent](#networkinterfacestatus)</sup></sup>



attachmentRef is the data-plane resource realizing this interface. The
provider sets it once an attachment exists.

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
        <td><b>apiGroup</b></td>
        <td>string</td>
        <td>
          apiGroup is the API group of the referent, such as
compute.datumapis.com.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>kind</b></td>
        <td>string</td>
        <td>
          kind is the kind of the referent.<br/>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b>name</b></td>
        <td>string</td>
        <td>
          name is the name of the referent.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkInterface.status.conditions[index]
<sup><sup>[↩ Parent](#networkinterfacestatus)</sup></sup>



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


### NetworkInterface.status.networkContextRef
<sup><sup>[↩ Parent](#networkinterfacestatus)</sup></sup>



networkContextRef is the network's presence in this location, resolved or
created while fulfilling the claim. It is a breadcrumb for operators
tracing where a network landed, and nothing needs it to configure a NIC.

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
          The network context name<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>
