---
status: provisional
stage: alpha
latest-milestone: "v0.x"
---

# A network interface a workload can be handed

- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [What it feels like](#what-it-feels-like)
  - [Notes/Constraints/Caveats](#notesconstraintscaveats)
- [Design Details](#design-details)
  - [NetworkInterfaceClaim](#networkinterfaceclaim)
  - [NetworkInterface](#networkinterface)
  - [Binding](#binding)
  - [Fulfilling a claim](#fulfilling-a-claim)
  - [Reaching the data plane](#reaching-the-data-plane)
  - [What compute writes](#what-compute-writes)
  - [A workload in two locations](#a-workload-in-two-locations)
- [What this depends on](#what-this-depends-on)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
- [Open Questions](#open-questions)
- [References](#references)

## Summary

Compute asks for a network interface the way a pod asks for storage: it writes a
**`NetworkInterfaceClaim`** naming a network and what the interface needs, and NSO binds it
to a **`NetworkInterface`** carrying everything required to configure a NIC — addresses,
gateway, MTU, and the data-plane identity behind them.

Two properties follow from that shape. **Compute stops reaching into networking
internals**: it no longer creates `NetworkBinding` and `SubnetClaim` objects, and no longer
needs to know that a `NetworkContext` exists. And **an interface becomes a thing that
outlives the instance using it**, which is what makes a per-instance address, a retained
address, and an address a consumer can actually see all possible at once.

This document is written for the compute service, for infrastructure providers that
configure NICs, and for NSO, which fulfills claims. It defines the shape of the two
resources and the contract between the three.

## Motivation

The interface between compute and networking today is not an interface. It is compute
reaching through NSO's internals, and three problems come out of that.

**An address is shared where it should be per-instance.** `WorkloadDeploymentReconciler`
creates one `SubnetClaim` per deployment and every instance in that deployment draws from
it. The code says so in a `TODO`. Instances in a deployment cannot have distinct addresses
because nothing per-instance exists to hold one.

**Nobody can see their address.** `Instance.status.networkInterfaces[].assignments.networkIP`
is declared, printed as a column, and never written. A consumer running
`kubectl get instance` sees an empty field where the answer should be.

**Every provider re-derives the same facts.** A provider configuring a NIC needs the
address, the gateway, the MTU, and which network it belongs to. Today it walks
`NetworkBinding` → `NetworkContext` → `SubnetClaim` → `Subnet` to assemble them, which
means NSO cannot change those objects without breaking every provider, and a new provider
must learn all four before it can bring up one interface.

All three are the same missing noun. There is no object that means *this instance's
interface on this network in this location*, so the facts about it live scattered across
the objects that happen to produce them.

[Compute PR #210](https://github.com/datum-cloud/compute/pull/210) settles where an address
comes from. This document settles what holds it.

### Goals

- Give compute one resource to create and one resource to read, with no knowledge of how
  NSO satisfies it.
- Give every instance its own interface, and every interface its own addresses.
- Give infrastructure providers a single resource carrying everything needed to configure
  a NIC.
- Let an interface — and therefore its addresses — survive the instance that used it, so a
  replacement instance comes back to the same address.
- Report allocation and programming separately, so an instance is never told its network
  is ready before it can carry traffic.
- Let NSO change how it allocates, and what it allocates from, without a compute release.

### Non-Goals

- **Choosing addresses.** Which address an interface gets, out of what space, under what
  policy, is [compute PR #210](https://github.com/datum-cloud/compute/pull/210). This
  document consumes that answer and never re-decides it.
- **Programming the data plane.** A `NetworkInterface` states what an interface must be;
  `VPCAttachment` and the agents behind it make it so.
- **Retiring `NetworkBinding`, `NetworkContext`, `SubnetClaim`, or `Subnet`.** They remain
  NSO's internals and keep doing what they do. What changes is that nothing outside NSO
  writes them.
- **Interface-level network policy.** `InstanceNetworkInterface.networkPolicy` stays where
  it is, on the compute side, and is not part of this contract.
- **Multiple interfaces per instance beyond the first.** The model is a list and carries
  more than one without changes; whether compute exposes that at launch is a compute
  product decision, not an API constraint here.
- **Interfaces for anything other than an instance.** Load balancers and gateways also
  attach to networks, and the same pair of resources should serve them. Nothing here
  assumes an instance, but nothing here is designed against a second consumer either.
- **Hot-attach and hot-detach.** Adding or removing an interface on a running instance is
  additive to this model and out of scope.

## Proposal

Compute creates a claim. NSO binds an interface to it. Providers read the interface.
Nobody reads anything else.

### What it feels like

A consumer writes the same workload compute PR #210 describes — a network, a list of
families, and optionally a class of extra address:

```yaml
apiVersion: compute.datumapis.com/v1alpha
kind: Workload
metadata:
  name: hello-sandbox
spec:
  template:
    spec:
      runtime:
        sandbox:
          containers:
            - name: app
              image: ghcr.io/datum-cloud/hello-unikraft:latest
      networkInterfaces:
        - name: eth0
          network:
            name: default
          ipFamilies:
            - IPv6
            - IPv4
          reclaimPolicy: Retain
          addresses:
            - class: public-unicast-ipv4
  placements:
    - name: default
      locations:
        - us-central-1
      scaleSettings:
        minReplicas: 2
```

Compute turns that into one claim per instance, per interface, in the cell the deployment
landed at:

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: NetworkInterfaceClaim
metadata:
  # Derived from the slot and the interface. Stable across every instance that
  # ever fills this slot.
  name: hello-sandbox-default-us-central-1-0-eth0
  ownerReferences:
    - kind: Instance
      name: hello-sandbox-default-us-central-1-0
spec:
  network:
    name: default
  interfaceName: eth0
  ipFamilies:
    - IPv6
    - IPv4
  reclaimPolicy: Retain
  addresses:
    - class: public-unicast-ipv4
```

NSO binds it, and the claim reports the answer:

```console
$ kubectl get networkinterfaceclaim hello-sandbox-default-us-central-1-0-eth0 -o yaml
status:
  networkInterfaceRef:
    name: nic-4f2a9c1e
  addresses:
    - family: IPv6   address: fd20:a1b:2c3d:1:0:1::/96   primary: true
    - family: IPv4   address: 10.128.0.2/32
  externalAddresses:
    - family: IPv4   address: 198.51.100.11   class: public-unicast-ipv4
  conditions:
    - type: Bound       status: "True"
    - type: Allocated   status: "True"
    - type: Programmed  status: "True"
    - type: Ready       status: "True"
```

The addresses appear on the claim as well as on the interface, deliberately. Compute
watches one object per interface and never needs a second read to answer "what address did
this instance get" — the same reason a `PersistentVolumeClaim` reports its own capacity.

A provider bringing up the NIC reads the interface, and reads nothing else:

```console
$ kubectl get networkinterface nic-4f2a9c1e -o yaml
spec:
  network:
    name: default
  claimRef:
    name: hello-sandbox-default-us-central-1-0-eth0
    uid: 8c1d…
  interfaceName: eth0
  mtu: 1460
  reclaimPolicy: Retain
  addresses:
    - family: IPv6   address: fd20:a1b:2c3d:1:0:1::/96   gateway: fd20:a1b:2c3d:1::1   primary: true
    - family: IPv4   address: 10.128.0.2/32              gateway: 10.128.0.1
  externalAddresses:
    - family: IPv4   address: 198.51.100.11   class: public-unicast-ipv4
status:
  phase: Bound
  networkContextRef:
    name: default-us-central-1
  attachmentRef:
    apiGroup: cloud.datumapis.com
    kind: VPCAttachment
    name: nic-4f2a9c1e
  vpc: 3kF9qP2x
  conditions:
    - type: Allocated   status: "True"
    - type: Programmed  status: "True"
```

Everything a NIC needs is in `spec`. Nothing in `spec` requires a second lookup to
interpret. The `NetworkContext` a provider used to have to find appears in `status` as a
breadcrumb for operators — nothing reads it to do its job.

### Notes/Constraints/Caveats

- **The claim is the durable identity, not the instance.** Its name derives from the slot,
  so a replacement instance finds a claim that already holds its addresses.
- **A claim binds exactly one interface, and an interface binds exactly one claim.** There
  is no fan-out and no re-matching.
- **Consumers never write either resource.** Compute writes claims on their behalf; NSO
  writes interfaces.
- **`Allocated` and `Programmed` are separate, and `Ready` requires both.** Allocation is
  synchronous, programming is not, and an instance released on allocation alone comes up
  before its packets can move.
- **Both resources live in the cell the deployment landed at.** Location is implicit in
  where the claim exists; nobody writes it down.
- **The addresses on a claim's status are a copy, and the interface is the source of
  truth.** They are written in the same reconcile that sets `Bound`.
- **A retained interface returns to `Available`, not to a dead end.** Compute PR #210 is
  explicit that the storage `Released` state — where an operator must clear a stale
  reference by hand — must not be copied, and it is not.

## Design Details

### NetworkInterfaceClaim

A claim states what an interface must be able to do. Every field is intent; none of it
describes a result.

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: NetworkInterfaceClaim
spec:
  # The network this interface attaches to. A LocalNetworkRef — a claim and its
  # network are always in the same namespace.
  # Required. Immutable: an interface that changed network after allocation
  # holds addresses from a space it no longer belongs to.
  network:
    name: default

  # The name the interface presents to the guest. Defaults to eth0. Immutable.
  interfaceName: eth0

  # The address families this interface must carry, in priority order. The first
  # is the interface's primary address. Defaults to [IPv6] — the platform is
  # IPv6-first.
  #
  # All requested families must be satisfiable, or the claim does not bind. A
  # partially-addressed interface is not published. A family the network does
  # not carry is a validation failure, not a pending condition.
  ipFamilies:
    - IPv6
    - IPv4

  # Additional addresses beyond the network-internal ones, each named by class.
  # A class is an IPAM concept: the consumer names a kind of address, never a
  # pool, a prefix length, or a CIDR. Omitted entirely for ordinary private
  # addressing, which is the common case.
  addresses:
    - class: public-unicast-ipv4

  # What becomes of the bound interface when this claim is deleted.
  #   Delete — the interface is deleted and its addresses released.
  #   Retain — the interface survives, unbound, still holding its addresses,
  #            waiting for a claim of this name to come back.
  # Defaults to Delete. The IP class may set a different default; this overrides
  # it.
  reclaimPolicy: Retain

  # Optional. Binds a specific existing interface by name, the part
  # PersistentVolumeClaim.volumeName plays for storage. Left empty — the normal
  # case — NSO chooses or creates one.
  networkInterfaceName: ""
```

Status carries the binding, a copy of the result, and the conditions:

```yaml
status:
  # Set once, when the claim binds. Never recomputed.
  networkInterfaceRef:
    name: nic-4f2a9c1e

  # Copied from the bound interface so a consumer reads one object.
  addresses: [...]
  externalAddresses: [...]

  conditions:
    # An interface has been bound to this claim.
    - type: Bound
    # Every requested family holds an address.
    - type: Allocated
    # The data plane can carry those addresses.
    - type: Programmed
    # Bound, Allocated, and Programmed are all true.
    - type: Ready
```

`Allocated` and `Programmed` are surfaced on the claim rather than left on the interface
because they are the two facts compute gates an instance on, and compute should not have to
hold a second watch to learn them. `SubnetClaim` already defaults exactly this trio of
conditions, so the pattern is NSO's own.

### NetworkInterface

An interface is a result. It is written by NSO, read by providers, and never authored by
hand outside of an operator repairing something.

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: NetworkInterface
spec:
  # The network this interface belongs to.
  network:
    name: default

  # The claim currently holding this interface. Empty when the interface is
  # retained and unbound. The uid is recorded so a claim deleted and recreated
  # under the same name is recognised as a different claim.
  claimRef:
    name: hello-sandbox-default-us-central-1-0-eth0
    uid: 8c1d…

  interfaceName: eth0

  # Resolved from Network.spec.mtu, so a provider never reads the network.
  mtu: 1460

  # The addresses inside the network. One entry per family, exactly one primary.
  # For IPv6 this is the endpoint's whole /96 block, not a single address; the
  # interface owns the block and assigns within it.
  addresses:
    - family: IPv6
      address: fd20:a1b:2c3d:1:0:1::/96
      gateway: fd20:a1b:2c3d:1::1
      primary: true
      class: tenant-endpoint-ipv6
    - family: IPv4
      address: 10.128.0.2/32
      gateway: 10.128.0.1
      class: tenant-endpoint-ipv4

  # Addresses reachable from outside the network, each mapped one-to-one onto
  # the interface's address of the same family.
  externalAddresses:
    - family: IPv4
      address: 198.51.100.11
      class: public-unicast-ipv4

  reclaimPolicy: Retain

status:
  # Available — allocated, holding addresses, bound to nothing.
  # Bound      — held by the claim in spec.claimRef.
  phase: Bound

  # The network's presence in this location, which NSO resolved or created while
  # fulfilling the claim. Recorded for operators; nothing depends on it.
  networkContextRef:
    name: default-us-central-1

  # The data-plane realization, once one exists.
  attachmentRef:
    apiGroup: cloud.datumapis.com
    kind: VPCAttachment
    name: nic-4f2a9c1e

  # Base62 VPC identifier, matching VPC.status.vpc. The fabric keys on this.
  vpc: 3kF9qP2x

  conditions:
    - type: Allocated
    - type: Programmed
```

**There is no `Released` phase.** A retained interface whose claim is deleted goes straight
back to `Available` with its addresses intact, and the next claim of the same name binds it.
Storage's `Released` state requires an operator to clear a stale reference before the volume
can be used again; an address held in a finite public range cannot wait on that. The `uid`
in `claimRef` is what makes this safe — it is the record that distinguishes the claim that
held the interface from the one asking for it now.

**Addresses live in `spec`, not `status`.** They are the desired configuration of a NIC,
and a provider that has to read `status` to know what to configure has no way to tell a
requested address from an observed one. `status` holds only what NSO observed: where the
network is, what realizes the interface, and whether programming succeeded.

### Binding

Binding follows storage, because storage's model is the one that survives a holder being
replaced.

**A claim binds once, at creation.** NSO either finds a retained interface whose name the
claim asks for, or allocates a new one. From then on the claim records the interface and
the interface records the claim, and nothing recomputes the pairing. There is no matching
pass, no window where an address is loose, and no way for a re-match to go wrong.

**The claim's name is the durable identity.** Compute derives it from the slot — workload,
placement, location, ordinal — and the interface name, all of which are stable across every
instance that ever fills that slot. A replacement instance creates a claim, finds the name
already exists, and adopts it.

**A claim ends when its slot does.** Deleting the workload deletes its claims through
ownership; scaling down deletes the claims of the slots it removes. `reclaimPolicy` then
decides what happens to the interface:

| Event | `Delete` | `Retain` |
|---|---|---|
| Instance replaced or rescheduled | claim survives — same interface, same addresses | same |
| Instance redeployed with a new template | claim survives — same interface, same addresses | same |
| Scale down then back up | new interface, new addresses | same interface, same addresses |
| Workload deleted then recreated | new interface, new addresses | same interface, same addresses |

The first two rows are the same in both columns and that is the point: **an interface
survives a redeploy on its own. Surviving a scale-down takes `Retain`.**

A retained interface still holds its addresses, still counts against its holder's budget,
and carries a lease — otherwise a public address sits out of service indefinitely with
nothing pressuring anyone to release it. Expiry, its duration, and operator force-release
are IPAM's to define; this resource only records the policy that triggers them.

### Fulfilling a claim

What NSO does with a claim is the part compute stops doing.

**Resolve the network's presence in this location.** The claim names a network and exists
in a cell, which is enough to find or create the `NetworkContext` for that pair. Compute
creates no `NetworkBinding` — that object is now NSO's business, created on the claim's
behalf and reported back only as a breadcrumb in `status.networkContextRef`.

**Allocate an address per requested family.** Each becomes an `IPClaim` of the appropriate
class against the platform allocator, with the network and location supplied as scope, per
[compute PR #210](https://github.com/datum-cloud/compute/pull/210). Each entry in the
claim's `addresses` list becomes one more `IPClaim` of the class it names. Every one must
succeed before anything is published.

**Create the interface and bind it.** The addresses, the gateways read from the location's
subnet, and the MTU read from the network land in `spec`. `Allocated` goes true, the claim
goes `Bound`, and compute can see an address.

**Wait for the data plane.** `Programmed` follows separately, when the attachment realizing
the interface reports ready.

The ordering matters for the failure case: an exhausted pool, a location with no space, or
a family the network does not carry all fail before an interface exists, so a claim that
cannot be satisfied reports why rather than binding to something incomplete. The condition
message should name what ran out and at which level, which is the property PR #210 asks
the allocator to preserve.

### Reaching the data plane

A `NetworkInterface` says what an interface must be. `VPCAttachment`, in the
[cloud](https://github.com/datum-cloud/cloud) API group, is where it becomes real, and the
split is deliberate: an interface is allocated as soon as a claim exists, before an instance
has been scheduled to any node, while an attachment cannot exist until a node, a container,
and a veth pair do.

```
NetworkInterfaceClaim   compute's intent      created per instance, per interface
        │  binds
        ▼
NetworkInterface        NSO's answer          addresses, gateway, MTU
        │  realized by
        ▼
VPCAttachment           the node's reality    node, containerID, VRF, veth, pod subnet
        │  attaches to
        ▼
VPC                     the data plane        base62 identity the fabric keys on
```

The agent on the node creates the `VPCAttachment` from the interface, copying
`spec.addresses` into `spec.interface.addresses` and naming the VPC backing this network in
this location. It reports back the facts only a node knows — the container ID, the host and
VRF device names, the pod subnet — and NSO sets `Programmed` on the interface when the
attachment reports ready, copying the VPC identifier onto `status.vpc`.

Nothing in `VPCAttachment` changes to support this. It already requires the addresses to
have been decided elsewhere; this names the elsewhere.

`Programmed` going false — an attachment lost, a node drained — does not release the
interface. The addresses stay allocated because the claim still exists, and the instance's
`Ready` condition reflects the loss without renumbering anything.

### What compute writes

Compute's changes are additive on the consumer-facing side and a removal on the internal
side. `InstanceNetworkInterface.network` keeps its existing `NetworkRef` type — the
consumer-facing reference does not move.

**On `InstanceNetworkInterface`**, four fields join it:

| Field | Meaning | Default |
|---|---|---|
| `name` | the interface name in the guest, and the claim-name suffix | `eth0` |
| `ipFamilies` | families to carry, in priority order | `[IPv6]` |
| `reclaimPolicy` | whether addresses survive a scale-down | `Delete` |
| `addresses[].class` | extra addresses by class | none |

`ipFamilies` and `reclaimPolicy` are the two fields
[compute #112](https://github.com/datum-cloud/compute/issues/112) already calls for.
`addresses[].class` is compute PR #210's. `name` is new here, and exists because a claim
name has to be derived from something stable that a consumer chose.

**On `InstanceNetworkInterfaceStatus`**, the single-address shape grows into the list the
interface actually holds:

```yaml
status:
  networkInterfaces:
    - name: eth0
      addresses:
        - family: IPv6   address: fd20:a1b:2c3d:1:0:1::/96   primary: true
        - family: IPv4   address: 10.128.0.2/32
      external:
        - family: IPv4   address: 198.51.100.11
      assignments:
        # Retained: the primary address of each family, so existing print
        # columns and clients keep working.
        networkIP: fd20:a1b:2c3d:1:0:1::
        externalIP: 198.51.100.11
      conditions:
        - type: Allocated   status: "True"
        - type: Programmed  status: "True"
```

`assignments` keeps its shape and finally gets written. It becomes a projection of the
primary address rather than a field of its own, which means the print column on `Instance`
starts showing a value without anyone changing the column.

**In `WorkloadDeploymentReconciler`**, the per-deployment `NetworkBinding` and `SubnetClaim`
are replaced by one `NetworkInterfaceClaim` per instance per interface. That is the fix for
the shared-allocation problem: the object that holds an address is now per-instance, so
addresses can be too.

**The `network` scheduling gate becomes per-instance.** Today it is removed when a shared
allocation is ready, which releases every instance in the deployment at once. It should be
removed for an instance when that instance's own claims report `Ready` — meaning bound,
allocated, and programmed. An instance whose interface is allocated but not yet programmed
stays gated, which is the whole reason the two conditions are separate.

Nothing changes in the federation path. Claims and interfaces are created in the POP cell
by NSO, which already runs there for exactly this reason. Addresses land on the local
`Instance`, and the existing write-back to Karmada and `InstanceProjector` mirror carry them
to the project. The consumer reads addresses in the same place they read everything else
about an instance.

### A workload in two locations

The same workload from compute PR #210 — one network, two locations, two replicas each,
dual-stack, a public address per instance. Every object this design causes to exist:

**The consumer's project.** Two objects, both written by the consumer: the `Network` and
the `Workload`. Nothing about interfaces appears here at all.

**Each POP cell.** The deployment arrives by placement; everything below it is created
locally.

```
us-central-1                                  eu-west-1
  WorkloadDeployment/…-americas                 WorkloadDeployment/…-europe
  Instance/…-americas-us-central-1-{0,1}        Instance/…-europe-eu-west-1-{0,1}
  NetworkInterfaceClaim/…-{0,1}-eth0            NetworkInterfaceClaim/…-{0,1}-eth0
  NetworkInterface ×2                           NetworkInterface ×2
  NetworkContext/default-us-central-1           NetworkContext/default-eu-west-1
  VPCAttachment ×2                              VPCAttachment ×2
```

Four claims, four interfaces, four attachments, and one `NetworkContext` per location that
the first claim in that location caused to exist. Each interface holds three addresses — an
IPv6 endpoint block, an IPv4 address, and a public IPv4 address — for the twelve
allocations compute PR #210 accounts for.

**What each layer knows.** The claim knows the network, the interface name, the families,
and the policy. The interface adds the addresses, the gateways, and the MTU. The attachment
adds the node, the container, and the devices. No layer repeats the one below it, and no
consumer of a layer needs the one above it.

**Scaling to one replica in `us-central-1`** deletes one instance, and with it one claim.
With `reclaimPolicy: Retain`, the interface stays, holding `fd20:a1b:2c3d:1:0:2::/96`,
`10.128.0.3/32`, and `198.51.100.12`, in phase `Available`. Scaling back up recreates a
claim with the identical name, which binds that interface, and the new instance comes back
on the address the old one had. With `Delete`, all three addresses go back to their pools
and the new instance gets whatever is next.

Removing the `eu-west-1` placement releases the addresses its instances held, but not that
location's `NetworkContext` or subnet. Those belong to the network, and other workloads on
it draw from them.

## What this depends on

An interface resource is necessary and not sufficient. Each of the following is assumed and
none of it is provided here.

- **[Compute PR #210](https://github.com/datum-cloud/compute/pull/210) must land first.**
  This design consumes classes, `IPClaim`, retention, and the central allocator wholesale.
  Without it there is no answer to what address an interface gets, and `spec.addresses` has
  nothing to hold.
- **A network's default families and an interface's must agree.** `NetworkSpec.ipFamilies`
  defaulted to `[IPv4]` while a claim here defaults to `[IPv6]`, so the default workload on
  the default network requested a family its network did not carry. `Network`'s default is
  now `[IPv6]`, which settles the new-object case; networks created before the flip persisted
  `[IPv4]` and still need patching.
- **A network needs one routing identity across every location it reaches**, unique
  platform-wide, or the two halves of a multi-location workload are unrelated networks
  sharing a name. That it is not the per-location forwarding-instance identifier needs to
  stay true.
- **A moved instance needs its old route withdrawn before the new one is trusted.**
  Retention makes this worse, not better: the address stays valid across the move, so both
  advertisements look legitimate and traffic splits.
- **Subnets and gateways need programming, not just allocation.**
  `spec.addresses[].gateway` is only useful if something answers at that address. Without
  it, oversized packets are dropped silently — handshakes succeed and large transfers hang.
  This is exactly why `Programmed` is a separate condition rather than folded into
  `Allocated`.
- **Consuming a class must be a privilege.** `spec.addresses[].class` on a claim is written
  by compute on a consumer's behalf. If the class name is the only authorization boundary,
  the check on it must fail closed, and it must check the consumer's project rather than
  the platform identity that made the call.
- **Providers must migrate.** The GCP and Unikraft providers read `NetworkBinding`,
  `NetworkContext`, `SubnetClaim`, and `Subnet` today. They keep working until they are
  moved to `NetworkInterface`, and compute's creation of the old objects cannot be removed
  until every one of them has moved.

## Drawbacks

- **Two resources where consumers see none.** Every interface now costs a claim, an
  interface, and an attachment where a deployment used to cost one shared subnet claim. At
  four replicas that is twelve objects instead of one. The cost is per-instance object
  count in the POP cell, and it buys the per-instance address that motivates the work.
- **A retained interface holds capacity nobody else can use.** That is the price of an
  address that survives a scale-down, and on a finite public range it is the price that
  matters. The lease is the mitigation, not the elimination.
- **The binding is invisible until it exists.** A claim that cannot bind — no space in the
  location, a family the network does not carry — reports a condition and holds its instance
  gated. That is correct and it is also a new way for an instance to be stuck, which needs
  the failure to name what could not be satisfied rather than reporting "not ready".
- **Splitting `Allocated` from `Programmed` makes readiness slower to reach**, on purpose.
  Instances that used to come up on allocation now wait for the data plane. Some workloads
  will notice.

## Alternatives

- **Put the addresses on `Instance.status` and skip both resources.** Rejected: it is close
  to the status quo, it gives providers nothing stable to watch, and an address on an
  instance's status cannot outlive the instance, which forecloses retention entirely.
- **One resource instead of two — a `NetworkInterface` compute creates directly.**
  Rejected: it merges intent with result, so a provider cannot tell a requested address
  from an allocated one, and it removes the object that survives the instance. The claim
  exists precisely so something outlives its holder.
- **Reuse `VPCAttachment` as the compute-facing resource.** Rejected: an attachment
  requires a node, a container ID, and device names, none of which exist when the address
  must be allocated. It is the realization of an interface, not the request for one.
- **Keep `NetworkBinding` and `SubnetClaim` and just add a per-instance `SubnetClaim`.**
  Rejected: it fixes the sharing problem and none of the others. Compute stays coupled to
  NSO internals, providers still walk four objects, and there is still nothing that survives
  an instance.
- **Bind by re-matching rather than by never unbinding.** Rejected for the same reason
  compute PR #210 rejects it: releasing an interface on instance deletion and re-matching it
  later opens a window where the address is loose and needs a durable identity distinct from
  the holder. The claim name is already that identity.
- **A `Released` phase, as storage has.** Rejected: it requires an operator to clear a stale
  reference before the interface is usable, which for a public address means capacity held
  hostage to a manual step. The `uid` on `claimRef` provides the same safety without the
  dead end.
- **Let the claim carry the location explicitly**, as `SubnetClaim` does. Rejected: the
  claim exists in the cell that serves the location, so the field would be a value the
  writer copies from where it is already standing, and a value that can disagree with
  reality.

## Open Questions

**What size block does an endpoint actually get?** The
[tenant addressing plan](https://github.com/datum-cloud/enhancements/blob/main/architecture/design/network/addressing/tenant.md)
specifies a `/96` per endpoint. `VPCAttachment.status.podSubnet` documents a `/80`. Both
cannot be right, and the answer changes what `spec.addresses[].address` holds for IPv6 and
how much space an interface can hand to containers without a control-plane round trip.

**Where does a load balancer's interface come from?** Nothing in this design is
instance-specific, and a load balancer needs the same object with the same fields. Whether
it uses the same claim kind, or the shape simply gets copied, should be settled before a
second consumer arrives and settles it by accident.

**Does the claim need to express bandwidth or queue policy?** Interface-level rate limits,
queue disciplines, and offload capabilities are properties of a NIC a consumer might
reasonably ask for. Adding them later is additive; deciding now whether they belong on the
claim or on the instance type keeps them from landing in both.

**Should an interface be attachable to more than one network?** The model says no — one
interface, one network, and multi-network reachability comes from multiple interfaces. That
is the simpler answer and it should be confirmed against the connector and interconnect
work rather than assumed.

**Does `NetworkInterface` need to name the VPC in `spec`?** Today it appears only in
`status`, discovered when the attachment is programmed. If a provider ever needs the VPC
identity before an attachment exists, it moves — and that would make the interface depend on
a location's data plane being resolved at allocation time, which it currently does not.

## References

**What this builds on**

- [IP classes for workload address allocation](https://github.com/datum-cloud/compute/pull/210)
  — where an address comes from, and the retention model this document reuses without
  restating.
- [Tenant addressing](https://github.com/datum-cloud/enhancements/blob/main/architecture/design/network/addressing/tenant.md)
  — the per-network IPv6 `/48`, the per-location `/64`, and the per-endpoint block an
  interface holds.
- [Federated Deployment Scheduling](https://github.com/datum-cloud/compute/blob/main/docs/enhancements/federated-deployment-scheduling.md)
  — which control plane each object lives in, and the write-back path addresses travel to
  reach a consumer.

**The work this serves**

- [network-services-operator#164](https://github.com/datum-cloud/network-services-operator/issues/164)
  — the issue calling for these two resources, and the decoupling they provide.
- [compute#112](https://github.com/datum-cloud/compute/issues/112)
  — the compute-side issue: per-instance allocation, addresses that reach
  `Instance.status`, and the `ipFamilies` and `reclaimPolicy` fields.

**The types involved**

- [`Network`, `NetworkContext`, `SubnetClaim`](../../api/v1alpha) — the network an interface
  belongs to, its presence in a location, and the internals compute stops writing.
- [`VPC` and `VPCAttachment`](https://github.com/datum-cloud/cloud/tree/main/api/v1alpha1)
  — the data plane an interface is realized on.
- [Compute API types](https://github.com/datum-cloud/compute/tree/main/api/v1alpha)
  — `Instance`, `InstanceNetworkInterface`, and the status fields this fills in.
