---
status: provisional
stage: alpha
latest-milestone: "v0.x"
---

# An internet a network can reach

- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [What it feels like](#what-it-feels-like)
  - [Which internet, not which translation](#which-internet-not-which-translation)
  - [What a consumer can see](#what-a-consumer-can-see)
  - [Notes/Constraints/Caveats](#notesconstraintscaveats)
- [Design Details](#design-details)
  - [What a network declares](#what-a-network-declares)
  - [What a network context reports](#what-a-network-context-reports)
  - [Reaching the data plane](#reaching-the-data-plane)
  - [Where the egress address comes from](#where-the-egress-address-comes-from)
  - [Failure, reported as itself](#failure-reported-as-itself)
  - [Turning it off](#turning-it-off)
- [What this depends on](#what-this-depends-on)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
- [Open Questions](#open-questions)
- [References](#references)

## Summary

A network's address space is private. Nothing a consumer can write today says that the
instances on it may reach the internet, and an instance on an IPv6-only network — which is
every network by default — cannot reach an IPv4-only destination at all.

This document adds one capability to a network: **`spec.egress.internet`**, which says
whether instances on this network reach the internet and which internet they reach. A
consumer names the outcome they want; the platform chooses the translation, allocates the
source address, and reports back what that address turned out to be.

The data plane that performs the translation already exists. What is missing is the noun a
consumer writes and the contract that carries it to the node, which is what this proposes.

## Motivation

**An IPv6-only instance can reach almost nothing.** A network defaults to IPv6 and an
IPv4-only network is rejected outright, so the default instance has a private IPv6 address
and no path to an IPv4 destination. Most of the internet a workload actually calls — package
registries, payment APIs, webhooks — is still reachable only over IPv4.

**Internet access is an operator's decision, not a consumer's.** Whether tenants on a node
reach the internet is a deployment-time setting on that node. It is all or nothing for
everything on it, invisible to the consumer, and not expressible per network. A consumer
cannot turn it on, cannot turn it off, and cannot tell whether it is on.

**Nobody can see the address they leave from.** A consumer whose destination requires an
allow-list has no field to read. They discover the address by making a request and looking
at the other end, and they have no way to know whether it is stable, exclusively theirs, or
about to change.

### Goals

- One field on a network that decides whether its instances reach the internet.
- Let a consumer on an IPv6 network reach IPv4 destinations without naming a mechanism.
- Report the source address a consumer's traffic leaves from, and how much they may rely
  on it.
- Report inability to provide egress as a condition on the object a consumer already reads.
- Leave room for a dedicated, and later a consumer-supplied, egress address without an API
  break.

### Non-Goals

- **Filtering destinations.** This decides whether a network reaches the internet at all.
  Which destinations it may reach is a security group, and belongs in that design.
- **Inbound reachability.** An address reachable from outside is `externalAddresses` on an
  interface; this is the outbound direction only.
- **Choosing an egress address.** A dedicated or consumer-supplied address is a later stage
  this shape makes room for, not something proposed here.
- **Owning the translation.** How a packet is translated belongs to the data plane; this
  defines only what a consumer asks for and what the node is told.

## Proposal

### What it feels like

A consumer writes a network and says their workloads reach the internet.

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

Instances on that network reach IPv6 destinations directly and IPv4 destinations through
translation the consumer never configures. Nothing else changes: no gateway object to
create, no route to write, no address to request.

### Which internet, not which translation

`reach` names destinations, not mechanisms. `IPv4` on an IPv6 network means "my workloads
call IPv4-only services" — the platform answers with address translation and a resolver that
returns synthesized addresses for names that have no IPv6 record. A consumer who has to
learn the difference between translating IPv6-to-IPv6 and IPv6-to-IPv4 has been handed an
implementation detail they cannot act on.

This also keeps the field honest across changes. Replacing the translation mechanism, or
giving a network real IPv4 addresses instead of translating for it, changes what the platform
does and not what the consumer wrote.

### What a consumer can see

A consumer reads the address their traffic leaves from, and **how much they may depend on
it**:

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

`stability` is the field that matters. `None` means the address may change and is not
exclusively this network's, so allow-listing it at a destination will eventually break and
will let in traffic that is not theirs. When a network can hold its own egress address, the
same field reads `Network` and the guidance inverts — without a new field, and without any
value that was previously true becoming a lie.

What a consumer does **not** see is every fact about how egress is delivered: which node
carries their traffic, how translation state is partitioned, or what else shares the path.
None of it is actionable, some of it describes their neighbours, and all of it constrains
the platform's ability to change.

### Notes/Constraints/Caveats

**The first stage shares an address.** One address serves every network reaching the
internet through the same place. This is why `stability: None` exists and why it must be
reported rather than glossed over: a consumer who allow-lists a shared address has been
misled by the API, not by their own mistake.

**Shared capacity is shared.** Translation state and port space are finite and, at this
stage, common. A network generating heavy connection churn can degrade another's, and there
is no per-network signal telling the affected consumer why their connections failed. This
is a gap this design names rather than solves — a fabricated limit field would be worse
than an absent one.

**Reaching the IPv4 internet requires the resolver to agree.** Synthesized addresses only
work if the resolver instances use and the translator on the path share a prefix. A network
whose instances use their own resolver will not reach IPv4 destinations by name.

## Design Details

### What a network declares

`NetworkSpec` gains `egress`:

| Field | Type | Notes |
|---|---|---|
| `egress.internet.mode` | `Enabled` \| `Disabled` | Defaulted, written explicitly, mutable |
| `egress.internet.reach` | `[]IPFamily` | Defaults to the network's `ipFamilies` |
| `egress.internet.addressClass` | `string` | Optional; names an address class, not a pool or address |

`reach` may name a family the network does not carry — that is the point of `IPv4` on an
IPv6 network. It may not name a family the platform cannot translate to, which is a
validation against the classes available to the project.

`addressClass` exists from the first version even while one class is available, because it
is the seam a dedicated or consumer-supplied address arrives through. Which classes a
project may name is already decided by the addressing service; a class the project cannot
use is rejected as absent rather than as forbidden, so that validation does not enumerate
what the platform has.

### What a network context reports

Egress is realized **per location**. A network present in two locations reaches the internet
from two different places with two different source addresses, so `sourceAddresses` belongs
on the network's presence in a location and not on the network. Reporting one address on the
network would either present one location's answer as global or concatenate two answers
into a list a consumer cannot attribute.

The network keeps the declaration; each `NetworkContext` reports the answer for its location,
and an interface's status carries the address for the location its instance is actually in —
which is the one a consumer running `kubectl get instance` is asking about.

### Reaching the data plane

NSO does not program the data plane. It decides intent and records it where the components
that do program it already read.

```
Network                 the consumer's intent   mode, reach, addressClass
        │  per location
        ▼
NetworkContext          the answer here         source addresses, translation prefix
        │  realized by
        ▼
VPCAttachment           egress on this VPC      what the node is told
        │  attaches to
        ▼
VPC                     the data plane          per-network egress route
```

The attachment is the handoff. It is already the object written as intent before a pod
exists and reported on by the node, and it already carries the network's identity in the
fabric — so the node learns that *this* network reaches the internet from the same object
that tells it everything else about the interface. The node installs the egress route into
that network's routing context, or does not.

This replaces a node-wide setting with a per-network one, which is what makes `Disabled`
mean anything. A route installed for every network on a node cannot express a network that
should not have one.

What the node does with the packet after that — how it translates, how it keeps state, how
a reply finds its way back — belongs to the data plane and is documented with it. The
contract this design owns ends at the attachment.

### Where the egress address comes from

The source address is publicly routable, which makes it unlike every address a network
holds today. It is allocated from an address class the same way every other address is, so
that it is accounted for, cannot be double-allocated, and is reclaimed when the last network
using it goes away.

Two properties follow. A shared address is allocated **once per location**, not per network,
because per-network blocks of public space exhaust the aggregate long before the networks
do — which is why `stability: None` is the honest first answer. And the address is retained
across restarts, because a source address that changes when a process restarts is one no
consumer can build on even briefly.

### Failure, reported as itself

`InternetEgressReady` on the network context:

| Reason | Meaning |
|---|---|
| `Ready` | Instances in this location reach the declared internet |
| `AddressUnavailable` | No egress address could be allocated for this location |
| `Unavailable` | Nothing in this location can currently provide egress |
| `Degraded` | Egress is provided, but not for every declared family |

A consumer is told what is true of their network. The specific cause — which component,
which node, which allocation — is an operator's event and an operator's alert, because a
consumer can act on none of it and reading it tells them where they are running.

### Turning it off

`mode: Disabled` removes the route and takes effect on interfaces attached afterwards.
Withdrawing egress from a running instance is the same problem as changing any other
programmed property of a live attachment and is deliberately not solved here.

## What this depends on

- **A public address class.** Public address space must be allocatable before egress can be
  provisioned anywhere but a lab. Nothing allocates it today.
- **Per-network egress in the data plane.** The route must be installable per network rather
  than per node, or `Disabled` cannot be honoured.
- **A resolver that agrees with the translator**, before `reach: [IPv4]` can be offered.
- **Rate limiting and per-network attribution.** Both are launch blockers independent of
  this API, and the second is why the capacity gap above cannot yet be reported.

## Drawbacks

**It defaults to on.** An IPv6-only network with egress disabled is inert, so the useful
default is `Enabled` — which means every new network is an open outbound path. That is only
safe once egress can be rate limited and attributed, and until then the default must be
`Disabled` even though it makes the common case require a field.

**It promises less than it looks like it promises.** A field named `egress.internet` reads
like a guarantee of reachability, while the first version delivers a shared, unattributed
path with no capacity guarantee. `stability` carries that caveat, but it carries it in one
field of a status a consumer may not read.

**It adds a second place networks differ.** A network already varies by address family; it
now also varies by what it can reach, and a workload portable between two networks is
portable only if both were declared the same way.

## Alternatives

**A gateway object attached to a network.** The familiar shape from other clouds: an
internet gateway created and associated. Rejected because it makes a consumer create and
wire an object to express one boolean, and the association carries no configuration that
the network could not carry directly. It becomes worth revisiting if egress ever needs
properties of its own — a bandwidth tier, a dedicated address pool — that a field cannot
hold.

**A per-interface setting only.** More precise, and it defeats the common case: a consumer
wanting internet access for a workload would set it on every interface of every instance.
A network-level declaration with a per-interface override later is the same expressiveness
with a usable default.

**An existing per-attachment egress policy type.** A type of this shape exists in the fabric
API group and is wired to nothing. Its vocabulary is fabric identities a consumer never
sees, which makes it a plausible internal representation of this decision and not a
consumer-facing API. Whether it is revived for that purpose or replaced is an open question
below.

**Giving networks real public addresses instead.** No translation, no shared address, no
capacity coupling. It requires far more public address space than translation does and a
different security posture, and it does not remove the need for this field — a network would
still declare whether it reaches the internet.

## Open Questions

1. **Does `reach: [IPv4]` oblige the platform to provide the resolver?** If a consumer must
   configure their own, the field promises reachability the platform does not deliver.
2. **Is a per-interface override in scope for the first version?** It is the one part of
   this that the data plane cannot honour today.
3. **Default `Enabled` or `Disabled` at launch?** `Enabled` is the useful default and is
   unsafe until egress is rate limited and attributable.
4. **Is the existing per-attachment egress policy type revived as the internal
   representation, or replaced?** It is described as superseded, but nothing supersedes it.
5. **What does a consumer see when shared capacity is exhausted?** Today, failed connections
   and no explanation. Answering this needs per-network accounting that does not exist.

## References

- [A network in every location it is used](network-in-every-location.md) — the presence
  this reports per-location egress on
- [A network interface a workload can be handed](network-interfaces.md) — the interface and
  attachment contract this extends
