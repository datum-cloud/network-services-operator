---
status: provisional
stage: alpha
latest-milestone: "v0.x"
---

# Internet egress for a network

- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-goals](#non-goals)
- [Proposal](#proposal)
  - [Declaring internet access](#declaring-internet-access)
  - [Naming destinations instead of mechanisms](#naming-destinations-instead-of-mechanisms)
  - [Reporting the egress address](#reporting-the-egress-address)
  - [Constraints and caveats](#constraints-and-caveats)
- [Design details](#design-details)
  - [Network API changes](#network-api-changes)
  - [The internet egress class](#the-internet-egress-class)
  - [Reporting per location](#reporting-per-location)
  - [Programming the data plane](#programming-the-data-plane)
  - [Realizing egress in a cell](#realizing-egress-in-a-cell)
  - [Allocating the egress address](#allocating-the-egress-address)
  - [Reporting failure](#reporting-failure)
  - [Disabling egress](#disabling-egress)
  - [Reserving the interface field](#reserving-the-interface-field)
- [Dependencies](#dependencies)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
- [Open questions](#open-questions)
- [References](#references)

## Summary

This document proposes **internet egress** for a network. Internet egress is outbound
traffic from instances on a network to destinations outside the platform.

A network holds private addresses. No field lets a consumer declare that instances on the
network reach the internet. Every network carries IPv6 by default, and an instance holding
only an IPv6 address cannot reach an IPv4-only destination.

This design adds one field group to the network resource. A consumer declares whether
instances reach the internet and which address families those instances reach. The platform
selects the translation mechanism, allocates a source address, and reports that address to
the consumer.

**Audience:** engineers working on the network services operator, the compute service, and
the network data plane. This document assumes you understand networks, network contexts,
and network interfaces.

**Terminology:** *address translation* rewrites the source address of an outbound packet so
that replies return to the platform. An *egress address* is the source address that
translation writes. An *address class* is a named policy that the addressing service
allocates addresses from.

## Motivation

Three problems motivate this design.

**An IPv6-only instance reaches almost nothing.** A network defaults to IPv6, and the
platform rejects an IPv4-only network. The default instance therefore holds a private IPv6
address and no path to an IPv4 destination. Most services that a workload calls, such as
package registries and payment APIs, still accept only IPv4.

**An operator decides internet access, not a consumer.** Today an operator sets internet
access on a node at deployment time. The setting applies to every network on that node. A
consumer cannot enable it, cannot disable it, and cannot read whether an operator enabled
it.

**No consumer can read their egress address.** A consumer whose destination requires an
allow-list has no field to read. That consumer discovers the address by sending a request
and inspecting the far end. The consumer cannot tell whether the address is stable or
exclusive to their network.

### Goals

- Let a consumer declare on one resource whether instances on a network reach the internet.
- Let an instance on an IPv6 network reach IPv4 destinations without naming a mechanism.
- Report the egress address and report how far a consumer may rely on that address.
- Report an inability to provide egress on a resource that consumers already read.
- Accept a dedicated egress address, and later a consumer-supplied address, without an API
  break.
- Fix the interface-level default now, so that adding per-interface control later does not
  change what an existing interface means.

### Non-goals

- **Filtering destinations.** This design decides whether a network reaches the internet.
  Security groups decide which destinations a network reaches.
- **Inbound reachability.** External addresses on an interface handle inbound traffic. This
  design covers outbound traffic only.
- **Selecting a specific egress address.** A dedicated or consumer-supplied address is a
  later stage. This design reserves the field for that stage.
- **Controlling egress per interface.** The first phase decides internet access for a whole
  network. A later phase decides it per interface. [Reserving the interface
  field](#reserving-the-interface-field) explains what ships now.
- **Defining the translation.** The data plane owns how it translates a packet. This design
  defines what a consumer requests and what the node receives.

## Proposal

### Declaring internet access

A consumer declares internet access on the network:

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: Network
metadata:
  name: default
spec:
  ipFamilies: [IPv6]
  egress:
    internet:
      mode: Enabled
      reach: [IPv6, IPv4]
```

Instances on this network reach IPv6 destinations directly. Those instances reach IPv4
destinations through translation that the consumer never configures. The consumer creates
no gateway resource, writes no route, and requests no address.

The declaration applies to every instance on the network. A consumer cannot enable internet
access for one instance and disable it for another in the first phase.

### Naming destinations instead of mechanisms

The `reach` field names destination address families. The field does not name a translation
mechanism.

Setting `reach: [IPv4]` on an IPv6 network states that instances call IPv4-only services.
The platform answers that request with address translation and with a resolver that returns
synthesized addresses for names that publish no IPv6 record. A consumer cannot act on the
difference between IPv6-to-IPv6 and IPv6-to-IPv4 translation, so the API does not expose
that difference.

Naming destinations also keeps the field accurate across future changes. The platform can
replace a translation mechanism, or assign real IPv4 addresses instead of translating. Both
changes alter platform behavior and leave the consumer's declaration correct.

### Reporting the egress address

The status reports the egress address and reports how far a consumer may rely on it:

```yaml
status:
  egress:
    internet:
      sourceAddresses:
      - family: IPv6
        address: 2001:db8:f00d::100
        stability: None
      dns64Prefix: 64:ff9b::/96
```

The `stability` field carries the contract:

| Value | Meaning | Consumer guidance |
|---|---|---|
| `None` | The address may change, and other networks share it | Do not allow-list the address |
| `Network` | The address belongs to this network and persists | Allow-listing the address is safe |

A consumer who allow-lists a shared address admits traffic from other networks and loses
access when the address changes. The `stability` field states both risks before the
consumer acts.

A later stage gives a network its own egress address. That stage changes `stability` from
`None` to `Network`. No field changes, and no previously reported value becomes incorrect.

The status omits facts about how the platform delivers egress, including which node carries
the traffic, how the platform partitions translation state, and which other networks share
the path. A consumer cannot act on those facts. Some of those facts describe other
consumers.

### Constraints and caveats

**The first stage shares one egress address.** One address serves every network that reaches
the internet from the same location. The `stability` field reports this constraint rather
than hiding it.

**Networks share translation capacity.** Translation state and port space are finite, and
the first stage shares both. A network that opens many connections degrades another network
on the same path. No per-network signal tells the affected consumer why connections failed.
This design names the gap and does not close it. A fabricated limit field would mislead
consumers more than an absent field does.

**Reaching IPv4 destinations by name requires a matching resolver.** Synthesized addresses
work only when the resolver and the translator share a prefix. Instances that use their own
resolver do not reach IPv4 destinations by name.

## Design details

### Network API changes

`NetworkSpec` gains an `egress` field:

| Field | Type | Default | Notes |
|---|---|---|---|
| `egress.internet.mode` | `Enabled` or `Disabled` | See [Drawbacks](#drawbacks) | Mutable |
| `egress.internet.reach` | `[]IPFamily` | The network's `ipFamilies` | May exceed `ipFamilies` |
| `egress.internet.class` | `string` | The default class | Names an `InternetEgressClass` |

The `reach` field may name a family that the network does not carry. Naming IPv4 on an IPv6
network is the primary use case. The field may not name a family that the platform cannot
translate to, and validation rejects such a value.

The `class` field names an `InternetEgressClass`. A consumer who omits the field receives
the default class, so the common case names no class at all. Validation rejects an
unavailable class as absent rather than as forbidden, so that validation does not enumerate
platform resources.

A consumer never names an address class, an address pool, or an address. Those are
addressing-service concepts, and a consumer who had to name one would be configuring the
platform rather than declaring an outcome.

### The internet egress class

`InternetEgressClass` is a cluster-scoped resource that an operator defines and a consumer
names. The resource follows the pattern that `ConnectorClass` already establishes in this
API group: the class carries the consumer contract and names a controller, and
implementation detail sits behind a parameters reference.

| Field | Type | Notes |
|---|---|---|
| `controllerName` | `string` | The controller that realizes this class |
| `sharing` | `Shared` or `Dedicated` | Whether networks share one egress address |
| `reach` | `[]IPFamily` | The destination families this class can deliver |
| `parametersRef` | object reference | Implementation configuration |

The `sharing` field earns the class its place in the design. The field is the operator-side
decision whose consumer-side projection is `stability`: `Shared` produces `stability: None`,
and `Dedicated` produces `stability: Network`. One decision produces both the platform
behavior and the guidance a consumer reads.

The parameters reference points at a resource in the implementing component's API group. The
parameters carry which address class the egress address is allocated from, which locator the
data plane allocates identifiers from, and how the implementation selects the resources that
serve the class. None of those facts belong in a resource that a consumer reads.

The reference runs in one direction. The class names its controller, and the implementation
watches for classes naming it; no data-plane resource references the class. Keeping the
reference one-directional lets the data-plane API group stay independent of this one.

An operator marks one class as the default by annotation, which is how the addressing
service already handles the same problem.

**`InternetEgressClass` does not filter traffic.** The resource decides how a network reaches
the internet, not which destinations a network may reach. Security groups decide the second
question, and the name is explicit so that the two are not confused.

### Reporting per location

The platform realizes egress per location. A network present in two locations reaches the
internet from two places and receives two egress addresses.

The network resource therefore holds the declaration, and each network context reports the
result for its location. An interface's status carries the address for the location that
runs the instance, which answers the question a consumer asks when they inspect an instance.

Reporting one address on the network would produce one of two errors. The status would
present one location's address as global, or the status would list two addresses that the
consumer cannot attribute to a location.

### Programming the data plane

The network services operator does not program the data plane. The operator records intent
where the components that program the data plane already read.

```
Network                 Consumer intent        mode, reach, class
        |  per location
        v
NetworkContext          Result per location    egress addresses, resolver prefix
        |  realized by
        v
VPCAttachment           Node instruction       egress enabled for this VPC
        |  attaches to
        v
VPC                     Data plane             per-network egress route
```

The attachment carries the handoff. An infrastructure provider already writes the attachment
as intent before the pod exists, and the node already reports on it. The attachment already
carries the network's identity in the fabric. The node therefore learns that a network
reaches the internet from the same resource that supplies every other fact about the
interface. The node installs an egress route into that network's routing context, or
installs no route.

A per-network instruction replaces a per-node setting, which is what gives `Disabled` an
effect. A node that installs one route for every network cannot express a network that
requires no route.

The data plane owns what happens to a packet after the node applies the route, including how
it translates the packet, how it stores state, and how a reply returns. The data plane
documents that behavior. The contract in this design ends at the attachment.

### Realizing egress in a cell

An **egress shard** is the data-plane resource that translates outbound packets for the
networks an egress class places on it. A shard runs on one node. A cell runs one or more
shards.

A cell holds four things that this design depends on:

- **Network contexts**, which record each network's presence in the cell.
- **Egress shards**, one for each node that performs translation.
- **A cell controller**, which claims addresses and binds networks to shards.
- **Attachments**, which carry the per-network instruction to a node.

```
  cell controller
        |
        +--  claims one address for each shard  ------>  IPAM
        |
        +--  writes that address into a shard spec  -->  EgressShard    ->  translation
        |
        +--  binds a network to a shard  ------------->  VPCAttachment  ->  egress route
```

**The cell controller claims the address, not the shard.** A shard runs on every translating
node, sits inside the data path's blast radius, and runs on hardware at the edge of the
network. Making a shard an addressing-service client would place platform credentials on
every such node and put an allocation request near the path that attaches a workload. The
cell controller claims the address and writes it into the shard's spec. The shard reads the
spec, programs the data plane, and reports status. That split matches the contract that
attachments already follow: a controller writes intent, and a node reports what it carries.

**A shard holds no list of the networks it serves.** The data plane identifies a network
from an identifier that the packet itself carries, which a node stamps when it attaches an
interface. Binding a network to a shard therefore means telling that network's nodes which
shard to send to. A shard needs no notification when a network binds or unbinds, and the
binding is realized entirely on the node that attaches the interface.

The platform realizes egress in eight steps:

1. An operator creates an egress class naming a controller, a sharing mode, and parameters.
2. An operator creates an egress shard for each translating node in the cell.
3. The cell controller claims an address for each shard and writes the address into the
   shard's spec.
4. The shard programs the data plane, reports status, and advertises the address.
5. A consumer enables egress on a network.
6. The cell controller binds that network's context to a shard serving the requested class.
7. The infrastructure provider records the bound shard on the attachment.
8. The node installs an egress route for that network toward the bound shard.

**The data plane requires three changes.** Today an operator supplies a shard's address as
process configuration, and the shard echoes the value into its status; the address must
become spec that a controller writes. Today a node holds one list of shards and installs a
route toward the first reachable entry for every network on the node; the instruction must
become per-network, or `Disabled` has no effect. Today an operator also chooses each shard's
data-plane identifier by hand, and choosing a value that a node already uses silently
diverts that node's traffic; the addressing service should allocate the identifier for the
same reason it allocates the address.

Explicit binding is also what makes failover possible. A shard that fails today drops the
traffic it carried, and no component reassigns the networks it served, because no component
recorded which networks those were. Once a binding is a recorded fact, reassignment is a
controller updating attachments.

### Allocating the egress address

The egress address is publicly routable, unlike every address a network holds today. The
addressing service allocates the egress address from the address class that the egress
class's parameters name, which gives the platform three properties:

- The platform accounts for the address.
- The platform cannot allocate the address twice.
- The platform reclaims the address when the resource holding it goes away.

**No network claims an egress address.** Under `sharing: Shared`, one address serves every
network the class places on the same data-plane resource, because per-network blocks exhaust
a public aggregate long before networks exhaust it. The address therefore belongs to that
resource and outlives every network using it, which is why `stability` reports `None`. The
platform also retains the address across restarts, because a consumer cannot rely on an
address that changes when a process restarts.

### Reporting failure

The network context carries an `InternetEgressReady` condition:

| Reason | Meaning |
|---|---|
| `Ready` | Instances in this location reach the declared destinations |
| `AddressUnavailable` | The platform allocated no egress address for this location |
| `Unavailable` | No component in this location provides egress |
| `Degraded` | Egress works for some declared families and not for others |

Each reason states a fact about the consumer's network. The condition omits the specific
cause, such as the failing component, node, or allocation. A consumer cannot act on those
causes, and reporting them tells a consumer where the platform runs their workload.
Operator events and operator alerts carry the specific cause.

### Disabling egress

Setting `mode: Disabled` removes the egress route. The change takes effect on interfaces
that attach after the change. Withdrawing egress from a running instance is the same problem
as changing any other programmed property of a live attachment, and this design does not
solve it.

### Reserving the interface field

`NetworkInterfaceClaimSpec` gains `egress.internet.mode`, and validation accepts one value:

| Value | Accepted in the first phase | Meaning |
|---|---|---|
| `Inherit` | Yes | The interface follows the network's declaration |
| `Enabled` | No | The interface reaches the internet regardless of the network |
| `Disabled` | No | The interface reaches no internet destination |

Reserving the field settles the default before consumers depend on it. An interface written
today records `Inherit`, so adding `Enabled` and `Disabled` later changes no existing
interface and reclassifies no existing behavior. Adding the field later instead of now would
also be backward compatible, but it would force the platform to choose a default for
interfaces that already exist, and the safe choice at that point is the one this design can
record today.

The data plane cannot honor `Enabled` or `Disabled` on an interface yet. A node installs one
egress route per network, not per interface. Widening the accepted values therefore depends
on per-interface routing in the data plane, which no component implements.

## Dependencies

This design depends on four items that it does not deliver:

1. **A public address class.** The platform must allocate public address space before egress
   ships outside a lab. No component allocates it today.
2. **A per-network egress route.** The data plane must install the route per network rather
   than per node. Without that change, `Disabled` has no effect.
3. **A resolver that matches the translator.** The platform must pair both before it offers
   `reach: [IPv4]`.
4. **Rate limiting and per-network attribution.** Both block launch independently of this
   API. The absence of attribution is why this design cannot report the capacity gap.

## Drawbacks

**The useful default is unsafe today.** An IPv6-only network without egress reaches nothing,
so `Enabled` is the default that serves consumers. `Enabled` also opens an outbound path on
every new network. The platform must therefore default to `Disabled` until it can rate limit
and attribute egress, which makes the common case require an explicit field.

**The API promises less than its name suggests.** A field named `egress.internet` reads as a
guarantee of reachability. The first version delivers a shared path with no capacity
guarantee. The `stability` field carries that caveat, and it carries the caveat in a status
field that a consumer may not read.

**Networks gain a second axis of variation.** A network already varies by address family. A
network now also varies by what it reaches. A workload moves between two networks only when
a consumer declared both networks the same way.

## Alternatives

**Attach a gateway resource to a network.** Other clouds use this shape: a consumer creates
an internet gateway and associates it with a network. This design rejects that shape because
it requires a consumer to create and wire a resource to express one boolean, and because the
association carries no configuration that the network cannot carry. The shape becomes
preferable if egress gains properties of its own, such as a bandwidth tier or a dedicated
address pool.

**Set internet access per interface only.** A per-interface field is more precise. A field
on the interface also forces a consumer to set internet access on every interface of every
instance. A network-level declaration with a later per-interface override offers the same
precision and a usable default.

**Reuse the existing per-attachment egress policy type.** A type of this shape exists in the
fabric API group, and no component reads it. The type names fabric identities that a
consumer never sees, which makes it a candidate internal representation rather than a
consumer-facing API. [Open questions](#open-questions) covers whether the platform revives
or replaces that type.

**Assign public addresses to networks instead of translating.** Direct assignment removes
translation, the shared address, and the capacity coupling. Direct assignment also requires
far more public address space and a different security posture. Direct assignment does not
remove the need for this field, because a network still declares whether it reaches the
internet.

## Open questions

1. **Does `reach: [IPv4]` oblige the platform to provide the resolver?** If a consumer must
   configure their own resolver, the field promises reachability that the platform does not
   deliver.
2. **What identifies an interface to the data plane when per-interface control ships?** The
   node currently installs one route per network, and a per-interface route needs an
   identifier that the attachment already carries.
3. **Does the platform default to `Enabled` or `Disabled` at launch?** `Enabled` serves
   consumers and remains unsafe until egress is rate limited and attributable.
4. **Does the platform revive the existing per-attachment egress policy type as the internal
   representation, or replace it?** Two code comments describe the type as superseded, and
   no design supersedes it.
5. **What does a consumer read when shared capacity is exhausted?** Today a consumer reads
   nothing and observes failed connections. Answering this question requires per-network
   accounting that no component performs.

## References

- [A network in every location it is used](network-in-every-location.md) — the per-location
  presence that reports egress results
- [A network interface a workload can be handed](network-interfaces.md) — the interface and
  attachment contract that this design extends
- [Cell controller manager](cell-controller-manager.md) — the controller that claims egress
  addresses and binds networks to shards
