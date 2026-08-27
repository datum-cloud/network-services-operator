---
status: provisional
stage: alpha
latest-milestone: "v0.x"
---

# An identity the fabric knows a network by

- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [What the fabric consumes](#what-the-fabric-consumes)
  - [What it feels like](#what-it-feels-like)
- [Design Details](#design-details)
  - [Thirty-two bits, not sixty-four](#thirty-two-bits-not-sixty-four)
  - [Allocating an integer from a prefix allocator](#allocating-an-integer-from-a-prefix-allocator)
  - [Reaching the data plane](#reaching-the-data-plane)
  - [Lifecycle](#lifecycle)
- [What this depends on](#what-this-depends-on)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
- [Open Questions](#open-questions)

## Summary

A network on the fabric exists as one forwarding instance per PoP. What makes those
instances the same network is a **BGP Route Target**, and the Route Target is derived from a
per-network identifier that nothing on the platform allocates.

This proposes that the platform allocate that identifier, once per network, and surface it
where every PoP can read it.

This is deliberately not a locator, not a Node-ID, and not a per-PoP VRF instance. Those are
separate allocations with different scopes, and one of them is correctly node-local. This
document covers only the identity that has to be the same everywhere.

## Motivation

**Nothing allocates it.** The fabric derives a network's Route Target as
`ASN:<identifier>`, and takes the identifier from the CNI configuration written for each
attachment. Today that value is a literal typed into a NetworkAttachmentDefinition. The
controller that used to mint one was removed along with the CRDs that held it. There is no
allocator, no registry, and no uniqueness check of any kind.

**Two networks that collide are one network.** The Route Target is what makes a PoP import
another PoP's routes into the right forwarding instance. Two networks sharing an identifier
do not fail closed. They merge: each imports the other's prefixes, and tenant traffic
crosses between them.

**The identifier is narrower than it looks.** The fabric truncates it to 32 bits when
building the Route Target. A 48-bit value that is unique across the platform is not
sufficient. What must be unique is the low 32 bits, and nothing today says so.

### Goals

- One identifier per network, unique platform-wide in the bits the fabric actually uses,
  stable for the network's life, the same in every PoP the network reaches.
- Readable by each PoP from a resource its cell already receives.
- Consumers do nothing. They create a network; they never see an identifier.

### Non-Goals

- **The per-PoP uSID locator block and per-node Node-ID.** These are the fabric's routing
  anchors and today they are hand-assigned with no registry, which is worth fixing. It is a
  different allocation, at a different scope, and it belongs in its own proposal.
- **The per-PoP VRF instance identifier.** The fabric allocates this on the node at
  attachment time, and that is correct: a packet only reaches the instance lookup after the
  locator has already steered it to that node, so the value is disambiguated upstream.
  Centralising it would add coordination that buys nothing.
- **The network's address space.** A network's tenant prefix is a separate per-network
  allocation, already named in the API and still unallocated. It is not this.

## Proposal

### What the fabric consumes

Three things identify a network's forwarding state, at three different scopes. Only one of
them has to be the same everywhere.

| What | Scope | Who should own it |
|---|---|---|
| Locator block, Node-ID | per PoP, per node | an allocator, in a separate proposal |
| VRF instance | per network per node | the fabric, on the node |
| **Route Target identifier** | **per network, platform-wide** | **this proposal** |

### What it feels like

A consumer creates a network. Nothing they write mentions the fabric.

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: Network
metadata:
  name: prod
spec:
  ipFamilies: [IPv6]
```

The platform allocates an identifier and reports it.

```yaml
status:
  fabricIdentity: 305419896
  conditions:
    - type: Allocated
      status: "True"
```

Place that network in two PoPs and both derive the same Route Target from that number. That
is what makes it one network rather than two that share a name.

## Design Details

### Thirty-two bits, not sixty-four

The identifier is a 32-bit unsigned integer, because that is exactly what survives into the
Route Target. Allocating anything wider invites a value whose uniqueness is real in the API
and absent in the fabric.

It is surfaced as an integer, not as a prefix or an encoded string. The consumer of this
value builds `ASN:<identifier>`, and asking it to parse an address to recover a number would
be a worse contract with no upside.

Value `0` is not allocated, so an unset field and a real allocation never read alike.

### Allocating an integer from a prefix allocator

The platform's address management service allocates prefixes, not integers. Rather than
build a second allocator with the same uniqueness and concurrency problems already solved
there, an identifier is allocated as a prefix from a pool that is never routed, and the
integer is the block's index within that pool.

A `/32` root pool handing out `/64`s yields exactly 2^32 allocations, whose distinguishing
bits are exactly the 32 the fabric uses. The mapping is total and order-preserving in both
directions.

This buys uniqueness, exhaustion accounting, quota, retention and an audit trail for free,
and costs address space that is never routed and never reachable. The pool must be
described as an identifier space so nobody later reads it as addressing.

The API surfaces the integer. The prefix is an implementation detail of the allocator and
does not appear in the API.

### Reaching the data plane

`Network.status` holds the authoritative allocation. It cannot be what a PoP reads: cells
cannot reach project control planes, and federation strips status on propagation.

The presence controller projects the identifier into `NetworkContext.spec`, alongside the
address families and MTU already projected there. A cell reads one object and has what it
needs.

Because the identifier is allocated into status, the network's generation does not advance
when it lands. The context is rewritten when the allocation appears, so the projected
generation still answers whether a PoP has caught up with the network's spec, and the
identifier being present answers whether it has caught up with the allocation.

### Lifecycle

The identifier is immutable once allocated. The fabric embeds it in import policy across
every PoP the network reaches, so a network that changed identity would be a different
network to everything already carrying its traffic.

A released identifier is not reissued. A Route Target still installed in a remote PoP's
import policy would silently merge a new network into a dead one's routes. Retention is the
safe failure, and it is unbounded today because the allocator accepts a retention lease and
does not enforce it.

## What this depends on

- **The projection path**, already carrying address families and MTU to cells.
- **The per-project client** for the address management service, already used for endpoint
  addressing.
- **A platform-owned tenancy** in that service. The platform allocates this on a consumer's
  behalf, so it must not be gated on the consumer enabling the service, and must not consume
  their claim budget. Without it, uniqueness is per project, which is not uniqueness.

  The address management service has no platform scope of its own today. Until it does, the
  operator addresses one project control plane the platform owns and allocates every
  network's identity there. That is one pool and one allocator for the whole platform, which
  is what the uniqueness actually rests on; what it does not yet get is a tenancy the service
  itself understands as the platform's. The seam is a single method on the client factory, so
  when the service grows one, nothing above that line changes.
- **The fabric accepting an assigned identifier.** It has no field for one today.

## Drawbacks

Network creation gains a dependency on the allocator. Today it cannot fail this way, because
the identifier is not allocated at all.

Allocating an integer as a prefix reads as a hack to anyone who finds the pool without the
explanation.

Thirty-two bits is a ceiling. It is the fabric's ceiling rather than one this introduces, but
it is now a platform-wide one, and exhaustion has no answer beyond widening the Route Target
format.

## Alternatives

**Derive it from the network UID.** No allocator and no failure mode, but 32 bits of a UID
collides by birthday at a few tens of thousands of networks, undetectably, with no way to
reserve or repair.

**Let each PoP keep choosing.** This is the status quo and it does not survive a network
reaching a second PoP.

**Allocate 48 bits to match the field the fabric parses.** The fabric truncates to 32 on the
way into the Route Target, so the extra bits are uniqueness the platform believes it has and
does not.

## Open Questions

- Should the fabric stop truncating instead, and carry a wider identifier in a four-byte
  Route Target? That reverses this proposal's central constraint, so it is worth answering
  before building.
- What is the exhaustion story at 32 bits, and what reclaims identifiers from deleted
  networks given retention is currently permanent?
- Should the network's tenant prefix be allocated in the same change? It is the other
  per-network global allocation and the field already exists.
- Does anything other than the Route Target need this identifier, and if so does it need the
  same width?
- **How do networks that already exist get one?** Every network on the platform today carries a
  per-location identifier chosen where it was placed. Allocating one identity for them is not
  a field being filled in: it renames the edge VRF device and moves the Route Target on live
  traffic. Nothing here attempts it, and a network that holds no identity behaves exactly as
  it does now. The migration needs its own proposal.
