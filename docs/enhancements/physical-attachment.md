---
status: provisional
stage: alpha
latest-milestone: "v0.x"
---

# Attaching a physical server to a network

- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-goals](#non-goals)
- [What exists today and what this proposes](#what-exists-today-and-what-this-proposes)
- [Proposal](#proposal)
  - [What it feels like](#what-it-feels-like)
  - [User stories](#user-stories)
- [Design details](#design-details)
  - [The Physical attachment mode](#the-physical-attachment-mode)
  - [The attachment point](#the-attachment-point)
  - [Boot claims and network claims](#boot-claims-and-network-claims)
  - [What a port carries](#what-a-port-carries)
  - [A private segment for one server](#a-private-segment-for-one-server)
  - [Where a physical claim lives](#where-a-physical-claim-lives)
  - [Who may write a physical claim](#who-may-write-a-physical-claim)
  - [The switch port hold](#the-switch-port-hold)
  - [Binding a physical claim, in order](#binding-a-physical-claim-in-order)
  - [Releasing a physical claim, and proving it](#releasing-a-physical-claim-and-proving-it)
  - [Installing a server and handing it to a customer](#installing-a-server-and-handing-it-to-a-customer)
  - [The boot network](#the-boot-network)
  - [The physical realizer](#the-physical-realizer)
  - [Conditions a physical claim reports](#conditions-a-physical-claim-reports)
- [How this lines up with Fleet's proposal](#how-this-lines-up-with-fleets-proposal)
- [Failure and recovery](#failure-and-recovery)
- [Delivery](#delivery)
- [Decisions for a person](#decisions-for-a-person)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
- [Open questions](#open-questions)
- [References](#references)

## Summary

A bare-metal server has no node agent, no container and no hypervisor. Its NIC
reaches a network through the switch port it is cabled to. NSO can attach a
container or a virtual machine to a network today, but it has no way to attach a
physical NIC.

This proposes a `Physical` attachment mode for the `NetworkInterfaceClaim`. A
physical claim names the NIC and the switch port it is cabled to. NSO assigns
its addresses through IPAM as it does for every claim, and a physical realizer
sets which networks the switch port carries.

The same mode serves two kinds of network:

- **Customer networks.** A server handed to a customer joins the networks the
  customer asked for.
- **The boot network.** A provider-owned network that servers join while they
  are installed or wiped. A claim for it is a boot claim.

A port's networks follow from the claims that name it. While a boot claim names
the port, the port carries the boot network and nothing else. Otherwise it
carries the networks of one project's claims, and with no claims it carries no
network.

Four rules keep a switch port safe:

- Only the provider can write a physical claim.
- A port carries one project's networks at a time, and the realizer is the only
  writer of what it carries.
- A port leaves a network before NSO frees the addresses it carried there.
- Under the `Delete` reclaim policy, NSO deletes a physical claim only once IPAM
  holds none of its addresses.

Everything in this document is proposed. Nothing in it is implemented yet. The
[next section](#what-exists-today-and-what-this-proposes) separates the two.

This document is written for Fleet, which manages physical servers and writes
physical claims, for the Networking team, which owns the switches, and for
NSO, which fulfills the claims.

## Motivation

Fleet allocates physical servers to customers. Its
[server networking proposal](https://github.com/datum-cloud/fleet/pull/186) and
its [Machine attachment work](https://github.com/datum-cloud/fleet/pull/198)
call NSO for one interface per Machine interface, and both stop at the same
gap. NSO's attachment modes all describe a NIC handed to a workload on a node,
and a physical server has no node.

Fleet asks NSO for:

1. A `Physical` attachment mode.
2. An attachment point on the claim: the NIC's MAC address and the switch and
   port it is cabled to, which Fleet reads from its Device inventory.
3. A provider-owned boot network, with isolation between the servers on it.
4. A port's networks derived from the claims that name it, with a boot claim
   taking the port while it exists.
5. Claims that only the provider can place, never the customer.
6. Detach before delete.
7. Boot addresses served by NSO on the boot network.

[How this lines up with Fleet's proposal](#how-this-lines-up-with-fleets-proposal)
maps each ask to this design, and says where the two differ.

### Goals

- Attach a physical server's NIC, through its switch port, to a customer
  network or to the boot network, with the same kind of claim.
- Let only the provider create a physical claim or name a switch port.
- Make NSO's realizer the one writer of a port's networks, including its
  untagged VLAN and its isolation on the boot network.
- Move a port from the boot network to a customer's networks in one change on
  the port.
- Optionally give a server its own private segment of a network, so it reaches
  the network's gateway and no other server.
- Assign every address, boot or customer, through IPAM, through NSO.
- Give a claim a lifetime its writer controls. Under `Delete`, delete a physical
  claim only once both its IPAM claims and their IPAM allocations are gone.

### Non-goals

- **Configuring the server's operating system.** NSO reports addresses,
  gateway, MTU and the VLAN a network arrives on. Fleet delivers them to the
  server.
- **Serving DHCP or network boot.** NSO assigns the boot address. DHCP, PXE,
  iPXE and the installer stay Fleet's, as its proposal describes.
- **Choosing a segment identifier.** Which VLAN or other switch-level segment
  carries a network at a site is the realizer's business.
- **A switch port's base profile.** Speed, bonding, bond fallback, edge mode and
  LLDP stay with network operations, as does DHCP snooping on the provisioning
  VLAN.
- **Changing a running server's networks.** As in Fleet's proposal, a server's
  customer interfaces are fixed for the life of its allocation.
- **Changing the other modes.** `Netns`, `Hypervisor` and `HypervisorDeclared`
  keep their current behavior, including how their claims are deleted.

## What exists today and what this proposes

| Piece | Today | Proposed here |
|---|---|---|
| `NetworkInterfaceClaim` and `NetworkInterface` | Implemented. Compute writes claims in a location's cell. NSO allocates through IPAM and binds an interface. | Unchanged for existing modes. |
| Attachment modes | `Netns`, `Hypervisor`, `HypervisorDeclared` | Adds `Physical`. |
| Attachment point | None | NIC MAC address, switch, and port, on the claim and its interface. |
| Boot claims | None | `purpose: Boot` on a physical claim. A boot claim takes its port while it exists. |
| Private segment | None | An optional `isolation: Private` on a physical claim. |
| Who may create a claim | Anyone who can write claims in the cell | Only configured provider identities may write a physical claim. |
| Switch port ownership | None | A `SwitchPortHold` per port records the boot claim and the one project whose claims hold the port. |
| Detach before release | None | A physical claim keeps its addresses until the realizer reports the port no longer carries its network. |
| Deleting a claim | NSO finds each IPAM claim, deletes it, and removes its own finalizer. It does not read the IPAM claim again after the delete. | For physical claims under `Delete`, NSO removes its finalizer only once a read finds each IPAM claim gone and no IPAM allocation naming it. Other modes are unchanged. |
| Copy of the interface in the project | An allow-list of fields. It omits `attachmentMode`. | Stays an allow-list. The attachment point, purpose and isolation stay off it. |
| Physical realizer | None | A contract the realizer follows, and a test realizer NSO ships with no real switch behind it. |

## Proposal

### What it feels like

Fleet allocates a server to a customer whose project has a network named `app`.
It writes one claim in the customer project's namespace, in the cell that
serves the server's location:

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: NetworkInterfaceClaim
metadata:
  # Fleet derives the name from a hash of the allocation and the interface,
  # so a Fleet restart finds the same claim.
  name: fleet-5d2c0e7a91b4f3c86e0d1a2b3c4d5e6f7a8b9c0d
  labels:
    fleet.datumapis.com/allocation: alloc-3b9d61c2
spec:
  network:
    name: app
  interfaceName: data
  attachmentMode: Physical
  attachmentPoint:
    macAddress: "00:00:5e:00:53:01"
    switch: rack-a1-tor-1
    port: Ethernet1/12
  ipFamilies:
    - IPv4
  reclaimPolicy: Delete
```

NSO binds it as it binds any claim, and the claim reports the answer. While the
server is still being installed on the boot network, the claim is bound and
allocated but not yet programmed:

```yaml
status:
  networkInterfaceRef:
    name: fleet-5d2c0e7a91b4f3c86e0d1a2b3c4d5e6f7a8b9c0d
  addresses:
    - family: IPv4
      address: 192.0.2.10/24
      gateway: 192.0.2.1
      primary: true
  conditions:
    - type: Bound
      status: "True"
    - type: Allocated
      status: "True"
    - type: Prepared
      status: "True"
    - type: Programmed
      status: "False"
      reason: BootAttachmentActive
    - type: Ready
      status: "False"
```

Once the install ends and its boot claim is released, the realizer moves the
port onto `app`, and `Programmed` and `Ready` turn true.

The bound `NetworkInterface` carries the same attachment point, so the realizer
reads one object to know which port to set. A customer reading their interfaces
through their project sees addresses, gateway and MTU, but never the switch or
the port. NSO publishes that copy from an allow-list of fields, and this design
leaves the attachment point, purpose and isolation off the list.

The install's boot claim lives in the provider project's namespace and names
the provider's boot network:

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: NetworkInterfaceClaim
metadata:
  # Named from the install operation, so the claim lives exactly as long as
  # the operation needs it.
  name: alloc-3b9d61c2-install-1-boot
spec:
  network:
    name: boot
  interfaceName: boot
  attachmentMode: Physical
  purpose: Boot
  attachmentPoint:
    macAddress: "00:00:5e:00:53:01"
    switch: rack-a1-tor-1
    port: Ethernet1/12
  ipFamilies:
    - IPv4
  reclaimPolicy: Delete
```

It leaves `isolation` unset, so the port joins a private segment of the boot
network. Its status carries the boot address, and `Programmed` turns true once
the port carries the boot network untagged.

### User stories

- **A server is installed and handed over.** Fleet writes the install's boot
  claim and the customer's claims. The port joins the boot network, and the
  customer's claims get their addresses so the installer can configure them.
  When Fleet deletes the boot claim, the realizer moves the port to the
  customer's networks in one change.
- **A customer's server joins two networks.** Fleet writes two claims. Both
  report `Ready` once the realizer sets the ports.
- **A customer cannot attach a port.** A customer, or compute on a customer's
  behalf, writes a claim with `attachmentMode: Physical`. Admission refuses it,
  and nothing is allocated.
- **A wipe is isolated.** A server being wiped joins the boot network on a
  private segment and can reach the boot network's gateway, but no other server
  on it.
- **A boot address lives as long as one install.** Fleet creates the boot claim
  before the install and deletes it after. Fleet treats the address as released
  once the claim is gone, and by then IPAM holds nothing for it.
- **A switch is unreachable.** The realizer cannot change a port. The claim
  stays, its addresses stay held, and the server stays out of use until the
  port changes or an operator steps in.

## Design details

### The Physical attachment mode

`attachmentMode` already says how the guest consumes an interface. `Physical`
means the interface is a physical NIC that reaches its network through a switch
port. There is no node, no container and no hypervisor, and nothing on the host
configures it on the platform's behalf.

As with the other modes, NSO copies `attachmentMode` to the interface and
never interprets it to allocate addresses. A physical claim is addressed
exactly like any other. The mode changes four things NSO does:

- NSO requires an attachment point, and lets only provider identities write the
  claim.
- NSO records the claim on the port's hold, which decides what the port carries.
- NSO waits for the realizer to take the claim's network off the port before it
  releases the claim's addresses.
- Under `Delete`, NSO deletes the claim only once IPAM confirms its addresses
  are released.

The mode stays immutable, like every other field the interface is built on.

### The attachment point

```yaml
spec:
  attachmentPoint:
    # The NIC's MAC address, from the provider's inventory. Six colon-separated
    # hexadecimal pairs.
    macAddress: "00:00:5e:00:53:01"
    # The switch and port the NIC is cabled to, in the names network operations
    # uses for them. Either may contain characters a Kubernetes name cannot,
    # such as the slash in Ethernet1/12.
    switch: rack-a1-tor-1
    port: Ethernet1/12
```

- A physical claim requires all three fields. A claim in any other mode may not
  carry an attachment point.
- The attachment point is immutable. It cannot be set, changed or cleared after
  the claim is created. A server recabled to another port gets a new claim.
- NSO copies it verbatim to the bound interface. NSO does not check it against
  any inventory. The provider identity that wrote the claim is trusted for it,
  which is why only provider identities may write one.
- The interface copy NSO publishes to the project never carries it.

The new fields have no server-side defaults. A writer that compares the claim it
reads back with the claim it wrote, as Fleet does, sees no field it did not set.

The attachment point is read only from these typed fields. NSO ignores any
annotation that names a MAC address, switch or port.

### Boot claims and network claims

A physical claim is one of two kinds:

- A **boot claim** sets `purpose: Boot`. It attaches the port to a boot network
  for as long as the claim exists, ahead of any other claim on the port.
- A **network claim** leaves `purpose` unset. It attaches the port to a network
  whenever no boot claim names the port.

`purpose` is immutable and only valid on a physical claim. Only provider
identities may set it, like everything else on a physical claim.

A boot claim is accepted only in a provider project's namespace. Each cell's
configuration lists the provider projects by project name, and admission
refuses `purpose: Boot` in a namespace whose project is not on the list.

Admission reads the project from the namespace's
`meta.datumapis.com/upstream-cluster-name` label, the label NSO already reads
to find a namespace's project before it allocates addresses. The label holds an
encoded value, not a bare project name, so admission decodes it the way NSO
does: it turns each underscore into a slash, then drops the `cluster-` prefix
and any leading slash. It compares the decoded name with the list, never the
raw label. A namespace without the label names no project and is refused.

A customer's network can therefore never outrank another claim on a port,
whoever writes the claim.

NSO does not mark which networks are boot networks. A boot claim may name any
network in its own namespace, and the provider's boot network is the one it
names in practice. That keeps the boot network an ordinary NSO network.

### What a port carries

The realizer works out what each port carries from the claims recorded on the
port's hold, counting only claims with no deletion timestamp:

1. While a boot claim is recorded, the port carries that boot claim's network,
   untagged, and nothing else.
2. Otherwise, the port carries the networks of the network claims recorded on
   the hold. They all belong to one project.
3. With neither, the port carries no network.

A port moves between these states in one change. Handing a server to a customer
is the change from 1 to 2. A reinstall on a NIC that also carries customer
networks is the change from 2 to 1 and back.

A network claim that is bound while a boot claim holds its port keeps its
addresses. It reports `Programmed=False` with reason `BootAttachmentActive`,
because the port does not carry its network yet. Nothing is released.

On a port that carries more than one network, all but one arrive tagged, and
the server's operating system must know each tag. The realizer reports the VLAN
each network arrives on, on the interface, before it reports `Prepared`. A
network that arrives untagged reports no VLAN.

### A private segment for one server

```yaml
spec:
  isolation: Private
```

`isolation` is optional, immutable, and only valid on a physical claim. It
takes `Private` or `Shared`. Left unset, it follows the claim's purpose:

- A boot claim is private, so servers on the boot network never reach each
  other unless the provider writes `isolation: Shared`.
- A network claim is shared, so the server reaches every other member of the
  network unless the provider writes `isolation: Private`.

The realizer applies that rule. NSO stores no default on the claim, so a writer
comparing its claim with what it reads back sees no field it did not set.

`Private` asks the realizer to put the port in a segment of its own on the
network. The server reaches the network's gateway and nothing else on the
network. Its address still comes from the network's subnet, so the boot network
keeps one address space whether its members are isolated or not.

How the realizer builds a private segment is its own choice. A private VLAN with
isolated ports, a VLAN per port, or a filter on the port all satisfy the
contract. A realizer that cannot build one reports the claim `Prepared=False`
with a reason, and never attaches the port to the shared segment in its place.

Fleet's proposal puts every server on the boot network in isolation, and a
boot claim that leaves `isolation` unset gets exactly that. A site that wants a
shared boot segment writes `isolation: Shared`.

### Where a physical claim lives

A physical claim lives where compute's claims live today: in the cell that
serves the server's location, in the namespace of the project whose network it
names.

| Claim | Namespace |
|---|---|
| A network claim for a customer's network | The customer project's namespace in the cell |
| A boot claim | The provider project's namespace in the cell |

Customers cannot write in a cell at all, which is the first half of keeping
claim placement with the provider. The admission rule below is the second half,
and it holds even against an identity that can write in the cell.

The namespace is what ties a claim to a project. A claim always names a network
in its own namespace, so a provider writing in a customer's namespace can only
attach to that customer's networks, and a boot claim can only attach to the
provider's.

Fleet's [current adapter](https://github.com/datum-cloud/fleet/pull/198) uses
one client for the customer's Networks and for its claims, and writes claims
next to the Networks. Under this design Fleet would read Networks where it does
today and write claims in the cell, which takes a second client and write
access in each cell. That is listed under
[Decisions for a person](#decisions-for-a-person).

### Who may write a physical claim

An admission rule in each cell covers every create and update of a claim,
including patches. It refuses the request when the claim, before or after the
change, has `attachmentMode: Physical`, an `attachmentPoint`, a `purpose`, or an
`isolation`, unless the requester is one of a configured list of provider
identities. The list is cell configuration, and Fleet's identity is the
expected entry.

Compute, and anything else that writes claims today, is not on the list. A
claim compute writes in any other mode is admitted exactly as it is today.

The rule checks the authenticated requester, which is the only identity it can
trust. It does not record that identity on the claim.

A provider identity is still refused a claim with `purpose: Boot` in a
namespace whose project is not on the cell's list of provider projects, as
[Boot claims and network claims](#boot-claims-and-network-claims) describes.
Both lists are parameters of the admission rule, set per cell.

NSO's own identity is exempt, because NSO writes finalizers and status on every
claim. The realizer's identity may write status on a physical claim.

A second rule protects the detach step. Only three kinds of identity may remove
the detach finalizer described
[below](#releasing-a-physical-claim-and-proving-it):

- the realizer's configured identity;
- NSO's own identity; and
- a configured break-glass group of operators.

Admission cannot read the hold, so it only checks who is asking. NSO's own code
carries the narrower rule that it removes the finalizer only from a claim the
hold never recorded.

An operator in the break-glass group uses it only after confirming on the
switch that the port no longer carries the claim's network. NSO's runbook will
say how. This is NSO's proposed answer to Fleet's open question about a port NSO
cannot release. Who may be in the group is a decision for a person.

### The switch port hold

NSO records the claims that name each port in a `SwitchPortHold`: a
cluster-scoped object in the cell. NSO is its only writer, and the realizer
reads it.

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: SwitchPortHold
metadata:
  # A hash of the switch and port, because port names can hold characters a
  # Kubernetes name cannot, and joining the two strings could collide.
  name: port-9c4e1f0b7a2d5e8c3b6a9f0e1d2c3b4a5f6e7d8c
spec:
  # The switch and port, stored in full. A hold whose stored pair differs from
  # a claim's is a hash collision, and NSO refuses the claim.
  switch: rack-a1-tor-1
  port: Ethernet1/12
  # The one boot claim on the port, if there is one.
  boot:
    namespace: ns-provider-example
    name: alloc-3b9d61c2-install-1-boot
    network: boot
  # The project whose network claims hold the port, and those claims.
  consumerRef:
    apiGroup: resourcemanager.miloapis.com
    kind: Project
    name: customer-example
  networkClaims:
    - namespace: ns-customer-example
      name: fleet-5d2c0e7a91b4f3c86e0d1a2b3c4d5e6f7a8b9c0d
      network: app
```

NSO reads the project for `consumerRef` from the claim's namespace, the same way
it finds the project when it allocates addresses today. NSO sets `consumerRef`
when it records the first network claim, and clears it when it removes the last
one. A hold kept alive only by a boot claim names no project. The reference uses
Milo's project type, which NSO's Milo test environment already reads.

Every change to a hold is a compare-and-swap. NSO reads the hold from the API
server rather than from a cache, changes it, and writes it back. If another
writer changed the hold in between, the write fails and NSO reads it again.
That makes each of these exact under concurrency:

- **Taking a free port.** NSO creates the hold with the claim recorded. Only one
  create of a given name can succeed.
- **Recording a boot claim.** A boot claim is recorded when `boot` is empty. A
  second boot claim waits with `Bound=False` and reason `AttachmentPointHeld`
  until the first is released. A wipe's boot claim written while an install's is
  still releasing waits this way.
- **Recording a network claim.** A network claim is recorded when
  `networkClaims` is empty, or when it already belongs to the claim's project and
  does not already carry the claim's network. Otherwise the claim waits with
  `AttachmentPointHeld`. It allocates no address while it waits, and its message
  names nothing about any other project.
- **Recording again.** An entry is matched by the claim's namespace, name and
  network. An entry with the claim's name and another network is left from an
  earlier claim of that name, and NSO replaces it.
- **Leaving a port.** A releasing claim's entry is removed. When the hold has no
  boot claim and no network claims left, NSO deletes it, on the condition that
  nobody has changed it since NSO read it. A claim recorded in between makes the
  delete fail, and the hold stays.

A port carries a network at most once, and only one project's network claims at
a time. Two projects' network claims can never be recorded on one hold at once.

NSO does not order waiting claims. Once the holding project's last network claim
leaves, the next claim to be recorded wins, whichever project it belongs to. The
provider decides who should hold a server, and Fleet allocates a server to one
customer at a time, so a race between projects means a provider error rather
than normal traffic.

A holder that never becomes ready keeps the hold. A waiting claim's reason does
not say who holds the port, because the waiting claim may belong to another
customer. The provider can read the hold, in the cell, to see which claims hold
it.

### Binding a physical claim, in order

NSO binds a physical claim in these steps. Each one starts only after the
previous one is written:

1. NSO adds its own finalizer and the detach finalizer in one update. A claim
   deleted before this update completes has neither, takes nothing, and is
   deleted at once.
2. NSO records the claim on the port's hold. From here on the realizer is
   responsible for taking the claim's network off the port.
3. NSO allocates the addresses through IPAM.
4. NSO creates or binds the interface, copying the attachment point, purpose and
   isolation.
5. NSO reports the claim `Bound` and `Allocated`.

Kubernetes refuses a new finalizer on an object that is being deleted, so step 1
can never add the detach finalizer to a claim that is already releasing. NSO
reconciles one claim at a time, and it never records a claim, or creates an
interface for it, once it sees a deletion timestamp on the claim.

The realizer acts only on claims recorded on a hold that carry the detach
finalizer and have no deletion timestamp. Recording comes before any interface
exists, so whether the claim was ever recorded on its hold answers whether the
realizer may have touched the port. Whether an interface exists does not.

### Releasing a physical claim, and proving it

A provider that deletes a claim needs to know when the addresses are free and
the port no longer carries the claim's network. The claim's absence is that
signal. NSO keeps the claim until both are true.

When a physical claim is deleted:

1. NSO marks the claim releasing, so `Prepared` and `Ready` go false.
2. The detach finalizer comes off. Who removes it depends on whether the hold
   ever recorded the claim, read from the API server:
   - If the hold records the claim, the realizer takes the claim's network off
     the port, or confirms the port never carried it, and removes the finalizer.
   - If the hold does not record the claim, the realizer never acted for it, and
     NSO removes the finalizer itself. A claim that waited with
     `AttachmentPointHeld` is in this case.
3. NSO releases the addresses. Under `Delete` it deletes the interface and the
   IPAM claims behind it. Under `Retain` it unbinds the interface, which keeps
   its addresses and stays off the port until a claim of the same name binds it
   again.
4. Under `Delete`, NSO reads each IPAM claim again, and lists the IPAM
   allocations in the same IPAM namespace. The release is verified once each
   IPAM claim is gone and no allocation's `spec.claimRef` names one. Under
   `Retain`, IPAM keeps the allocations on purpose, so the release is verified
   once the interface is unbound.
5. NSO removes the claim's entry from the hold.
6. NSO removes its own finalizer, and the claim is gone.

Until each step completes, the claim stays and its conditions say which step it
is waiting on. A hold entry whose claim no longer exists, read from the API
server rather than a cache, is removed the next time NSO writes that hold, so
an entry cannot outlive its claim.

NSO never releases an address while the detach finalizer is present. A claim
whose realizer never answers therefore stays, with its addresses held, and that
is the intended failure. An address freed while a port might still carry it
could be handed to another server on the same network.

The claim's name stays taken until step 6. A create under the same name before
then is refused as already existing, and the writer retries once the old claim
is gone. Naming a boot claim from its operation means a new operation never
needs an old operation's name.

The claim's absence is the whole contract for a release. A provider confirms an
address is free by finding the claim gone, not by reading IPAM itself. The
names NSO gives its IPAM claims are internal. An address can already be held
under either of two names, one of them specific to the location, and NSO may
change how it names them. NSO checks every name an address can be held under
before it lets the claim go.

The verified release in step 4 applies to physical claims only. Today, for
every mode, NSO finds each IPAM claim, deletes it, and removes its own
finalizer without reading the IPAM claim again. Extending step 4 to compute's
claims would make every claim's deletion wait on IPAM, including during
namespace and project teardown. That choice is listed under
[Decisions for a person](#decisions-for-a-person).

### Installing a server and handing it to a customer

Handing a server over is one change on the port. Fleet writes the claims, and
NSO and the realizer do the rest:

1. Fleet writes the install's boot claim and the customer's network claims. The
   boot claim is recorded on the hold, and so are the network claims.
2. The realizer puts the port on the boot network, untagged, and reports the
   boot claim `Programmed`. The network claims are bound and allocated, and
   report `Programmed=False` with reason `BootAttachmentActive`.
3. Fleet installs the server, with the customer's addresses from the network
   claims in the host configuration and the boot address from the boot claim.
4. Fleet deletes the boot claim when its rules allow.
5. The realizer changes the port in one step, off the boot network and onto the
   customer's networks. It then removes the boot claim's detach finalizer and
   reports the network claims `Programmed`.
6. NSO releases and verifies the boot address, and the boot claim is gone.

At no point does the port carry the boot network and a customer's network
together.

The release at the end of an allocation runs the other way. Fleet deletes the
customer's network claims, and the realizer takes their networks off the port.
Fleet then writes the wipe's boot claim, and the port joins the boot network.
That matches Fleet's order of release before wipe, although the hold would keep
the port safe in either order.

Which ports change depends on the boot NIC:

- **Shared boot NIC.** One port carries the boot network during an install or a
  wipe, and the customer's networks otherwise. A reinstall moves that port off
  the customer's networks while its boot claim exists, and back afterwards.
- **Dedicated boot NIC.** The boot port only ever carries the boot network, and
  only while a boot claim names it. The customer's ports keep their networks
  through a reinstall, and handoff is their first attachment, not a move.

### The boot network

The boot network is an ordinary NSO `Network` in the provider project. It
reaches each location that has servers the way any network does. Its subnet in
a location provides the gateway servers boot through, and network operations
gives the realizer that site's provisioning VLAN as the boot network's segment
there.

- **Addresses.** A boot claim's address comes from IPAM through NSO, like any
  claim's. The provider chooses the boot network's address families. Firmware
  usually needs IPv4. Once bound, the claim reports the address in
  `status.addresses`, one entry per family, each with its `family`, `address`
  and `gateway`. A reader picks the entry by `family: IPv4`, not by its place
  in the list, because the list's order is not part of the contract.
- **Lifetime.** The provider names a boot claim from the operation it serves,
  creates it before the operation starts and deletes it when the operation ends.
  NSO adds no lifetime of its own.
- **Isolation.** A boot claim that leaves `isolation` unset keeps the server
  away from the others on the boot network. A provider that wants a shared boot
  segment writes `isolation: Shared`.
- **Serving firmware.** NSO hands out the address and the gateway. Fleet's DHCP
  and boot file servers answer firmware with them, from a central cluster
  rather than from the site.

#### Reaching the central DHCP and boot file servers

Fleet's DHCP and boot file servers run in a central cluster. Each site's router
relays firmware's DHCP requests to that cluster, and firmware then fetches its
boot file from it through the same router. NSO's part does not change: it
attaches the port to the boot network and assigns the address. Three things
around it must hold for a relayed boot to work:

- **The site router is the boot network's gateway.** The router relays DHCP
  from the gateway address of the boot network's subnet at that site, and the
  central server picks the site's subnet by that relay address. NSO reports as
  the gateway the address after the start address of the location's subnet, so
  the router must hold that address on the provisioning VLAN and relay from it.
- **Each site's boot subnet is distinct.** One central server serves every
  site, so two sites whose boot subnets overlap would look the same to it. The
  provider must give the boot network a subnet in each location that overlaps
  no other location's.
- **The boot network and the central cluster route to each other.** Firmware
  sends every request beyond its own subnet through the gateway, so the site
  router must route the boot network to the central DHCP and boot file servers
  and to nothing else. Replies travel the other way: a relayed DHCP reply goes
  back to the router's relay address, and boot file traffic back to the
  server's own address. The central cluster therefore needs a route back to
  every site's boot subnet.

A private segment still works. A server on a private segment reaches the
network's gateway, which is the router, and every relayed request and boot file
fetch goes through the router.

Routing and relays are network operations' configuration, not NSO's. Who keeps
the router's gateway address and the boot subnets in step with NSO's boot
network is listed under [Decisions for a person](#decisions-for-a-person).

A boot claim carries its address from the first delivery. NSO does not offer a
physical claim without an address, because every claim NSO binds allocates at
least one family. [How this lines up with Fleet's proposal](#how-this-lines-up-with-fleets-proposal)
explains why that differs from Fleet's plan.

### The physical realizer

A realizer is whatever turns an interface into a working attachment. For
physical claims, the realizer programs switch ports. NSO defines what the
realizer reads and writes, and ships a test realizer that follows the same
contract against a simulated switch.

The realizer:

- reads the holds in its cell, the claims they record, and the interfaces bound
  to them;
- counts a claim only while it is recorded on a hold, carries the detach
  finalizer, and has no deletion timestamp;
- works out what each port carries by the rules in
  [What a port carries](#what-a-port-carries);
- changes each port in one step, one port at a time, so it is the only writer of
  what the port carries, including its untagged VLAN and its isolation;
- writes `Prepared`, `Programmed`, and the VLAN each network arrives on, on the
  interface and on the claim, and `status.attachmentRef` if it has an object to
  name;
- removes the detach finalizer from a deleted claim once the port no longer
  carries that claim's network.

Three rules keep it safe:

- **Withdraw before changing a port.** Before it changes a port, the realizer
  writes `Programmed=False`, with reason `PortChanging`, on every claim whose
  network the change adds or removes. If that write fails, the realizer leaves
  the port alone and tries again. A claim never reports `Programmed=True` for a
  port that is between states.
- **Read the port back.** A change counts only once the realizer has read the
  port back from the switch and found what it asked for. A change that fails,
  or only partly applies, leaves the port's state unknown. The realizer keeps
  every detach finalizer on that port, leaves `Programmed` false, and tries
  again until the read-back matches.
- **Check after changing a port.** The realizer reads the claims again from the
  API server after it changes a port. If one has gained a deletion timestamp
  meanwhile, the realizer changes the port again before it removes that claim's
  detach finalizer.
- **Fail closed.** A realizer that cannot reach a switch changes nothing on the
  claims beyond the withdrawal above, and keeps every detach finalizer. It never
  removes one on a guess.

A boot claim recorded on a port whose network claims report `Programmed=True`
starts with that withdrawal. The network claims go false, the port moves to the
boot network, and only then does the boot claim go true.

How the realizer reaches a switch, and how a network maps to a segment at a
site, belong to the Networking team. The test realizer stands in for both in
NSO's own tests, and any other team may run it in theirs.

### Conditions a physical claim reports

A physical claim reports the same five conditions as any claim. Each has one
writer:

| Condition | Written by | Physical meaning |
|---|---|---|
| `Bound` | NSO | The hold records the claim and an interface is bound. |
| `Allocated` | NSO | Every requested family holds an address. |
| `Prepared` | Realizer, on the interface and the claim | The realizer accepted the attachment point and can build its segment. |
| `Programmed` | Realizer, on the interface and the claim | The port carries the claim's network. |
| `Ready` | NSO | All four are true. NSO derives it each time the claim changes. |

The existing realizer for virtual interfaces writes `Ready` on a claim only to
set it `Unknown` while it migrates an attachment. The physical realizer never
writes `Ready`, so `Ready` keeps one writer.

The [interface design](network-interfaces.md#how-the-guest-consumes-it-and-when-it-is-safe-to-start)
says nothing may gate creating a workload on `Programmed`. That rule exists
because a virtual interface is attached while its workload's sandbox is being
created, so a consumer that waits for `Programmed` first waits forever.

A physical port is attached with no workload at all. The realizer programs it
once the claim is recorded and nothing outranks it, whether the server is on or
off. So a provider may wait for a boot claim's `Programmed` before it boots a
server, and for its network claims' `Programmed` before it reports the server
ready, as Fleet's proposal does. The rule for the other modes stands.

## How this lines up with Fleet's proposal

This compares against fleet#186 at `d850b06a`, plus Fleet's later move of its
DHCP and boot file servers to a central cluster behind each site router's
relay, which its proposal does not yet describe. Fleet's side is a proposal,
not an agreed contract.

| Fleet expects | This design | Agreement |
|---|---|---|
| `Physical` mode, attachment point on the claim and interface | Same | Agrees |
| A provider-owned boot network, isolated between servers | An ordinary NSO network. A boot claim is private unless it says `isolation: Shared`. | Agrees |
| A separate boot claim per network-booting operation, made before the operation | A physical claim with `purpose: Boot`, named by Fleet from the operation, in a provider project's namespace | Agrees |
| NSO knows a boot claim from the boot network it names | NSO knows a boot claim from `purpose: Boot`, which Fleet must set | **Differs** |
| A port carries the boot network while a boot claim names it, otherwise its customer networks, otherwise nothing | [What a port carries](#what-a-port-carries) | Agrees |
| Handoff is one step after the boot claim's release | The realizer moves the port in one change once the boot claim is deleted | Agrees |
| NSO owns the untagged VLAN and boot isolation. Network operations keeps the base profile and DHCP snooping. | Same | Agrees |
| Boot claim released under the same verified rules as an address | Verified release applies to every physical claim | Agrees |
| DHCP and boot file servers in a central cluster, reached through each site router's relay | NSO attaches the port and assigns the address. The router is the boot network's gateway at each site, and each site's boot subnet is distinct. | Agrees. Keeping the router and subnets in step with NSO is a decision for a person. |
| Only Fleet's identity creates physical claims, where the customer cannot write | Provider-only admission, claims in the cell | Agrees. Where Fleet writes claims is a decision for a person. |
| At first, a boot claim with no address, and a separate IPAM claim made after `Programmed` | Every boot claim carries its address from the first delivery | **Differs** |
| The realizer checks each attachment point against Fleet's allocation | The provider identity is trusted, and nothing reads Fleet | **Differs** |
| Host configuration carries MAC, addresses, gateway and MTU | The realizer also reports the VLAN each network arrives on | **Differs.** Fleet's host configuration would need the VLAN. |

Where the design differs, NSO's model points somewhere better:

- **Knowing a boot claim.** See
  [the API shapes Fleet's boot attachment work assumes](#the-api-shapes-fleets-boot-attachment-work-assumes).
- **The boot address.** NSO allocates through IPAM for every claim, so a boot
  claim without an address would be a special case in NSO that Fleet would drop
  again. Both pieces of NSO land together, so there is no time when NSO attaches
  a port and cannot address it. Fleet could skip the separate IPAM claim and the
  later swap, and wait for the boot claim's `Allocated` and `Programmed`.
- **Checking the attachment point.** For the realizer to read Fleet's
  allocations, NSO would need a contract with Fleet's API, a second authority
  over the same port, and a way to fail when Fleet is unreachable. Provider-only
  admission puts the same trust in one place.
- **The VLAN a network arrives on.** A port carrying two customer networks
  delivers at least one of them tagged. Without the tag, the installer cannot
  configure that interface.

### The API shapes Fleet's boot attachment work assumes

Fleet's boot attachment work, built against a test realizer and off by
default, assumes six NSO API shapes. None of them is an agreed contract.

| Fleet assumes | This design | Verdict |
|---|---|---|
| 1. A `Physical` attachment mode for a bare-metal server | [The Physical attachment mode](#the-physical-attachment-mode) | Adopted as is |
| 2. The NIC's MAC address, switch and port on the claim, passed as annotations today | Typed, validated, immutable fields in `spec.attachmentPoint`. Annotations are ignored. | Adopted, with typed fields replacing the annotations |
| 3. A boot attachment claim on a provider-owned boot network, isolated between servers, that puts the port on the boot network while it exists and reports `Programmed` | A physical claim with `purpose: Boot`, private by default, accepted only in a provider project | Changed. NSO knows a boot claim from `purpose: Boot`, not from the network it names. |
| 4. The bound provisioning address in the claim's status, read as the first IPv4 entry | `status.addresses`, one entry per family | Changed. Read the entry whose `family` is IPv4, not the first entry. |
| 5. NSO keeps the claim until the port is detached, then releases the address, with a stable link from the claim to its IPAM claim so Fleet can confirm the address is free | [Releasing a physical claim, and proving it](#releasing-a-physical-claim-and-proving-it) | Release adopted. The IPAM link is rejected. The claim's absence is the confirmation. |
| 6. A port's membership follows its claims. A boot claim puts the port on the boot network only, with customer claims prepared but not programmed. Otherwise the port carries the customer networks. Each change is one step. | [What a port carries](#what-a-port-carries) and [the realizer's rules](#the-physical-realizer) | Adopted, with the realizer's safety rules added |

The reasons for each change and rejection:

- **Typed fields, not annotations.** A field can be validated, required for
  `Physical`, frozen once written and kept out of the customer's copy of the
  interface. An annotation can be none of those. Admission also limits who may
  write the typed fields, and it cannot do the same for an annotation key
  without matching every write to every claim.
- **Knowing a boot claim by its purpose.** Taking boot precedence from the
  network a claim names would need a way to mark a network as a boot network,
  and any project can write its own networks. `purpose: Boot` sits on a claim
  only providers can write, in a namespace only providers may use for it.
  Isolation follows the purpose, so Fleet gets an isolated boot claim without
  setting anything else.
- **Picking the address by family.** NSO publishes one address per family and
  promises nothing about their order. Reading the first entry works for a
  claim that asks only for IPv4 and breaks quietly the day a boot network also
  carries IPv6.
- **No IPAM link.** If Fleet also verified IPAM, two components would decide
  when an address is free, and a change in NSO's naming would break Fleet
  without a single NSO field changing. NSO already holds the claim until every
  IPAM claim it could have used is gone and no IPAM allocation names one, so
  the claim's absence already proves what the link was meant to prove. The
  link would also have to name a resource in IPAM's own API, which is a
  contract IPAM would have to agree to.
- **Safety rules on the handover.** Fleet's model of what a port carries is
  NSO's. The realizer adds the steps that keep it honest when a switch
  misbehaves: it withdraws `Programmed` before a change, reads the port back,
  and keeps every detach finalizer until the read-back matches.

## Failure and recovery

| Failure | What happens |
|---|---|
| NSO restarts while binding | Each step is written before the next starts, and the hold and IPAM claims are named from the port and the claim, so the next attempt finds them. |
| Two projects claim one port at once | Both write the hold by compare-and-swap. The first recorded wins, and the other claim waits with `AttachmentPointHeld`. |
| A project records a claim while its last claim on the port leaves | Either the record lands first and the hold stays, or the delete lands first and the new claim creates a new hold. |
| A claim is deleted before NSO adds its finalizers | The claim is deleted at once. Nothing was taken for it. |
| A claim waiting for a port is deleted | The hold never recorded it, so NSO removes the detach finalizer itself and the claim is deleted. |
| NSO crashes after recording a claim and before creating its interface | The hold records the claim, so on deletion the realizer, not NSO, removes the detach finalizer, after confirming the port does not carry the network. |
| A port change fails or only partly applies | Every claim the change touched already reports `Programmed=False`. The realizer keeps the port's detach finalizers and retries until it reads the intended state back from the switch. |
| The realizer restarts while changing a port | It recomputes what each port carries from the holds and claims, and changes the port again if a claim has a deletion timestamp. |
| The realizer is down when a claim is deleted | The claim stays with its addresses held and the detach finalizer present, until the realizer returns or a break-glass operator removes it. |
| NSO is down | No new claim binds and no claim releases. Ports keep what they carry. Fleet's proposal holds installs, wipes and releases until NSO returns. |
| The central DHCP or boot file servers are unreachable from a site | NSO is unaffected. A boot claim stays `Programmed`, since the port carries the boot network. In Fleet's proposal the server is powered on only after DHCP reports its reservation served, so the install waits. Releasing the boot claim works as usual. |
| IPAM is unreachable during release | The claim stays, reporting the failed release, and NSO retries. |
| IPAM keeps an allocation after its IPAM claim is gone | The claim stays, reporting the allocation that remains. |
| A claim is recreated under a name whose claim is still releasing | The create is refused as already existing. The writer retries after the old claim is gone. |

## Delivery

Each step is one draft PR, stacked on the one before it, and each ships with
tests for concurrency, authorization, lifecycle and recovery.

1. **API.** The `Physical` mode, `attachmentPoint`, `purpose` and `isolation` on
   the claim and the interface, with their validation and immutability, and a
   test that the project copy omits them.
2. **Admission.** The rule that limits physical claims to provider identities,
   and the rule that limits removing the detach finalizer.
3. **Verified release.** NSO keeps a physical claim until IPAM confirms its
   addresses are released.
4. **Port holds and detach.** The `SwitchPortHold`, the binding order, waiting
   for a port, and the detach finalizer.
5. **Test realizer.** The realizer contract against a simulated switch,
   including boot precedence and the one-step move, so NSO's tests, and any
   team's, can run a physical attachment end to end.

## Decisions for a person

- **The realizer that programs real switches.** NSO ships the contract and a
  test realizer. Someone on the Networking team must agree to build the real
  one against this contract, including how it reaches switches, how a network
  maps to a segment at a site, and how it reports the VLAN a network arrives on.
- **Where Fleet writes claims.** This design places physical claims in the
  location's cell, like compute's. Fleet's adapter writes them next to the
  customer's Networks with one client today. Fleet would need a second client
  and write access in each cell, and the platform owners must agree to grant
  it.
- **Which identities are providers, realizers and break-glass operators, and
  which projects are provider projects.** Each cell's admission rules need them
  named, per environment.
- **The boot network's gateway and subnets at each site.** The site router
  relays DHCP from the boot network's gateway address, and the central server
  tells sites apart by subnet. Someone must decide whether NSO's boot network
  is the source the router and the central server are configured from, or the
  other way round, and who keeps them in step.
- **Routing between the boot network and the central cluster.** Each site
  router must route the boot network to the central DHCP and boot file servers
  and to nothing else, and the central cluster must route back to every site's
  boot subnet. Network operations, and whoever runs the central cluster, must
  agree to own each direction.
- **Fleet sets `purpose: Boot`.** NSO knows a boot claim from that field, not
  from the network it names. Fleet must agree to set it on each boot claim.
- **A port NSO cannot release.** This design proposes a break-glass group that
  may remove a detach finalizer after confirming the port on the switch. Who is
  in it, and who confirms the port, needs an owner.
- **Fleet relies on the claim's absence to confirm a release.** Fleet's boot
  attachment work reads NSO's IPAM claim names to confirm an address is free.
  This design keeps those names internal and makes the claim's absence the
  confirmation. Fleet must agree to drop its IPAM check for NSO claims.
- **Fleet moves the attachment point from annotations to typed fields, and
  reads the IPv4 address by family.** Both are changes to Fleet's boot
  attachment work.
- **Boot addresses from the first delivery.** Fleet plans an address-less boot
  claim and a separate IPAM claim at first. This design gives every boot claim
  its address from the start. Fleet must agree to skip the interim step, or ask
  NSO for an address-less physical claim.
- **Checking the attachment point against Fleet's records.** Fleet's proposal
  asks the realizer to check each attachment point against the allocation Fleet
  records. This design trusts the provider identity instead. If that check is
  still wanted, the realizer needs a contract with Fleet that this document
  does not define.
- **The VLAN in Fleet's host configuration.** A port that carries two networks
  delivers at least one tagged. Fleet must agree to carry the VLAN the realizer
  reports into host configuration.
- **Retain on a physical claim.** A retained physical interface keeps its
  addresses and stays off the port. Whether providers need that, or physical
  claims should accept only `Delete`, needs a product answer.
- **Verified release for every mode.** This design verifies release for
  physical claims only. Whether compute's claims should also wait for IPAM to
  confirm, and with what limit on that wait, needs the compute team's view.

## Drawbacks

- **Every network boot needs NSO.** Only NSO attaches a port to the boot network,
  so installs, wipes and recoveries by network boot all wait while NSO is down.
- **A release can be slow, and can stall.** Deleting a physical claim waits on a
  switch and on IPAM. A switch outage holds addresses and servers until it
  clears or an operator steps in.
- **One more object per port.** Each attached port costs a `SwitchPortHold` in
  the cell for as long as any claim names it.
- **The attachment point is trusted.** A provider identity that writes a wrong
  port attaches the wrong server. The admission rule narrows who can make that
  mistake, but it cannot catch the mistake.
- **A stuck holder blocks the port.** A claim that holds a port and never
  becomes ready keeps other projects out until the provider deletes it.
- **Waiting claims are not ordered.** When a port frees up, whichever claim is
  recorded first takes it.

## Alternatives

- **Fleet programs the switch ports itself.** Rejected, as in Fleet's proposal.
  Fleet would hold switch credentials and write the network owner's systems.
- **A separate `PhysicalAttachment` kind instead of a mode.** Rejected. It would
  repeat the claim's addressing, binding and release, and give providers two
  ways to ask for an interface.
- **A boot network marked on the `Network`.** Rejected. Any project can write its
  own networks, so a marker there would let a customer's network outrank others
  on a port. `purpose: Boot` sits on a claim only providers can write.
- **Network claims wait until the boot claim is gone.** Rejected. An install
  needs the customer's addresses before it runs, so customer claims that could
  not bind while the install's boot claim exists would never get them.
- **A handoff object that moves a port from one network to another.**
  Rejected. The hold and the realizer's rules already order the move, and a
  handoff object would be a second writer for the same port.
- **Exclusivity by listing interfaces instead of a hold.** Rejected. Two claims
  reconciled at once can each list, find the port free, and both bind. Writing
  one named hold by compare-and-swap cannot race.
- **Deciding the detach step from whether an interface exists.** Rejected. NSO
  creates the interface after it records the claim, so a crash in between would
  let NSO remove the finalizer for a port the realizer may have changed.
- **The detach finalizer on the interface instead of the claim.** Rejected.
  Under `Retain` the interface is never deleted, so a finalizer on it would
  never be used to signal a detach.
- **Isolation as a property of the network.** Rejected. Isolation is a choice
  for each attachment, so a site can isolate some servers on a network and not
  others.

## Open questions

- **Bonded NICs.** A server whose NICs form an LACP bond reaches a network
  through two ports. This design attaches one port per claim. Whether a bond is
  one attachment point with two ports, or two claims, should be settled with the
  Networking team before bonds are in use.
- **IPv6 on the boot network.** Firmware network boot is mostly IPv4 today.
  Whether boot networks should carry IPv6 for operating systems that boot over
  it is open.

## References

- [Fleet server networking](https://github.com/datum-cloud/fleet/pull/186)
  lists what Fleet needs from NSO.
- [Fleet Machine network attachment](https://github.com/datum-cloud/fleet/pull/198)
  is Fleet's side of this contract.
- [Fleet's IPAM claim contract](https://github.com/datum-cloud/fleet/pull/216)
  shows the per-operation lifetime and verified release Fleet relies on.
- [A network interface a workload can be handed](network-interfaces.md) defines
  the claim and interface this extends.
