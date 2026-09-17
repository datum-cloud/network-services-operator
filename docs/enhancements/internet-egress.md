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
  - [The internet egress class](#the-internet-egress-class)
  - [Reporting per location](#reporting-per-location)
  - [Realizing egress in a cell](#realizing-egress-in-a-cell)
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

A consumer who needs a particular kind of egress names an `InternetEgressClass` on
`egress.internet.class`. Omitting the field selects the default class, so the common case
names nothing. A consumer never names an address class, an address pool, or an address.

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

- `None`: the address may change, and other networks share it. Do not allow-list it.
- `Network`: the address belongs to this network and persists. Allow-listing it is safe.

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

### The internet egress class

`InternetEgressClass` is a cluster-scoped resource that an operator defines and a consumer
names. The resource follows the pattern that `ConnectorClass` already establishes in this
API group.

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: InternetEgressClass
metadata:
  name: shared
  annotations:
    networking.datumapis.com/is-default-class: "true"
spec:
  controllerName: networking.datumapis.com/cell-egress
  sharing: Shared
  reach: [IPv6, IPv4]
  parametersRef:
    group: network.datumapis.com
    kind: EgressShardParameters
    name: shared-ipv6
```

The `sharing` field is the operator-side decision whose consumer-side projection is
`stability`: `Shared` produces `None`, and `Dedicated` produces `Network`.

The parameters name the address class that the egress address comes from, the locator that
the data plane allocates identifiers from, and how the implementation selects the resources
serving the class. The reference runs one way. The class names its controller, and no
data-plane resource references the class, which lets the data-plane API group stay
independent of this one.

An operator marks one class as the default by annotation. The class decides how a network
reaches the internet, not which destinations a network may reach.

### Reporting per location

The platform realizes egress per location, so a network present in two locations receives two
egress addresses. The network holds the declaration, and each network context reports the
result for its location. Reporting one address on the network would either present one
location's answer as global or list two addresses that a consumer cannot attribute.

### Realizing egress in a cell

An **egress shard** is the data-plane resource that translates outbound packets for the
networks that a class places on it. A shard runs on one node, and a cell runs one or more
shards. A cell controller claims addresses and binds networks to shards.

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
node and on hardware at the edge of the network. Making a shard an addressing-service client
would place platform credentials on every such node and put an allocation request near the
path that attaches a workload. The controller writes the address into the shard's spec, and
the shard programs the data plane and reports status, which is the split that attachments
already follow.

**A shard holds no list of the networks it serves.** A node stamps an identifier that each
packet carries, so binding a network to a shard means telling that network's nodes which
shard to send to. A shard needs no notification when a network binds or unbinds.

**No network claims an egress address.** Under `sharing: Shared`, one address serves every
network that the class places on a shard, because per-network blocks exhaust a public
aggregate long before networks exhaust it. The address belongs to the shard, outlives every
network using it, and survives a restart.

The infrastructure provider records the bound shard on the attachment, which already carries
the network's identity to the node before a pod exists. The node installs an egress route
for that network toward that shard, or installs no route.

**The data plane requires three changes.** An operator supplies a shard's address as process
configuration and the shard echoes the value into status; the address must become spec that
a controller writes. A node holds one list of shards and routes every network on it toward
the first reachable entry; the instruction must become per-network, or `Disabled` has no
effect. An operator also chooses each shard's data-plane identifier by hand, and a value
that a node already uses silently diverts that node's traffic; the addressing service should
allocate the identifier.

Explicit binding is also what makes failover possible. A shard that fails today drops the
traffic it carried, because no component recorded which networks it served.

### Reporting failure

The network context carries an `InternetEgressReady` condition, with four reasons:

- `Ready`: instances in this location reach the declared destinations.
- `AddressUnavailable`: the platform allocated no egress address for this location.
- `Unavailable`: no component in this location provides egress.
- `Degraded`: egress works for some declared families and not for others.

Each reason states a fact about the consumer's network. The condition omits the specific
cause, such as the failing component, node, or allocation, because a consumer cannot act on
those causes and reporting them tells a consumer where the platform runs their workload.
Operator events carry the specific cause.

### Disabling egress

Setting `mode: Disabled` removes the egress route, and the change takes effect on interfaces
that attach afterwards. Withdrawing egress from a running instance is the same problem as
changing any other programmed property of a live attachment, and this design does not solve
it.

### Reserving the interface field

`NetworkInterfaceClaimSpec` gains `egress.internet.mode`, and validation accepts only
`Inherit`, which follows the network's declaration. Reserving the field settles the default
before consumers depend on it: an interface written today records `Inherit`, so accepting
`Enabled` and `Disabled` later changes no existing interface. Widening the accepted values
depends on per-interface routing, which no component implements.

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
