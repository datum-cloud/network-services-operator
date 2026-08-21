# API Reference

Packages:

- [networking.datumapis.com/v1alpha](#networkingdatumapiscomv1alpha)

# networking.datumapis.com/v1alpha

Resource Types:

- [NetworkInterfaceClaim](#networkinterfaceclaim)




## NetworkInterfaceClaim
<sup><sup>[↩ Parent](#networkingdatumapiscomv1alpha )</sup></sup>






NetworkInterfaceClaim asks for an interface on a network. It is the resource
a user creates. The operator finds or creates a NetworkInterface that
satisfies it, allocates the addresses, and reports them in status.

A claim describes what the interface must be able to do, never which
interface or address to use. One claim holds at most one interface, and one
interface is held by at most one claim.

A claim's name is what makes addresses stable. It names the slot in a
workload rather than the instance filling it, so an instance replaced by
another that asks for the same claim name comes back on the same interface
and the same addresses. What happens when the claim itself is deleted is
spec.reclaimPolicy.

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
      <td>NetworkInterfaceClaim</td>
      <td>true</td>
      </tr>
      <tr>
      <td><b><a href="https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.27/#objectmeta-v1-meta">metadata</a></b></td>
      <td>object</td>
      <td>Refer to the Kubernetes API documentation for the fields of the `metadata` field.</td>
      <td>true</td>
      </tr><tr>
        <td><b><a href="#networkinterfaceclaimspec">spec</a></b></td>
        <td>object</td>
        <td>
          NetworkInterfaceClaimSpec defines the desired state of NetworkInterfaceClaim.
Every field states what the interface must be able to do, never which
interface or which address to use.

Most of the spec is immutable, because the addresses are allocated against
it. To change one of those fields, delete the claim and create a new one,
accepting that the workload gets new addresses unless the interface is
retained.<br/>
          <br/>
            <i>Validations</i>:<li>has(self.networkInterfaceName) == has(oldSelf.networkInterfaceName) && (!has(self.networkInterfaceName) || self.networkInterfaceName == oldSelf.networkInterfaceName): networkInterfaceName is immutable and cannot be set, changed, or cleared after creation</li><li>has(self.addresses) == has(oldSelf.addresses) && (!has(self.addresses) || self.addresses == oldSelf.addresses): addresses is immutable and cannot be set, changed, or cleared after creation</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkinterfaceclaimstatus">status</a></b></td>
        <td>object</td>
        <td>
          NetworkInterfaceClaimStatus defines the observed state of
NetworkInterfaceClaim. It repeats the bound interface's addresses so a
consumer reads one object rather than following the reference.<br/>
          <br/>
            <i>Default</i>: map[conditions:[map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:Bound] map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:Allocated] map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:Prepared] map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:Programmed] map[lastTransitionTime:1970-01-01T00:00:00Z message:Waiting for controller reason:Pending status:Unknown type:Ready]]]<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkInterfaceClaim.spec
