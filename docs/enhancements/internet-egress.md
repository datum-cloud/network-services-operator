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
  - [One shard per node](#one-shard-per-node)
  - [Reporting per interface](#reporting-per-interface)
  - [Realizing egress in a cell](#realizing-egress-in-a-cell)
  - [The cell contract](#the-cell-contract)
  - [Reporting failure](#reporting-failure)
  - [Changing egress on a live network](#changing-egress-on-a-live-network)
  - [Reserving the interface field](#reserving-the-interface-field)
- [Dependencies](#dependencies)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
- [Open questions](#open-questions)
- [References](#references)

## Summary

This document proposes **internet egress** for a network. Internet egress is outbound
traffic from instances on a network to destinations outside the platform.

A network holds private addresses. Networks are addressed from unique local address space by
default, and two networks may hold the same prefix, so reaching the internet means
translation rather than routing. No field lets a consumer declare that instances on the
network reach the internet. Every network carries IPv6 by default, and an instance holding
only an IPv6 address cannot reach an IPv4-only destination.

This design adds one field group to the network resource. A consumer declares whether
instances reach the internet and which address families those instances reach. The platform
translates on the node that runs the instance and reports the resulting source address on the
instance's interface.

**Audience:** engineers working on the network services operator, the compute service, and
the network data plane. This document assumes you understand networks, network contexts,
and network interfaces.

**Terminology:** *address translation* rewrites the source address of an outbound packet so
that replies return to the platform. An *egress address* is the source address that
translation writes. An *egress shard* is the data-plane component on a node that performs
translation for the instances on that node. An *address class* is a named policy that the
addressing service allocates addresses from.

## Motivation

Three problems motivate this design.

**An IPv6-only instance reaches almost nothing.** A network defaults to IPv6, and the
platform rejects an IPv4-only network. The default instance therefore holds a private IPv6
address and no path to an IPv4 destination. Most services that a workload calls, such as
package registries and payment APIs, still accept only IPv4. Phase one does not solve this
problem: it delivers IPv6-to-IPv6 egress, and an instance still cannot reach an IPv4-only
destination until phase two adds `reach: [IPv4]`.

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
- Make egress live when an instance attaches, with no window in which the instance runs
  without the path it declared.
- Keep the consumer's declaration correct when the platform later changes where translation
  runs.
- Fix the interface-level default now, so that adding per-interface control later does not
  change what an existing interface means.

### Non-goals

- **Reaching IPv4 destinations.** Phase one validates only `reach: [IPv6]`. Widening `reach`
  to accept `IPv4` is phase two, once the platform pairs a resolver with the translator and
  runs a pooled translation tier for IPv4 addresses.
- **Filtering destinations.** This design decides whether a network reaches the internet.
  Security groups decide which destinations a network reaches.
- **Inbound reachability.** External addresses on an interface handle inbound traffic. This
  design covers outbound traffic only.
- **A dedicated egress address.** An address that belongs to one network and persists is a
  later stage, and it needs a translation tier that this design does not build. This design
  reserves the field that would report it.
- **Controlling egress per interface.** The first phase decides internet access for a whole
  network. A later phase decides it per interface. [Reserving the interface
  field](#reserving-the-interface-field) explains what ships now.
- **Surviving the loss of the translating node.** Translation runs on the node that runs the
  instance, so the node's failure takes the instance down with its egress. There is no
  failover to design.
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
      reach: [IPv6]
```

**A network reaches no internet destination until a consumer enables egress.** `mode`
defaults to `Disabled`, so egress is a capability a consumer opts into rather than one they
discover. An outbound path that a consumer never asked for is one nobody is accountable for.

Instances on a network with egress enabled reach IPv6 destinations. Phase one stops there;
phase two lets those instances reach IPv4 destinations through translation that the consumer
never configures. The consumer creates no gateway resource, writes no route, requests no
address, and names no class.

The declaration applies to every instance on the network. A consumer cannot enable internet
access for one instance and disable it for another in the first phase.

### Naming destinations instead of mechanisms

The `reach` field names destination address families. The field does not name a translation
mechanism.

Setting `reach: [IPv4]` on an IPv6 network will state that instances call IPv4-only
services, once phase two admits the value. The platform will answer that request with
address translation and with a resolver that returns synthesized addresses for names that
publish no IPv6 record. A consumer will not be able to act on the difference between
IPv6-to-IPv6 and IPv6-to-IPv4 translation, so the API will not expose that difference.

Naming destinations also keeps the field accurate across future changes. The platform can
replace a translation mechanism, move translation off the node onto a shared tier, or assign
real IPv4 addresses instead of translating. Each change alters platform behavior and leaves
the consumer's declaration correct.

### Reporting the egress address

The interface reports the egress address and reports how far a consumer may rely on it:

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: NetworkInterface
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

The address lives on the interface because translation runs on the node that runs the
instance. An interface holds one node for its life, so its address holds for exactly as long
as the interface does. A network holds many interfaces on many nodes, and reporting one
address on the network would present one node's answer as the network's.

No value this design ships reports `stability: Network`. Producing it needs an address that
follows a network across nodes, which is a different translation tier. The value is defined
now so that adding that tier changes a reported value and no field.

The status omits facts about how the platform delivers egress, including how the platform
partitions translation state and which other networks share the path. A consumer cannot act
on those facts. Some of those facts describe other consumers.

### Constraints and caveats

**The first stage shares one egress address per node.** One address serves every network
whose instances run on the same node. Two instances of one network on two nodes leave
through two addresses, and an instance that reschedules leaves through a new one. The
`stability` field reports this constraint rather than hiding it.

**Networks share translation capacity per node.** Translation state and port space are
finite, and the first stage shares both among the networks on a node. A network that opens
many connections degrades another network on the same node, and only that node. No
per-network signal tells the affected consumer why connections failed. This design names the
gap and does not close it. A fabricated limit field would mislead consumers more than an
absent field does.

**Reaching IPv4 destinations by name requires a matching resolver.** Synthesized addresses
work only when the resolver and the translator share a prefix. Instances that use their own
resolver do not reach IPv4 destinations by name.

**Reaching IPv4 destinations costs one public IPv4 address per node.** Translation per node
means a public IPv4 address per node. That is the cost model a pooled tier exists to avoid,
so IPv4 translation stays in the lab until that tier exists, and phase two depends on it.

## Design details

### One shard per node

Every compute node runs an egress shard, and the instances on a node leave through that
node's shard. The shard is the data-plane resource that translates outbound packets, and it
is already deployed on every compute node; this design uses it in place rather than
selecting among shards.

Placing translation on the node gives three properties that a shared tier does not:

- **No cross-node dependency.** The route from an instance to its shard never leaves the
  node. A shard elsewhere failing cannot strand this node's instances, and the only component
  whose failure removes egress is the one whose failure already removes the instance.
- **A smaller blast radius.** A network that exhausts translation capacity exhausts one
  node's capacity.
- **A simpler data path.** Encapsulation toward a remote shard exists to reach translation
  elsewhere. A shard on the node needs none of it.

The cost is that the egress address belongs to the node, which [Reporting the egress
address](#reporting-the-egress-address) states to the consumer, and that an address that
follows a network needs a different tier. [Alternatives](#alternatives) covers the design
that selected shards through a class, and why this design replaces it.

### Reporting per interface

The platform realizes egress per node, so a network present on two nodes receives two egress
addresses. The network holds the declaration, and each interface reports the result for the
node it attached to. The network context reports whether egress works in its location and
reports the DNS64 prefix, which is a fact about the location, and reports no address.

### Realizing egress in a cell

A cell realizes egress with four kinds. Two exist today and two change shape.

```
  infra                cell controller                    node
    |                        |                              |
    +-- EgressShard ---------|----------------------------->|  shard programs translation
    |   (one per node)       |                              |  installer records shard SID
    |                        |                              |
    |                        +-- NetworkContext.mode ------>|  attachment stanza: Enabled
    |                        |                              |
    |                        |         CNI ADD:  route to this node's shard, or none
    |                        |                              |
    |                        +-- EgressShardClaim <---------+  attachment reports its node
    |                        |   (one per attachment)
    |                        |
    |                        +-- attachment status.egress: shard's address
```

**An `EgressShard` names the node it runs on and carries the shard's identity.** Its spec
holds the node reference, the data-plane identifier the node's instances encapsulate toward,
and the egress address that translation writes. The shard programs its data plane from that
spec and reports readiness in status. In the first stage an operator writes one object per
compute node in the infrastructure repository. A controller deriving the object from the node
and its router is the intended follow-on, and [Open questions](#open-questions) tracks it.

**The node learns its own shard.** The long-running node agent reads the `EgressShard`
whose node reference names its node and records that shard's identifier in the node's
configuration. The plugin that attaches an instance reads that configuration at attach time
and never queries the control plane. The agent re-reads the object on its existing sweep, so
a replaced shard is picked up without a node restart.

**The attachment carries the declaration, not the shard.** The stanza the infrastructure
provider renders for an attachment carries `egress.internet.mode` and nothing else about
egress. The node combines that declaration with its own shard: `Enabled` installs a route
for the network's VRF toward the node's shard, `Disabled` or an absent stanza installs none,
and `Enabled` on a node with no shard fails the attach. Failing is deliberate. A node without
a shard is an infrastructure error, and failing the first instance surfaces it where a
successful attach without egress would hide it.

**Egress is live when the attach completes.** Every input the node needs is present before
the plugin runs: the shard identifier in node configuration and the declaration in the
attachment stanza. Nothing waits on a controller.

**An `EgressShardClaim` records the binding, one per attachment.** Once the attachment
reports the node it landed on, the cell controller writes a claim naming the attachment, the
node, and the address families the network declared. The claim's binder resolves the
`EgressShard` on that node and records it in status. The claim decides nothing: the node
already installed the route from the same object. It exists so that the binding is readable,
so that a node without a ready shard produces a condition a consumer can see, and so that a
later tier that does select among shards binds through the same object.

**A shard holds no list of the networks it serves.** The node stamps an identifier that each
packet carries, so a shard needs no notification when a network attaches or detaches.

**No network claims an egress address.** One address serves every network on a shard,
because per-network blocks exhaust a public aggregate long before networks exhaust it. The
address belongs to the shard, outlives every network using it, and survives a restart. The
cell controller claims that address, not the shard: a shard runs on every compute node, and
making it an addressing-service client would place platform credentials on every node. In
the first stage an operator may supply the address in the shard's spec instead.

### The cell contract

The contract between the three components is small enough to state as a table.

| Resource | Written by | Read by |
|---|---|---|
| `Network.spec.egress` | consumer | network services operator |
| `NetworkContext.spec.egress.internet.mode` | network services operator | infrastructure provider |
| attachment stanza `egress.internet.mode` | infrastructure provider | node, at attach |
| `EgressShard.spec` | operator (first stage) | shard, node agent, claim binder |
| `EgressShard.status` | shard | claim binder |
| attachment `status.node` | infrastructure provider, from the node's advertisement | claim binder |
| `EgressShardClaim` | infrastructure provider | infrastructure provider, network services operator |
| attachment `status.egress` | infrastructure provider, from the bound shard | network services operator |
| `NetworkInterface.status.egress` | network services operator | consumer, compute |

Two rules hold the contract together. `EgressShard.spec` has exactly one writer, and the
shard writes only status. The attachment stanza is the only channel from the control plane to
the attaching plugin, and it carries a declaration, never a shard identity.

### Reporting failure

The interface carries an `InternetEgressReady` condition, with four reasons:

- `Ready`: this instance reaches the declared destinations.
- `AddressUnavailable`: the shard on this node has no egress address.
- `Unavailable`: no shard on this node is ready.
- `Degraded`: egress works for some declared families and not for others.

The network context carries the same condition as a summary: `Ready` when every interface in
the location is `Ready`, and the most severe interface reason otherwise.

`Degraded` needs a network that declares two families to distinguish partial failure from
total failure. Phase one validates only `reach: [IPv6]`, so nothing reports `Degraded` until
phase two lets a network declare `IPv4` alongside `IPv6`. Its absence now is expected, not a
bug.

Each reason states a fact about the consumer's instance. The condition omits the specific
cause, such as the failing component or allocation, because a consumer cannot act on those
causes. Operator events carry the specific cause.

### Changing egress on a live network

Switching `mode` on a network with running instances re-renders each attachment's stanza.
The plugin that reads the stanza runs only when an instance attaches, so the change reaches a
running instance through the node agent's existing sweep, which re-resolves each VRF's egress
route on a fixed interval. That sweep reads node configuration today and must also read the
per-attachment declaration, which [Dependencies](#dependencies) lists.

Until the sweep reads the declaration, a change takes effect on interfaces that attach
afterwards, and a running instance keeps the route it attached with. The window is bounded by
the sweep interval once the dependency lands.

### Reserving the interface field

`NetworkInterfaceClaimSpec` and `NetworkInterfaceSpec` gain `egress.internet.mode`, and
validation accepts only `Inherit`, which follows the network's declaration. Reserving the
field settles the default before consumers depend on it: an interface written today records
`Inherit`, so accepting `Enabled` and `Disabled` later changes no existing interface.
Widening the accepted values depends on per-interface routing, which no component
implements.

## Dependencies

This design depends on six items that it does not deliver:

1. **A public address class.** The platform must allocate public address space, and the
   underlay must route each shard's address to its node, before egress ships outside a lab.
   No component allocates the space, and the lab routes the address by hand.
2. **A node that translates its own traffic.** The data plane today refuses to route a VRF
   toward a shard on the same node, and the shard's translation program sees only traffic
   arriving from the fabric. Both must change for a node's instances to use the node's shard.
   This is the one dependency without which nothing in this design runs.
3. **A shard configured from its object.** The shard reads its identity from process
   configuration today and echoes it into status. It must read spec, so that the object an
   operator writes is the object the shard runs.
4. **A node agent that reads its shard and the per-attachment declaration.** The agent
   derives the node's shard list from a deployment-time setting today, and its sweep
   re-resolves every VRF from that list. It must derive the list from the node's
   `EgressShard`, and the sweep must honor each attachment's `mode`, or
   [changing egress on a live network](#changing-egress-on-a-live-network) has no effect.
5. **A resolver that matches the translator.** The platform must pair both before it accepts
   `reach: [IPv4]`. Until then, validation on the network and the network context refuses the
   value, with the message "Only IPv6 is accepted; reaching IPv4 destinations needs a
   resolver and a translator sharing a prefix, and the platform pairs neither."
6. **Rate limiting and per-network attribution.** Both block launch independently of this
   API. The absence of attribution is why this design cannot report the capacity gap.

## Drawbacks

**The safe default is not the useful one.** An IPv6-only network without egress reaches
nothing, so a consumer who wants the ordinary case must write a field to get it. The
platform accepts that cost: defaulting to `Enabled` would open an outbound path on every
network that anyone creates, before the platform can rate limit that path or attribute what
leaves it.

**The API promises less than its name suggests.** A field named `egress.internet` reads as a
guarantee of reachability. The first version delivers a shared path with no capacity
guarantee. The `stability` field carries that caveat, and it carries the caveat in a status
field that a consumer may not read.

**The address follows the node.** A network with instances on ten nodes has ten egress
addresses, and an instance that reschedules changes its address. A consumer who reads the
address on one interface and assumes it holds for the network is wrong, and the design can
only say so through `stability: None` and by placing the address on the interface rather than
the network.

**Networks gain a second axis of variation.** A network already varies by address family. A
network now also varies by what it reaches. A workload moves between two networks only when
a consumer declared both networks the same way.

## Alternatives

**Select shards through a class.** An earlier draft of this design defined an
`InternetEgressClass` that named a controller, a sharing policy, and parameters, and bound
each network to a shard the class selected. With translation on every node, the class has
nothing left to select and no sharing policy it can honor, and binding a network to one shard
contradicts a network whose instances run on many. The claim survives from that draft as a
record rather than a decision, so a pooled tier that does select among shards can return to
this shape without a consumer-facing change.

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

**Carry the shard identity in the attachment.** The infrastructure provider could name the
shard in each attachment's stanza instead of the node resolving its own. The attachment does
not know its node until after it attaches, so the stanza would have to carry every shard in
the cell and let the node pick, and every shard change would re-render every attachment.
Node configuration is the right place for a fact about the node.

**Assign public addresses to networks instead of translating.** Direct assignment removes
translation, the shared address, and the capacity coupling. Direct assignment also requires
far more public address space and a different security posture, and it does not fit networks
that hold unique local space by default. Direct assignment does not remove the need for this
field, because a network still declares whether it reaches the internet.

## Open questions

1. **Does `reach: [IPv4]` oblige the platform to provide the resolver?** If a consumer must
   configure their own resolver, the field promises reachability that the platform does not
   deliver.
2. **Should the cell controller create each node's `EgressShard`?** The shard's identifier is
   derivable from the node's router, and its address is an allocation the controller already
   performs. Creating the object from a node watch removes the per-node file from the
   infrastructure repository and the chance that the file and the shard's process
   configuration disagree. This document recommends it and does not decide it.
3. **Who originates a shard's address into the underlay?** The shard advertises its address
   inside the fabric. A reply from the internet arrives from outside it, and something must
   attract the address to the node. The lab configures that by hand.
4. **What identifies an interface to the data plane when per-interface control ships?** The
   node installs one route per network, and a per-interface route needs an identifier that
   the attachment already carries.
5. **What does a consumer read when a node's capacity is exhausted?** Today a consumer reads
   nothing and observes failed connections. Answering this question requires per-network
   accounting that no component performs.

## References

- [A network in every location it is used](network-in-every-location.md): the per-location
  presence that reports whether egress works in a location
- [A network interface a workload can be handed](network-interfaces.md): the interface that
  reports the egress address
- [Cell controller manager](cell-controller-manager.md): the controller that claims egress
  addresses and records each attachment's shard
- [Internet NAT66 gateway for Galactic VPC](https://github.com/datum-cloud/enhancements/issues/865):
  the data-plane work this design consumes; this document is its control-plane design
- [NAT64 gateway for VPC networks](https://github.com/datum-cloud/enhancements/pull/879): the
  data-plane design for phase two, and the shard object's field list
- [Drive shard identity from the shard object](https://github.com/datum-cloud/galactic/issues/581):
  dependency 3
- [Keep each VRF on the first reachable egress shard](https://github.com/datum-cloud/galactic/pull/593):
  the node sweep that dependency 4 extends
