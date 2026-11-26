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
  - [A private segment for one server](#a-private-segment-for-one-server)
  - [Where a physical claim lives](#where-a-physical-claim-lives)
  - [Who may write a physical claim](#who-may-write-a-physical-claim)
  - [One project holds a switch port at a time](#one-project-holds-a-switch-port-at-a-time)
  - [Binding a physical claim, in order](#binding-a-physical-claim-in-order)
  - [Releasing a physical claim, and proving it](#releasing-a-physical-claim-and-proving-it)
  - [Handing a server from the boot network to a customer](#handing-a-server-from-the-boot-network-to-a-customer)
  - [The boot network](#the-boot-network)
  - [The physical realizer](#the-physical-realizer)
  - [Conditions a physical claim reports](#conditions-a-physical-claim-reports)
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
adds the switch port to the network.

The same mode serves two kinds of network:

- **Customer networks.** A server handed to a customer joins the networks the
  customer asked for.
- **The boot network.** A provider-owned network that servers join while they
  are installed or wiped.

Four rules keep a switch port safe:

- Only the provider can write a physical claim.
- One project holds a port at a time. Moving a server from the boot network to
  a customer is one change by the provider, and NSO puts its steps in order.
- A port leaves its network before NSO frees its addresses.
- NSO deletes a physical claim only once IPAM no longer holds any of its
  addresses.

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

Fleet asks NSO for four things:

1. A `Physical` attachment mode.
2. An attachment point on the claim: the NIC's MAC address and the switch and
   port it is cabled to, which Fleet reads from its Device inventory.
3. Claims that only the provider can place, never the customer.
4. A physical realizer that programs the switch port.

A server also needs a network while it is being installed or wiped, before any
customer holds it. Fleet's proposal serves that boot network from a site DHCP
service with addresses taken from IPAM directly. Making the boot network an
NSO network instead gives the boot network and customer networks one attachment
model, one writer for each switch port, and one way to release an address.

### Goals

- Attach a physical server's NIC, through its switch port, to a customer
  network or to the boot network, with the same claim.
- Let only the provider create a physical claim or name a switch port.
- Keep one writer for each switch port's network membership.
- Move a server from the boot network to a customer's networks in one change,
  with the port detached from the boot network before it joins a customer's.
- Optionally give a server its own private segment of a network, so it reaches
  the network's gateway and no other server.
- Assign every address, boot or customer, through IPAM, through NSO.
- Give a claim a lifetime its writer controls, and delete a physical claim only
  once both its IPAM claims and their IPAM allocations are gone.

### Non-goals

- **Configuring the server's operating system.** NSO reports addresses, gateway
  and MTU. Fleet delivers them to the server.
- **Serving DHCP or network boot.** NSO assigns the boot address. Whatever
  answers firmware on the boot network is Fleet's, as its proposal describes.
- **Choosing a segment identifier.** How a network maps to a VLAN or another
  switch-level segment at a site is the realizer's business.
- **A switch port's base profile.** Speed, bonding, the native VLAN, spanning
  tree and LLDP stay with network operations.
- **Changing a running server's networks.** As in Fleet's proposal, a server's
  interfaces are fixed for the life of its allocation.
- **Changing the other modes.** `Netns`, `Hypervisor` and `HypervisorDeclared`
  keep their current behavior, including how their claims are deleted.

## What exists today and what this proposes

| Piece | Today | Proposed here |
|---|---|---|
| `NetworkInterfaceClaim` and `NetworkInterface` | Implemented. Compute writes claims in a location's cell. NSO allocates through IPAM and binds an interface. | Unchanged for existing modes. |
| Attachment modes | `Netns`, `Hypervisor`, `HypervisorDeclared` | Adds `Physical`. |
| Attachment point | None | NIC MAC address, switch, and port, on the claim and its interface. |
| Private segment | None | An optional `isolation: Private` on a physical claim. |
| Who may create a claim | Anyone who can write claims in the cell | Only configured provider identities may write a physical claim. |
| Switch port ownership | None | A `SwitchPortHold` per port names the one project that holds it. |
| Detach before release | None | A physical claim keeps its addresses until the realizer reports the port detached. |
| Deleting a claim | NSO finds each IPAM claim, deletes it, and removes its own finalizer. It does not read the IPAM claim again after the delete. | For physical claims, NSO removes its finalizer only once a read finds each IPAM claim gone and no IPAM allocation naming it. Other modes are unchanged. |
| Copy of the interface in the project | An allow-list of fields. It omits `attachmentMode`. | Stays an allow-list. The attachment point and isolation stay off it. |
| Physical realizer | None | A contract the realizer follows, and a test realizer NSO ships with no real switch behind it. |

## Proposal

### What it feels like

Fleet hands a server to a customer whose project has a network named `app`.
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
    fleet.datumapis.com/machine-uid: 0f8e6a52-1c3d-4b7a-9e21-6d5c4b3a2f10
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

The Machine UID in the label is Fleet's, on Fleet's own label. Nothing in the
claim's spec names an object by UID.

NSO binds it as it binds any claim, and the claim reports the answer:

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
      status: "True"
    - type: Ready
      status: "True"
```

The bound `NetworkInterface` carries the same attachment point, so the physical
realizer reads one object to know which port to add to which network.

A customer reading their interfaces through their project sees addresses,
gateway and MTU, but never the switch or the port. NSO publishes that copy from
an allow-list of fields, and this design leaves the attachment point and
isolation off the list.

A server being installed joins the boot network the same way. The claim lives
in the provider project's namespace and names the provider's boot network:

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
  attachmentPoint:
    macAddress: "00:00:5e:00:53:00"
    switch: rack-a1-tor-1
    port: Ethernet1/11
  isolation: Private
  ipFamilies:
    - IPv4
  reclaimPolicy: Delete
```

### User stories

- **A customer's server joins two networks.** Fleet writes two claims. Each
  binds, the realizer adds each port to its network, and both report `Ready`.
- **A customer cannot attach a port.** A customer, or compute on a customer's
  behalf, writes a claim with `attachmentMode: Physical`. Admission refuses it,
  and nothing is allocated.
- **A server moves from the boot network to a customer.** Fleet deletes the boot
  claim and writes the customer's claims in the same pass. The customer's
  claims wait until the port has left the boot network and its boot address is
  released, then bind.
- **A wipe is isolated.** A server being wiped joins the boot network on a
  private segment and can reach the boot network's gateway, but no other server
  on it.
- **A boot address lives as long as one install.** Fleet creates the claim
  before the install and deletes it after. Fleet treats the address as released
  once the claim is gone, and by then IPAM holds nothing for it.
- **A switch is unreachable.** The realizer cannot detach a port. The claim
  stays, its addresses stay held, and the server stays out of use until the
  port is detached.

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
- NSO lets one project hold a switch port at a time.
- NSO waits for the realizer to detach the port before it releases the claim's
  addresses.
- NSO deletes the claim only once IPAM confirms its addresses are released.

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

### A private segment for one server

```yaml
spec:
  isolation: Private
```

`isolation` is optional, immutable, and only valid on a physical claim. Left
unset, the port joins the network's shared segment and the server can reach
every other member of the network.

`Private` asks the realizer to put the port in a segment of its own on the
network. The server reaches the network's gateway and nothing else on the
network. Its address still comes from the network's subnet, so the boot network
keeps one address space whether its members are isolated or not.

How the realizer builds a private segment is its own choice. A private VLAN with
isolated ports, a VLAN per port, or a filter on the port all satisfy the
contract. A realizer that cannot build one reports the claim `Prepared=False`
with a reason, and never attaches the port to the shared segment in its place.

### Where a physical claim lives

A physical claim lives where compute's claims live today: in the cell that
serves the server's location, in the namespace of the project whose network it
names.

| Network | Namespace |
|---|---|
| A customer's network | The customer project's namespace in the cell |
| The boot network | The provider project's namespace in the cell |

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
change, has `attachmentMode: Physical`, an `attachmentPoint`, or an
`isolation`, unless the requester is one of a configured list of provider
identities. The list is cell configuration, and Fleet's identity is the
expected entry.

Compute, and anything else that writes claims today, is not on the list. A
claim compute writes in any other mode is admitted exactly as it is today.

The rule checks the authenticated requester, which is the only identity it can
trust. It does not record that identity on the claim.

NSO's own identity is exempt, because NSO writes finalizers and status on every
claim.

A second rule protects the detach step. Only three kinds of identity may remove
the detach finalizer described
[below](#releasing-a-physical-claim-and-proving-it):

- the realizer's configured identity;
- NSO's own identity, for a claim that never had an interface; and
- a configured break-glass group of operators.

An operator in the break-glass group uses it only after confirming on the
switch that the port no longer carries the network. NSO's runbook will say how.

### One project holds a switch port at a time

A switch port can carry several networks, for example a customer's two networks
as two VLANs on one NIC. Those networks must all belong to one project. A port
shared between two projects would let one project's traffic reach the other's.

NSO records which project holds each port in a `SwitchPortHold`: a
cluster-scoped object in the cell. NSO is its only writer.

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
  # The project holding the port.
  consumerRef:
    apiGroup: resourcemanager.miloapis.com
    kind: Project
    name: customer-example
  # Every claim of that project bound to the port, by namespace and name.
  holders:
    - namespace: ns-customer-example
      name: fleet-5d2c0e7a91b4f3c86e0d1a2b3c4d5e6f7a8b9c0d
  # Set once another project's claim is waiting for the port.
  draining: false
```

NSO reads the project for `consumerRef` from the claim's namespace, the same
way it finds the project when it allocates addresses today.

Every change to a hold is a compare-and-swap. NSO reads the hold, changes it,
and writes it back. If another writer changed the hold in between, the write
fails and NSO reads it again. That makes each of these exact under concurrency:

- **Taking a free port.** NSO creates the hold with the claim as its only
  holder. Only one create of a given name can succeed, so two projects racing
  for a port cannot both win.
- **Joining a held port.** A claim of the holding project adds itself to
  `holders`, unless the hold is draining.
- **Leaving a port.** A releasing claim removes itself from `holders`. When the
  list becomes empty, NSO deletes the hold, on the condition that nobody has
  changed it since NSO read it. A claim that joined in between makes the delete
  fail, and the hold stays.
- **Waiting for a port.** A claim of another project sets `draining` and reports
  `Bound=False` with reason `AttachmentPointHeld`. It allocates no address while
  it waits, and its message names nothing about the other project.

A draining hold admits no new holders, so the holding project cannot keep a
waiting project out by adding claims. Once the last holder leaves, the hold is
deleted and the next waiting claim creates its own. When several projects wait
at once, the first create wins and the others keep waiting. Fleet allocates a
server to one customer at a time, so several waiting projects mean something
upstream is already wrong.

A port carries a network at most once. A second claim for the same port and the
same network waits, with reason `AttachmentPointHeld`, until the first is
released. That is what happens when a wipe's boot claim is written while the
install's boot claim is still releasing.

A holder that never becomes ready keeps the hold. A waiting claim's reason does
not say who holds the port, because the waiting claim may belong to another
customer. The provider can read the hold itself, in the cell, to see which
claims hold it.

### Binding a physical claim, in order

NSO binds a physical claim in these steps. Each one starts only after the
previous one is written:

1. NSO adds its own finalizer and the detach finalizer in one update. A claim
   deleted before this update completes has neither, takes nothing, and is
   deleted at once.
2. NSO takes or joins the port's hold.
3. NSO allocates the addresses through IPAM.
4. NSO creates or binds the interface, copying the attachment point and
   isolation.
5. NSO reports the claim `Bound` and `Allocated`.

Kubernetes refuses a new finalizer on an object that is being deleted, so step 1
can never add the detach finalizer to a claim that is already releasing. The
realizer never acts on a claim that lacks the detach finalizer, which closes
the other half of that race.

### Releasing a physical claim, and proving it

A provider that deletes a claim needs to know when the addresses are free and
the port is detached. The claim's absence is that signal. NSO keeps the claim
until both are true.

When a physical claim is deleted:

1. NSO marks the claim releasing, so `Prepared` and `Ready` go false.
2. The detach finalizer comes off. Who removes it depends on whether the claim
   ever had an interface:
   - If an interface bound to the claim exists, the realizer removes the port
     from the claim's network, confirms the port no longer carries it, and
     removes the finalizer.
   - If no interface bound to the claim exists, the port was never programmed
     for it, and NSO removes the finalizer itself. A claim waiting with
     `AttachmentPointHeld` is in this case.
3. NSO releases the addresses. Under `Delete` it deletes the interface and the
   IPAM claims behind it. Under `Retain` it unbinds the interface, which keeps
   its addresses and stays detached until a claim of the same name binds it
   again.
4. Under `Delete`, NSO reads each IPAM claim again, and lists the IPAM
   allocations in the same IPAM namespace. The release is verified once each
   IPAM claim is gone and no allocation's `spec.claimRef` names one. Under
   `Retain`, IPAM keeps the allocations on purpose, so the release is verified
   once the interface is unbound.
5. NSO leaves the port's hold.
6. NSO removes its own finalizer, and the claim is gone.

Until each step completes, the claim stays and its conditions say which step it
is waiting on.

NSO never releases an address while the detach finalizer is present. A claim
whose realizer never answers therefore stays, with its addresses held, and that
is the intended failure. An address freed while a port might still carry it
could be handed to another server on the same network.

The claim's name stays taken until step 6. A create under the same name before
then is refused as already existing, and the writer retries once the old claim
is gone. Naming a boot claim from its operation means a new operation never
needs an old operation's name.

The verified release in step 4 applies to physical claims only. Today, for
every mode, NSO finds each IPAM claim, deletes it, and removes its own
finalizer without reading the IPAM claim again. Extending step 4 to compute's
claims would make every claim's deletion wait on IPAM, including during
namespace and project teardown. That choice is listed under
[Decisions for a person](#decisions-for-a-person).

### Handing a server from the boot network to a customer

Handing a server over is one change from the provider's side. In one pass,
Fleet deletes the server's boot claim and writes the customer's claims. NSO
puts the rest in order:

1. The customer's claims find the port held by the provider project. They mark
   the hold draining and wait with `AttachmentPointHeld`.
2. The boot claim releases. The realizer removes the port from the boot
   network, and NSO releases and verifies the boot address.
3. The boot claim leaves the hold. It was the last holder, so NSO deletes the
   hold.
4. The customer's claims take the port, allocate their addresses and bind.
5. The realizer adds the port to the customer's networks and reports each claim
   `Programmed`.

The port is never in both projects' networks at once, and the realizer is the
only thing that changes its membership.

The reverse trip, from a customer back to the boot network for a wipe, follows
the same steps with the projects swapped. Its precondition is that every one of
the customer's claims on the port is deleted. Fleet's proposal also considers a
boot NIC that carries customer networks as tagged VLANs. On such a port, a boot
claim waits behind the customer's hold until the customer's claims are all
gone, which matches Fleet's order of release before wipe.

### The boot network

The boot network is an ordinary NSO `Network` in the provider project. It
reaches each location that has servers the way any network does, and its
subnet in a location provides the gateway servers boot through.

- **Addresses.** A boot claim's addresses come from IPAM through NSO, like any
  claim's. The provider chooses the boot network's address families. Firmware
  usually needs IPv4.
- **Lifetime.** The provider names a boot claim from the operation it serves,
  creates it before the operation starts and deletes it when the operation ends.
  NSO adds no lifetime of its own.
- **Isolation.** The provider sets `isolation: Private` on a boot claim to keep
  the server away from the others on the boot network. Leaving it unset shares
  the segment.
- **Serving firmware.** NSO hands out the address and the gateway. Whatever
  answers firmware's DHCP request with that address is the provider's.

### The physical realizer

A realizer is whatever turns an interface into a working attachment. For
physical claims, the realizer programs switch ports. NSO defines what the
realizer reads and writes, and ships a test realizer that follows the same
contract against a simulated switch.

The realizer:

- reads `Physical` interfaces in its cell and the claims bound to them;
- counts a claim toward a port's membership only while the claim has no
  deletion timestamp and carries the detach finalizer;
- works out each port's desired membership from those claims, their networks
  and their `isolation`;
- changes a port's membership in one place, one port at a time, so it is the
  only writer of that membership;
- writes `Prepared` and `Programmed` on the interface and on the claim, as the
  existing realizer for virtual interfaces does, and `status.attachmentRef` if
  it has an object to name;
- removes the detach finalizer from a deleted claim once the port no longer
  carries that claim's network.

Two rules keep it safe:

- **Check after changing a port.** The realizer reads the claim again after it
  changes a port. If the claim has gained a deletion timestamp meanwhile, the
  realizer removes the membership it just added before it removes the detach
  finalizer.
- **Fail closed.** A realizer that cannot reach a switch leaves `Programmed`
  false and keeps the detach finalizer. It never removes the finalizer on a
  guess.

How the realizer reaches a switch, and how a network maps to a segment at a
site, belong to the Networking team. The test realizer stands in for both in
NSO's own tests, and any other team may run it in theirs.

### Conditions a physical claim reports

A physical claim reports the same five conditions as any claim, with the same
owners:

| Condition | Written by | Physical meaning |
|---|---|---|
| `Bound` | NSO | The project holds the port and an interface is bound. |
| `Allocated` | NSO | Every requested family holds an address. |
| `Prepared` | Realizer, on the interface and the claim | The realizer accepted the attachment point and can build its segment. |
| `Programmed` | Realizer, on the interface and the claim | The port carries the network. |
| `Ready` | NSO, and the realizer when it writes `Programmed` | All four are true. |

The [interface design](network-interfaces.md#how-the-guest-consumes-it-and-when-it-is-safe-to-start)
says nothing may gate creating a workload on `Programmed`. That rule exists
because a virtual interface is attached while its workload's sandbox is being
created, so a consumer that waits for `Programmed` first waits forever.

A physical port is attached with no workload at all. The realizer programs it
as soon as the claim binds, whether the server is on or off. So a provider may
wait for `Programmed` before installing a server or handing it over, as
Fleet's `NetworkReady` already does. The rule for the other modes stands.

## Failure and recovery

| Failure | What happens |
|---|---|
| NSO restarts while binding | Each step is written before the next starts, and the hold and IPAM claims are named from the port and the claim, so the next attempt finds them. |
| Two projects claim one port at once | One hold create wins. The other claim waits with `AttachmentPointHeld`. |
| A project binds a claim while its last claim on the port leaves | Both write the hold by compare-and-swap. Either the join lands first and the hold stays, or the delete lands first and the joining claim creates a new hold. |
| A claim is deleted before NSO adds its finalizers | The claim is deleted at once. Nothing was taken for it. |
| A claim waiting for a port is deleted | No interface was bound to it, so NSO removes the detach finalizer itself and the claim is deleted. |
| The realizer restarts while attaching | It recomputes each port's membership from the claims, and removes any membership whose claim has a deletion timestamp. |
| The realizer is down when a claim is deleted | The claim stays with its addresses held and the detach finalizer present, until the realizer returns or an operator in the break-glass group removes it. |
| IPAM is unreachable during release | The claim stays, reporting the failed release, and NSO retries. |
| IPAM keeps an allocation after its IPAM claim is gone | The claim stays, reporting the allocation that remains. |
| A claim is recreated under a name whose claim is still releasing | The create is refused as already existing. The writer retries after the old claim is gone. |

## Delivery

Each step is one draft PR, stacked on the one before it, and each ships with
tests for concurrency, authorization, lifecycle and recovery.

1. **API.** The `Physical` mode, `attachmentPoint` and `isolation` on the claim
   and the interface, with their validation and immutability, and a test that
   the project copy omits both.
2. **Admission.** The rule that limits physical claims to provider identities,
   and the rule that limits removing the detach finalizer.
3. **Verified release.** NSO keeps a physical claim until IPAM confirms its
   addresses are released.
4. **Port holds and detach.** The `SwitchPortHold`, the binding order, the wait
   for another project's hold, and the detach finalizer.
5. **Test realizer.** The realizer contract against a simulated switch, so
   NSO's tests, and any team's, can run a physical attachment end to end.

## Decisions for a person

- **The realizer that programs real switches.** NSO ships the contract and a
  test realizer. Someone on the Networking team must agree to build the real
  one against this contract, including how it reaches switches and how a
  network maps to a segment at a site.
- **Where Fleet writes claims.** This design places physical claims in the
  location's cell, like compute's. Fleet's adapter writes them next to the
  customer's Networks with one client today. Fleet would need a second client
  and write access in each cell, and the platform owners must agree to grant
  it.
- **Which identities are providers, realizers and break-glass operators.** Each
  cell's admission rules need them named, per environment.
- **Boot addresses through NSO, not IPAM directly.** Fleet's proposal claims
  boot addresses from IPAM itself. This design routes them through NSO's boot
  network. Fleet must agree to move.
- **Checking the attachment point against Fleet's records.** Fleet's proposal
  asks the realizer to check each attachment point against the allocation Fleet
  records. This design trusts the provider identity instead. If that check is
  still wanted, the realizer needs a contract with Fleet that this document
  does not define.
- **Retain on a physical claim.** A retained physical interface keeps its
  addresses and is detached. Whether providers need that, or physical claims
  should accept only `Delete`, needs a product answer.
- **Verified release for every mode.** This design verifies release for
  physical claims only. Whether compute's claims should also wait for IPAM to
  confirm, and with what limit on that wait, needs the compute team's view.

## Drawbacks

- **A release can be slow, and can stall.** Deleting a physical claim waits on a
  switch and on IPAM. A switch outage holds addresses and servers until it
  clears or an operator steps in.
- **One more object per port.** Each attached port costs a `SwitchPortHold` in
  the cell for as long as it is attached.
- **The attachment point is trusted.** A provider identity that writes a wrong
  port attaches the wrong server. The admission rule narrows who can make that
  mistake, but it cannot catch the mistake.
- **A stuck holder blocks the port.** A claim that holds a port and never
  becomes ready keeps other projects out until the provider deletes it.

## Alternatives

- **Fleet programs the switch ports itself.** Rejected, as in Fleet's proposal.
  Fleet would hold switch credentials and write the network owner's systems.
- **A separate `PhysicalAttachment` kind instead of a mode.** Rejected. It would
  repeat the claim's addressing, binding and release, and give providers two
  ways to ask for an interface.
- **A handoff object that moves a port from one network to another.**
  Rejected. The hold and the detach finalizer already order the move, and a
  handoff object would be a second writer for the same port.
- **Exclusivity by listing interfaces instead of a hold.** Rejected. Two claims
  reconciled at once can each list, find the port free, and both bind. Writing
  one named hold by compare-and-swap cannot race.
- **The detach finalizer on the interface instead of the claim.** Rejected.
  Under `Retain` the interface is never deleted, so a finalizer on it would
  never be used to signal a detach.
- **Isolation as a property of the network.** Rejected. Fleet wants a private
  segment per server on a shared boot network, so the choice belongs to each
  attachment.
- **The boot network outside NSO**, as Fleet's proposal describes. Rejected
  here because it gives a switch port two writers and two release paths. It
  remains Fleet's choice, listed above.

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
