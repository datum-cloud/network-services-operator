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
  - [Handing a server from the boot network to a customer](#handing-a-server-from-the-boot-network-to-a-customer)
  - [The boot network](#the-boot-network)
  - [Releasing a claim, and proving it](#releasing-a-claim-and-proving-it)
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

Four rules keep a switch port safe. Only the provider can write a physical
claim. One project holds a port at a time, so moving a server from the boot
network to a customer is one change that NSO puts in order. A port leaves its
network before NSO frees its addresses. NSO deletes a claim only once IPAM no
longer holds any of its addresses.

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
- Give a claim a lifetime its writer controls, and delete a claim only once both
  the claim's addresses and their IPAM allocations are gone.

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
- **Retiring the other modes.** `Netns`, `Hypervisor` and `HypervisorDeclared`
  are unchanged.

## What exists today and what this proposes

| Piece | Today | Proposed here |
|---|---|---|
| `NetworkInterfaceClaim` and `NetworkInterface` | Implemented. Compute writes claims in a location's cell. NSO allocates through IPAM and binds an interface. | Unchanged for existing modes. |
| Attachment modes | `Netns`, `Hypervisor`, `HypervisorDeclared` | Adds `Physical`. |
| Attachment point | None | NIC MAC address, switch, and port, on the claim and its interface. |
| Private segment | None | An optional `isolation: Private` on a physical claim. |
| Who may create a claim | Anyone who can write claims in the cell | Only configured provider identities may write a physical claim. |
| Switch port ownership | None | One project holds a port at a time. Other projects' claims for it wait. |
| Detach before release | None | A physical claim keeps its addresses until the realizer reports the port detached. |
| Deleting a claim | NSO deletes the IPAM claims and removes its finalizer without reading them back. | NSO removes its finalizer only once a read finds each IPAM claim gone and no IPAM allocation naming it. This applies to every mode. |
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
  # Fleet derives the name from the allocation and the interface, so a Fleet
  # restart finds the same claim.
  name: fleet-alloc-3b9d61c2-data
  labels:
    fleet.datumapis.com/allocation: alloc-3b9d61c2
spec:
  network:
    name: app
  interfaceName: data
  attachmentMode: Physical
  attachmentPoint:
    macAddress: "02:00:5e:10:00:01"
    switch: rack-a1-tor-1
    port: Ethernet12
  ipFamilies:
    - IPv4
  reclaimPolicy: Delete
```

NSO binds it as it binds any claim, and the claim reports the answer:

```yaml
status:
  networkInterfaceRef:
    name: fleet-alloc-3b9d61c2-data
  addresses:
    - family: IPv4
      address: 192.0.2.10/24
      gateway: 192.0.2.1
      primary: true
  conditions:
    - type: Bound        status: "True"
    - type: Allocated    status: "True"
    - type: Prepared     status: "True"
    - type: Programmed   status: "True"
    - type: Ready        status: "True"
```

The bound `NetworkInterface` carries the same attachment point, so the physical
realizer reads one object to know which port to add to which network. A
customer reading their interfaces through the project never sees the switch or
the port. NSO already leaves cell-only fields out of the copy it publishes to
the project, and the attachment point is one of them.

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
    macAddress: "02:00:5e:10:00:00"
    switch: rack-a1-tor-1
    port: Ethernet11
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
exactly like any other. The mode changes three things NSO does:

- NSO requires an attachment point, and lets only provider identities write the
  claim.
- NSO lets one project hold a switch port at a time.
- NSO waits for the realizer to detach the port before it releases the claim's
  addresses.

The mode stays immutable, like every other field the interface is built on.

### The attachment point

```yaml
spec:
  attachmentPoint:
    # The NIC's MAC address, from the provider's inventory. Six colon-separated
    # hexadecimal pairs.
    macAddress: "02:00:5e:10:00:01"
    # The switch and port the NIC is cabled to, in the names network operations
    # uses for them.
    switch: rack-a1-tor-1
    port: Ethernet12
```

- A physical claim requires all three fields. A claim in any other mode may not
  carry an attachment point.
- The attachment point is immutable. A server recabled to another port gets a
  new claim.
- NSO copies it verbatim to the bound interface. NSO does not check it against
  any inventory. The provider identity that wrote the claim is trusted for it,
  which is why only provider identities may write one.
- NSO leaves it out of the interface copy published to the project.

The new fields have no server-side defaults. A writer that compares the claim it
reads back with the claim it wrote, as Fleet does, sees no field it did not set.

### A private segment for one server

```yaml
spec:
  isolation: Private
```

`isolation` is optional and only valid on a physical claim. Left unset, the
port joins the network's shared segment and the server can reach every other
member of the network.

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

Fleet's [current adapter](https://github.com/datum-cloud/fleet/pull/198) writes
claims to the API that holds the customer's Networks rather than to a cell.
Moving it is listed under [Decisions for a person](#decisions-for-a-person).

### Who may write a physical claim

An admission rule in each cell refuses any request that creates a claim with
`attachmentMode: Physical`, `attachmentPoint`, or `isolation`, unless the
requester is one of a configured list of provider identities. The list is cell
configuration, and Fleet's identity is the only expected entry.

Compute, and anything else that writes claims today, is not on the list. A
claim compute writes in any other mode is admitted exactly as it is today.

The admission rule checks the authenticated requester, which is the only
identity it can trust. It does not record that identity on the claim.

A second rule protects the detach step. Only the realizer's configured
identity may remove the detach finalizer described
[below](#releasing-a-claim-and-proving-it). Anyone else's update that removes
it is refused.

### One project holds a switch port at a time

A switch port can carry several networks, for example a customer's two networks
as two VLANs on one NIC. Those networks must all belong to one project. A port
shared between two projects would let one project's traffic reach the other's.

NSO keeps one record per switch port in each cell, named from the switch and
port. The record names the project that holds the port as a typed reference:

```yaml
consumerRef:
  apiGroup: resourcemanager.miloapis.com
  kind: Project
  name: customer-example
```

A physical claim binds only once its project holds the port:

- If no project holds it, NSO creates the record for the claim's project. Only
  one create of a given name can succeed, so two projects racing for a port
  cannot both win.
- If the claim's own project holds it, the claim binds.
- If another project holds it, the claim reports `Bound=False` with reason
  `AttachmentPointHeld`, naming nothing about the other project. It allocates
  no address while it waits.

NSO deletes the record once no claim of the holding project names the port,
which is only after the realizer has detached every one of them. A waiting claim
from another project then takes the port.

### Handing a server from the boot network to a customer

Handing a server over is one change from the provider's side. In one pass,
Fleet deletes the server's boot claim and writes the customer's claims. NSO
does the rest in order:

1. The boot claim reports it is releasing. The realizer removes the port from
   the boot network and removes the detach finalizer.
2. NSO releases the boot address and confirms IPAM no longer holds it. The boot
   claim is deleted, and with it the provider project's hold on the port.
3. The customer's claims, which waited with `AttachmentPointHeld`, take the
   port, allocate their addresses and bind.
4. The realizer adds the port to the customer's networks and reports each claim
   `Programmed`.

The port is never in both projects' networks at once, and the realizer is the
only thing that changes its membership. The reverse trip, from a customer back
to the boot network for a wipe, is the same four steps with the projects
swapped.

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

### Releasing a claim, and proving it

A provider that deletes a claim needs to know when the addresses are free and
the port is detached. The claim's absence is that signal. NSO keeps the claim
until both are true.

When a physical claim is created, NSO adds two finalizers: its own, and a detach
finalizer the realizer owns. When the claim is deleted:

1. NSO marks the claim releasing, so `Prepared` and `Ready` go false.
2. The realizer removes the port from the claim's network, or confirms it was
   never added, and removes the detach finalizer.
3. NSO releases the addresses. Under `Delete` it deletes the interface and the
   IPAM claims behind it. Under `Retain` it unbinds the interface, which keeps
   its addresses and stays detached until a claim of the same name binds it
   again.
4. Under `Delete`, NSO reads each IPAM claim back and lists IPAM's allocations.
   It removes its own finalizer only once every IPAM claim is gone and no
   allocation names one. Until then the claim stays, reporting why.

NSO never releases an address while the detach finalizer is present. A claim
whose realizer never answers therefore stays, with its addresses held, and that
is the intended failure. An address freed while a port might still carry it
could be handed to another server on the same network.

Step 4 applies to every attachment mode, not only `Physical`. Today NSO deletes
the IPAM claims and removes its finalizer without reading them back, so a claim
can be gone while IPAM still holds an address for it. Holding the claim until
IPAM confirms changes no address and only delays the deletion until IPAM has
finished.

### The physical realizer

A realizer is whatever turns an interface into a working attachment. For
physical claims, the realizer programs switch ports. NSO defines what the
realizer reads and writes, and ships a test realizer that follows the same
contract against a simulated switch.

The realizer:

- reads `Physical` interfaces in its cell and the claims bound to them;
- works out each port's desired membership from the bound, non-releasing
  claims that name it, and their `isolation`;
- changes a port's membership in one place, one port at a time, so it is the
  only writer of that membership;
- sets `Prepared` and `Programmed` on the interface, and `status.attachmentRef`
  if it has an object to name;
- removes the detach finalizer once a port no longer carries the releasing
  claim's network.

Two rules keep it safe:

- **Check before attaching.** The realizer reads the claim after changing a
  port. If the claim began releasing meanwhile, the realizer removes the
  membership it just added before it removes the detach finalizer.
- **Fail closed.** A realizer that cannot reach a switch leaves `Programmed`
  false and keeps the detach finalizer. It never removes the finalizer on a
  guess.

How the realizer reaches a switch, and how a network maps to a segment at a
site, belong to the Networking team. The test realizer stands in for both in
NSO's tests and in Fleet's.

### Conditions a physical claim reports

A physical claim reports the same five conditions as any claim, with the same
owners:

| Condition | Written by | Physical meaning |
|---|---|---|
| `Bound` | NSO | The project holds the port and an interface is bound. |
| `Allocated` | NSO | Every requested family holds an address. |
| `Prepared` | Realizer | The realizer accepted the attachment point and can build its segment. |
| `Programmed` | Realizer | The port carries the network. |
| `Ready` | NSO | All four are true. |

A provider that installs a server waits for `Allocated` to learn the addresses
it delivers, and for `Programmed` to know the port carries them. Unlike a
container, a physical server can be powered on before its port is programmed,
so waiting on `Programmed` is safe here.

## Failure and recovery

| Failure | What happens |
|---|---|
| NSO restarts while binding | The port record and the IPAM claims are named from the switch port and the claim, so the next attempt finds them. |
| Two projects claim one port at once | One record create wins. The other claim waits with `AttachmentPointHeld`. |
| The realizer restarts while attaching | It recomputes each port's membership from the claims, and removes any membership whose claim is releasing. |
| The realizer is down when a claim is deleted | The claim stays with its addresses held and the detach finalizer present, until the realizer returns. |
| IPAM is unreachable during release | The claim stays, reporting the failed release, and NSO retries. |
| IPAM keeps an allocation after its IPAM claim is gone | The claim stays, reporting the allocation that remains. |
| A claim is deleted and recreated under the same name | The new claim waits for the old one's release to finish, because the old one's finalizers hold the name until then. |

## Delivery

Each step is one draft PR, stacked on the one before it, and each ships with
tests for concurrency, authorization, lifecycle and recovery.

1. **API.** The `Physical` mode, `attachmentPoint` and `isolation` on the claim
   and the interface, with their validation, and the attachment point left out
   of the project copy.
2. **Admission.** The rule that limits physical claims to provider identities,
   and the rule that limits the detach finalizer to the realizer.
3. **Verified release.** NSO keeps every claim until IPAM confirms its addresses
   are released.
4. **Port holds and detach.** The per-port record, the wait for another
   project's hold, and the detach finalizer.
5. **Test realizer.** The realizer contract against a simulated switch, so Fleet
   can test against NSO end to end.

## Decisions for a person

- **The realizer that programs real switches.** NSO ships the contract and a
  test realizer. Someone on the Networking team must agree to build the real
  one against this contract, including how it reaches switches and how a
  network maps to a segment at a site.
- **Where Fleet writes claims.** This design places physical claims in the
  location's cell, like compute's. Fleet's adapter writes them next to the
  customer's Networks today. Fleet and the platform owners must agree on cell
  access for Fleet's identity.
- **Which identities are providers.** Each cell's admission rule needs Fleet's
  identity, and the realizer's. Someone must name them per environment.
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

## Drawbacks

- **A release can be slow, and can stall.** Deleting a physical claim now waits
  on a switch and on IPAM. A switch outage holds addresses and servers until it
  clears.
- **Every mode's deletion now waits on IPAM.** Deleting a compute claim takes
  one more IPAM read before it completes.
- **One more record per port.** Each attached port costs a record in the cell
  for as long as it is attached.
- **The attachment point is trusted.** A provider identity that writes a wrong
  port attaches the wrong server. The admission rule narrows who can make that
  mistake, but it cannot catch the mistake.

## Alternatives

- **Fleet programs the switch ports itself.** Rejected, as in Fleet's proposal.
  Fleet would hold switch credentials and write the network owner's systems.
- **A separate `PhysicalAttachment` kind instead of a mode.** Rejected. It would
  repeat the claim's addressing, binding and release, and give providers two
  ways to ask for an interface.
- **A handoff object that moves a port from one network to another.**
  Rejected. The port record and the detach finalizer already order the move,
  and a separate object would be a second writer for the same port.
- **Exclusivity by listing interfaces instead of a record.** Rejected. Two
  claims reconciled at once can each list, find the port free, and both bind.
  Creating a named record cannot race.
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