<sup><sup>[↩ Parent](#networkinterfaceclaim)</sup></sup>



NetworkInterfaceClaimSpec defines the desired state of NetworkInterfaceClaim.
Every field states what the interface must be able to do, never which
interface or which address to use.

Most of the spec is immutable, because the addresses are allocated against
it. To change one of those fields, delete the claim and create a new one,
accepting that the workload gets new addresses unless the interface is
retained.

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
        <td><b><a href="#networkinterfaceclaimspecnetwork">network</a></b></td>
        <td>object</td>
        <td>
          network is the network the interface attaches to. The network must already
exist in the same namespace as the claim.

Immutable. An interface that changed network would hold addresses from a
space it no longer belongs to, so move a workload by recreating the claim
against the other network.<br/>
          <br/>
            <i>Validations</i>:<li>self == oldSelf: network is immutable and cannot be changed after creation</li>
        </td>
        <td>true</td>
      </tr><tr>
        <td><b><a href="#networkinterfaceclaimspecaddressesindex">addresses</a></b></td>
        <td>[]object</td>
        <td>
          addresses request extra addresses by class, beyond the ones the interface
holds inside its network. Each appears in status.externalAddresses as a
bare address, mapped onto the interface address of the same family.

Omit this field for ordinary private addressing, which is the common case.<br/>
          <br/>
            <i>Validations</i>:<li>self.all(a, self.exists_one(b, b.class == a.class)): Each address class may be requested at most once</li>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>attachmentMode</b></td>
        <td>enum</td>
        <td>
          attachmentMode is how the guest consumes this interface. Netns places it in
the workload's network namespace, which is what an ordinary container
expects. Hypervisor hands it to a hypervisor as a device, which is what a
virtual machine or microVM guest needs.

It is copied to the bound interface and never interpreted here. Whoever
realizes the interface decides what each mode means on its data plane.

Immutable, because the guest and the attachment are both built against it.<br/>
          <br/>
            <i>Validations</i>:<li>self == oldSelf: attachmentMode is immutable and cannot be changed after creation</li>
            <i>Enum</i>: Netns, Hypervisor<br/>
            <i>Default</i>: Netns<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>interfaceName</b></td>
        <td>string</td>
        <td>
          interfaceName is the device name the interface presents to the guest
operating system, such as eth0 or eth1. Set it when a workload has more
than one interface and the guest configuration names them.

Immutable, because the guest is configured against it.<br/>
          <br/>
            <i>Validations</i>:<li>self == oldSelf: interfaceName is immutable and cannot be changed after creation</li>
            <i>Default</i>: eth0<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>ipFamilies</b></td>
        <td>[]enum</td>
        <td>
          ipFamilies are the address families the interface must carry, in priority
order. List [IPv6, IPv4] for a dual-stack interface. The first family
listed holds the interface's primary address, which is the one reported in
single-address fields such as an instance's network IP.

Every family listed must be satisfiable or the claim does not bind. Asking
for a family the network does not carry fails the claim outright rather
than leaving it pending, and no partially addressed interface is ever
published.<br/>
          <br/>
            <i>Validations</i>:<li>self.all(f, self.exists_one(g, g == f)): Each address family may be requested at most once</li><li>self == oldSelf: ipFamilies is immutable and cannot be changed after creation</li>
            <i>Enum</i>: IPv4, IPv6<br/>
            <i>Default</i>: [IPv6]<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>networkInterfaceName</b></td>
        <td>string</td>
        <td>
          networkInterfaceName binds one specific interface by name, instead of the
interface named after this claim. The named interface must already carry
every family and class this claim asks for, under the same reclaim policy,
and must not be held by another claim.

Leave it empty, which is the normal case. The claim then binds the
interface of its own name, retained by an earlier claim, or creates one.

Immutable, including from empty to set. Rebinding a workload to a different
interface means a new claim.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b>reclaimPolicy</b></td>
        <td>enum</td>
        <td>
          reclaimPolicy decides what becomes of the bound interface, and its
addresses, when this claim is deleted.

Delete deletes the interface and returns its addresses to IPAM. A workload
recreated later comes back on different addresses.

Retain keeps the interface, unbound and still holding its addresses, so a
later claim of this name binds it again and the workload returns to the
same addresses. Choose Retain when an address is published in DNS, allowed
through a firewall, or otherwise depended on from outside.

A retained address is reserved, and billable, for as long as the interface
exists. Deleting the interface does not return it to the pool today, so
choose Retain for addresses worth holding rather than as a default.

Both policies keep the addresses while the claim exists, including across
instance replacement. They differ only on scale-down and deletion.

Immutable. An address keeps the policy it was allocated under, and a claim
asking for a policy the interface was not allocated under cannot bind it.<br/>
          <br/>
            <i>Validations</i>:<li>self == oldSelf: reclaimPolicy is immutable and cannot be changed after creation</li>
            <i>Enum</i>: Delete, Retain<br/>
            <i>Default</i>: Delete<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkInterfaceClaim.spec.network
<sup><sup>[↩ Parent](#networkinterfaceclaimspec)</sup></sup>



network is the network the interface attaches to. The network must already
exist in the same namespace as the claim.

Immutable. An interface that changed network would hold addresses from a
space it no longer belongs to, so move a workload by recreating the claim
against the other network.

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


### NetworkInterfaceClaim.spec.addresses[index]
<sup><sup>[↩ Parent](#networkinterfaceclaimspec)</sup></sup>



NetworkInterfaceAddressRequest asks for one address beyond the ones the
interface holds inside its network, such as a public IPv4 address in front of
a private one.

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
        <td><b>class</b></td>
        <td>string</td>
        <td>
          class is the IPAM class to allocate from, such as public-ipv4.

A class names a kind of address, and the platform decides which pool and
prefix length serve it. A class never names a pool, a prefix length, or a
CIDR, so a class cannot be used to ask for a particular address.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>


### NetworkInterfaceClaim.status
<sup><sup>[↩ Parent](#networkinterfaceclaim)</sup></sup>



NetworkInterfaceClaimStatus defines the observed state of
NetworkInterfaceClaim. It repeats the bound interface's addresses so a
consumer reads one object rather than following the reference.

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
        <td><b><a href="#networkinterfaceclaimstatusaddressesindex">addresses</a></b></td>
        <td>[]object</td>
        <td>
          addresses are the addresses the bound interface holds inside its network,
each with its prefix length and, once the location has a subnet, its
gateway. They are copied from the interface, which remains the source of
truth.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkinterfaceclaimstatusconditionsindex">conditions</a></b></td>
        <td>[]object</td>
        <td>
          conditions report the current state of the claim. Wait on Ready, which is
true once the claim is bound, its addresses are allocated, the data plane
is prepared for a workload, and the data plane carries the addresses.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkinterfaceclaimstatusexternaladdressesindex">externalAddresses</a></b></td>
        <td>[]object</td>
        <td>
          externalAddresses are the addresses the bound interface is reachable at from
outside the network, one per class the claim requested. Each is a bare
address with no prefix length. They are copied from the interface.<br/>
        </td>
        <td>false</td>
      </tr><tr>
        <td><b><a href="#networkinterfaceclaimstatusnetworkinterfaceref">networkInterfaceRef</a></b></td>
        <td>object</td>
        <td>
          networkInterfaceRef is the interface bound to this claim, in the same
namespace. Read it to reach fields the claim does not repeat, such as the
MTU and the data-plane attachment.<br/>
        </td>
        <td>false</td>
      </tr></tbody>
</table>


### NetworkInterfaceClaim.status.addresses[index]
<sup><sup>[↩ Parent](#networkinterfaceclaimstatus)</sup></sup>



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


### NetworkInterfaceClaim.status.conditions[index]
<sup><sup>[↩ Parent](#networkinterfaceclaimstatus)</sup></sup>



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


### NetworkInterfaceClaim.status.externalAddresses[index]
<sup><sup>[↩ Parent](#networkinterfaceclaimstatus)</sup></sup>



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


### NetworkInterfaceClaim.status.networkInterfaceRef
<sup><sup>[↩ Parent](#networkinterfaceclaimstatus)</sup></sup>



networkInterfaceRef is the interface bound to this claim, in the same
namespace. Read it to reach fields the claim does not repeat, such as the
MTU and the data-plane attachment.

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
          name is the network interface name.<br/>
        </td>
        <td>true</td>
      </tr></tbody>
</table>
