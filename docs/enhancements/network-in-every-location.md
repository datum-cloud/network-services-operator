---
status: provisional
stage: alpha
latest-milestone: "v0.x"
---

# A network in every location a workload runs in

- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [What it feels like](#what-it-feels-like)
  - [The three objects](#the-three-objects)
  - [Notes/Constraints/Caveats](#notesconstraintscaveats)
- [Design Details](#design-details)
  - [Where a location is answered from](#where-a-location-is-answered-from)
  - [Locations reach the cells](#locations-reach-the-cells)
  - [Declaring that a network is needed somewhere](#declaring-that-a-network-is-needed-somewhere)
  - [Counting by listing](#counting-by-listing)
  - [The presence controller](#the-presence-controller)
  - [What a NetworkContext carries](#what-a-networkcontext-carries)
  - [Reaching the cell](#reaching-the-cell)
  - [What the claim reconciler reads](#what-the-claim-reconciler-reads)
  - [Keeping it current](#keeping-it-current)
  - [Garbage collection](#garbage-collection)
  - [Teardown and retained addresses](#teardown-and-retained-addresses)
  - [The address family default](#the-address-family-default)
  - [Two controllers, one name](#two-controllers-one-name)
  - [A workload in two locations](#a-workload-in-two-locations)
  - [Failure, reported as itself](#failure-reported-as-itself)
- [What this depends on](#what-this-depends-on)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
- [Open Questions](#open-questions)
- [References](#references)

## Summary

A network is declared in a consumer's project. Their instances run in edge cells. Today
nothing carries the network from one to the other, so the component that hands out
addresses at the edge cannot answer the two questions it has to answer before an instance
can start: which address families does this network carry, and what MTU do its interfaces
use.

This document makes a network's presence in a location a real, declared thing. A consumer
of the network — a workload deployment today, a load balancer or a gateway later — says
"I need this network here" by creating a **`NetworkBinding`** on the Karmada hub, owned by
the consuming object. A hub-resident controller turns every binding for the same
(network, location) pair into one **`NetworkContext`** carrying the network's rules, and
Karmada delivers that context to the cell. The cell reads the context instead of reaching
for a `Network` it cannot see.

Nothing here allocates an address. [PR #360](https://github.com/datum-cloud/network-services-operator/pull/360)
settles what holds an address; this settles what has to exist in a location before one can
be handed out.

## Motivation

[PR #360](https://github.com/datum-cloud/network-services-operator/pull/360) gives compute
a `NetworkInterfaceClaim` to write and a `NetworkInterface` to read. The claim reconciler
runs in the cell. Before it can bind anything it reads exactly two facts off the network:
the address families it carries, so it can refuse a claim asking for a family the network
does not have, and the MTU, which it copies onto the interface. Everything else it needs is
the network's name, which the claim already carries.

The `Network` holding those two facts lives in the consumer's project control plane. Cells
do not read project control planes. So today every claim rejects with `NetworkNotFound`,
every instance sits on its network scheduling gate, and the failure reads as a scheduling
problem rather than as a network that is not there.

Three things are missing, and they are one thing.

**A network's presence in a location is not declared anywhere.** `NetworkContext` is the
object that means it, and it exists only in project control planes, created as a side effect
of a binding.

**Nobody says who needs it.** `NetworkBinding` exists and its `spec.location` is required,
but no controller resolves that location — it is read only to build a name. There is no
record of which consumer wanted the network here, so there is nothing to key a lifetime to.

**Locations are invisible where the work happens.** No `Location` object exists on the hub
or on any cell, and the hub does not have the CRD. The platform knows which locations exist
and which have compute enabled; the systems placing and running workloads have to be told
separately.

### Goals

- Carry a network's address families and MTU to every location one of its consumers runs
  in, and keep them current when they change.
- Give a consumer one object to create that means "I need this network here", cleaned up by
  the hub apiserver when the consumer goes away.
- Let several consumers share one presence without anyone maintaining a count.
- Make a location visible to the systems that place and run workloads.
- Report a network that is not available in a location as exactly that, rather than as an
  instance that never starts.

### Non-Goals

- **Handing out addresses.** That is PR #360 and compute PR #210. This document is what has
  to be true before either can run.
- **Programming a data plane.** A `NetworkContext` states that a network is wanted in a
  location. Nothing here makes packets move.
- **Subnet allocation policy.** Which prefixes a location's subnets come from is IPAM's, and
  unchanged.
- **A general project-plane-to-cell replication framework.** This carries one kind for one
  reason. If a second kind needs the same path, that is a good time to generalize; not now.
- **Retiring the project-plane `NetworkBinding` controller.** It keeps serving the providers
  that read project-plane contexts until they move.
- **Scheduling.** Which location a workload lands in is compute's decision, made before any
  of this runs.

## Proposal

A consumer declares that it needs a network in a location. NSO makes the network present
there. When the last consumer goes, the presence goes.

### What it feels like

The consumer writes nothing new. They declare a workload on a network and pick locations, as
they do now:

```yaml
apiVersion: compute.datumapis.com/v1alpha
kind: Workload
spec:
  template:
    spec:
      networkInterfaces:
        - network:
            name: default
  placements:
    - name: default
      locations:
        - us-central-1
        - eu-west-1
```

Instances come up with addresses in both places, and the network means the same thing in
each: same families, same MTU. Change the MTU on the network and every location it reaches
learns.

When it does not work, it says so as a network problem:

```console
$ kubectl get instance hello-default-eu-west-1-0
NAME                        READY   REASON
hello-default-eu-west-1-0   False   NetworkNotAvailableInLocation
```

rather than as an instance that never leaves its gate.

### The three objects

```
project control plane            Karmada hub                        cell
─────────────────────            ───────────                        ────
Network                          NetworkBinding  ← owned by the consuming
  ipFamilies                       network         WorkloadDeployment
  mtu                              location
     │                               │
     │  read by                      │  listed by
     └───────────► presence controller ◄──┘
                             │ writes
                             ▼
                     NetworkContext  ──── Karmada ───►  NetworkContext
                       network                            (same object,
                       location                            no status, no
                       ipFamilies    ◄── the network's        owner refs)
                       mtu               rules, carried            │
                                                                   │ read by
                                                                   ▼
                                                        NetworkInterfaceClaim
                                                          reconciler
```

`NetworkBinding` is the declaration. `NetworkContext` is the presence. The presence
controller is the only thing that reads a project control plane, and it runs in the one
process that already has both views.

### Notes/Constraints/Caveats

- **The binding moves to the hub; the context follows it.** Both objects exist in project
  control planes today and keep existing there for the providers that read them. What is new
  is a hub-resident copy of the same pair, which is the only one a cell ever sees.
- **A real `ownerReference` does the cleanup.** The consumer and its binding are both hub
  objects in the same namespace, so the hub apiserver garbage collects the binding when the
  consumer goes away. No finalizer, no reconciler, no leak on a controller being down.
- **Reference counting is a `LIST`, not a number.** A count stored on the context is state
  that can be wrong. Listing labelled bindings for a (network, location) pair cannot be.
- **Everything a cell reads is in `spec`.** Karmada strips `status`, `uid`,
  `ownerReferences`, and finalizers from what it propagates. A context that carried its
  rules in `status` would arrive empty.
- **The cell never re-decides anything.** It does not evaluate availability, does not read a
  `Location` to make a decision, and does not reach for a `Network`. It reads the context it
  was given.
- **A `NetworkContext` is cheap and its teardown is lazy.** It is a name and two scalars, plus
  whatever subnet allocation follows it. It is not worth racing a retained address to
  reclaim.

## Design Details

### Where a location is answered from

Three resources currently overlap on the question "can this workload run here", and the
overlap is a real source of drift: compute filters city codes by `LocationBinding` at
admission and separately matches `Location` topology cell-side.

This proposal fixes the split by giving each resource one job.

| Resource | Where it lives | Answers | Read by |
|---|---|---|---|
| `ServiceAvailability` | Milo platform plane | which locations have which services enabled | the platform, to produce `LocationBinding` |
| `LocationBinding` | project control plane | **can this project run here** | admission, and the presence controller |
| `Location` | platform plane → hub → cell | what and where a location *is* — class, topology, coordinates | placement, and anything that needs a city code |

**`LocationBinding` is the answer to "can this workload run here."** It already exists as a
per-project projection created once the location's class is supported, the `Location` is
`Ready`, and the matching `ServiceAvailability` is `Available` — which is to say it already
folds in every input. Nothing else should re-derive that decision, and in particular a cell
must not: a cell that decides for itself can disagree with the admission that let the
workload in.

`Location` on a cell is identity and topology only. It exists so placement and the operator
can name a location and read its city code, not so anything can decide whether to run there.
`ServiceAvailability` never leaves the platform plane.

The presence controller therefore validates one thing about a location: that the consuming
project has a `LocationBinding` for it. If it does not, the binding reports
`LocationNotAvailable` and no context is created — the network is not made present somewhere
the project cannot run.

### Locations reach the cells

`Location` objects are copied out of Milo's platform control plane onto the Karmada hub, and
propagated from there to cells. The hub does not have the CRD today; it gets one, and so
does every cell.

A hub-resident replicator watches `Location` in the platform plane and maintains a
matching cluster-scoped copy on the hub. The copy is a projection, not a mirror: class,
topology, coordinates, and provider. Status does not survive propagation and is not worth
reconstructing; a location that is not `Ready` is simply not copied, and a location that
stops being `Ready` is removed.

An existing `ClusterPropagationPolicy` already carries NSO's resources to the cell fleet.
`Location` is added to it as a cluster-scoped selector. One caveat, called out because it is
easy to get wrong: today's policy selects cells by `infra.datum.net/gateways=enabled`, which
is the gateway edge fleet. Compute cells are the fleet that needs `Location` and
`NetworkContext`, and whether those are the same set of clusters is an infra question that
has to be answered before this ships — either the two labels converge, or this needs its own
policy with its own affinity.

**`LocationReference` loses its namespace.** `Location` became cluster-scoped and the
reference type was never updated, so `NetworkBinding.spec.location.namespace` is required
and meaningless. It is deprecated: defaulted when unset, ignored when set, and dropped at the
next API version. It cannot simply be removed now, because the deterministic
`NetworkContext` name is built from it and existing cell-side contexts already own subnets
under names containing that segment. The name keeps its shape, with the namespace segment
pinned to a constant, so nothing renames and nothing is orphaned.

### Declaring that a network is needed somewhere

A consumer that needs a network in a location creates a `NetworkBinding` on the hub, in the
project's hub namespace, owned by itself.

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: NetworkBinding
metadata:
  # Deterministic: several consumers of the same network in the same location
  # converge on one binding only if they choose to. Ownership is per consumer, so
  # each consumer creates its own.
  name: hello-default-us-central-1
  namespace: ns-8c1d…            # the project's hub namespace
  labels:
    networking.datumapis.com/network: default
    networking.datumapis.com/location: us-central-1
  ownerReferences:
    - apiVersion: compute.datumapis.com/v1alpha
      kind: WorkloadDeployment
      name: hello-default
      uid: 4f2a…
      blockOwnerDeletion: false
spec:
  network:
    name: default
  location:
    name: us-central-1
  # Optional, informational. Restates the owner in a form something that is not a
  # hub object can also use.
  consumer:
    apiGroup: compute.datumapis.com
    kind: WorkloadDeployment
    name: hello-default
```

**The `ownerReference` does the work, and `spec.consumer` is added anyway.** The
functional argument for the explicit reference is thin — the hub apiserver already collects
the binding, and NSO never resolves the field. Two things make it worth the field. It makes
a binding legible on its own: an operator looking at a stray binding can see who asked for
it without resolving a UID against a kind they may not have. And it leaves room for a
consumer that is not a hub object — a control-plane-resident load balancer, say — which
cannot be an owner and must delete its own binding; for those, `spec.consumer` is the only
record of why the binding exists. NSO reads it for nothing, and a binding is never held open
because of it.

The two labels are what make counting cheap, and they are the reason the label is on the
binding rather than derived at list time.

### Counting by listing

There is no reference count anywhere. To answer "does this network still need to be present
in this location", the controller lists bindings in the project's hub namespace matching the
network and location labels. Non-empty means yes.

This is derived state. It cannot drift, cannot be double-decremented, cannot be left high by
a controller that crashed between deleting a consumer and decrementing a counter, and needs
no repair tooling. The cost is a label-selected list per reconcile against an indexed cache,
which is a cache read.

The one property it requires is that a binding's lifetime is exactly its consumer's, which
is what the `ownerReference` guarantees and what a hand-maintained count never could.

### The presence controller

A new controller on the hub. It watches `NetworkBinding` on the hub and, for each
(project, network, location) triple, maintains one `NetworkContext`.

Per reconcile it:

1. Resolves the project from the hub namespace's `meta.datumapis.com/upstream-cluster-name`
   and `upstream-namespace` labels. Compute stamps them on the namespaces it creates, and
   NSO already decodes exactly these to find a project — the mechanism works unchanged here.
2. Confirms the project has a `LocationBinding` for the location. If not, the binding
   reports `LocationNotAvailable` and nothing is created.
3. Reads the `Network` from the project control plane, for `spec.ipFamilies` and `spec.mtu`.
4. Writes the `NetworkContext` into the same hub namespace, carrying those two facts, with
   the labels Karmada's policy selects on.
5. Reports readiness back onto every binding for the pair.

**It runs on the singleton manager, not the sharded one.** The central NSO manager's own
deployment cluster *is* the Karmada hub, and its milo provider engages project control
planes concurrently in the same process — so a hub-resident controller needs no new
deployment and no new credentials, and both reads it needs are already available to it. But
the sharded managers run three replicas with leader election disabled, so a controller
watching the hub from there would reconcile the same object in all three. This is a
registration detail with a correctness consequence, which is why it is stated here rather
than left to implementation.

### What a NetworkContext carries

`NetworkContext` today is a pure (network name, location) tuple. It gains the two facts a
cell needs, and they go in `spec`:

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: NetworkContext
metadata:
  name: default-datum-cloud-us-central-1
  namespace: ns-8c1d…
  labels:
    meta.datumapis.com/upstream-cluster-name: my-project
    meta.datumapis.com/upstream-namespace: default
    networking.datumapis.com/network: default
    networking.datumapis.com/network-uid: 9a4c…
spec:
  network:
    name: default
  location:
    name: us-central-1

  # Projected from the Network. The reason this object exists.
  ipFamilies:
    - IPv6
    - IPv4
  mtu: 1460

  # The Network generation these were read from. An operator comparing this to
  # the Network answers "has this location caught up" without guessing.
  networkGeneration: 7
status:
  conditions:
    - type: Programmed
    - type: Ready
```

Both new fields are optional at the API level and required in practice: a context written by
the presence controller always has them, and a context that predates this change does not.
The cell treats an absent `ipFamilies` as "not yet carried" and rejects with a reason that
says so, rather than defaulting to something and binding an interface to the wrong rules.

The `network-uid` label is not decoration — it is what garbage collection keys on, below.

`status` stays as it is and is not propagated. `Programmed` and `Ready` remain meaningful in
the project plane, where the existing controller sets them; on the cell copy they arrive
empty and nothing reads them.

### Reaching the cell

Karmada propagates the hub `NetworkContext` to cells under the existing
`ClusterPropagationPolicy`, selected by the `upstream-cluster-name` label the presence
controller stamps — the same selector every other NSO kind on that policy uses. The
namespace itself is already propagated by that policy.

What arrives at the cell is the spec and the labels. No owner references, no finalizers, no
uid, no status. That is the whole reason the network's rules live in `spec`.

Karmada's propagation is one-way. A cell cannot report anything back through it, which is a
constraint that shapes the teardown decision below rather than something to work around here.

### What the claim reconciler reads

The cell-side claim reconciler stops reading the `Network` and reads the `NetworkContext`
for the claim's network in the claim's namespace. Same two facts, same checks: a claim
asking for a family the context does not carry is rejected, and the context's MTU is copied
onto the interface.

It also stops creating a `NetworkBinding` cell-side. That write exists today only to produce
a context name; the context now arrives from the hub, and a binding created in a cell is a
declaration nobody can see or count.

A missing context is a distinct, legible rejection — `NetworkNotAvailableInLocation`, not
`NetworkNotFound`. The difference matters to whoever is looking: `NetworkNotFound` says the
consumer named a network that does not exist, and the new reason says the network exists and
has not reached here yet.

### Keeping it current

Three watches, replacing two requeues and a gap.

**On the hub, the presence controller watches `Network` in project control planes.** An
`ipFamilies` or MTU change enqueues every context for that network. Without this, a network
edited after a context exists never reaches the cells that carry it — the failure mode is
silent and can persist indefinitely, because nothing else would ever cause that context to
be rewritten.

**On the hub, it owns its contexts.** A context deleted out from under it is rebuilt.

**In the cell, the claim reconciler watches `NetworkContext`.** A context arriving or
changing enqueues the claims naming that network in that namespace, which needs a claims-by-
network index. This is what turns first-claim latency from "up to the 60-second reject
requeue" into "as soon as the context lands", and it is what makes an MTU change converge on
existing interfaces instead of only on the next one created.

The 60-second reject requeue stays as a backstop for the rejections that have no watch behind
them — an unresolvable project, an IPAM failure — but it stops being the mechanism by which a
network becomes usable.

### Garbage collection

Network deletion today finds the contexts to delete through a field index on the controller-
owner UID, in the same control plane as the network. Hub contexts are not owned by the
network — they cannot be; they are in a different cluster — so that index returns nothing,
and a `Network` would delete cleanly while orphaning every hub context and every cell copy
derived from it. That is the one place this design can lose objects permanently, so it gets
an explicit replacement rather than an inherited mechanism.

**The presence controller owns network deletion for hub contexts.** It is the only component
with both views, so it is the only one that can do this in one place:

- Every hub `NetworkContext` and `NetworkBinding` carries the network's UID as a label, and
  the hub indexes on it.
- The project-plane `Network` keeps a finalizer. The presence controller already watches
  `Network` for `ipFamilies` and MTU; a deletion timestamp is just another event on that
  watch.
- On deletion it lists hub bindings and contexts by network UID, deletes them, and removes
  the finalizer once the list is empty. Deleting the hub context deletes the cell copy
  through Karmada.

The UID label, not the name, is what this keys on. A network deleted and recreated under the
same name is a different network with a different address space, and its predecessor's
contexts must not be adopted.

The existing project-plane finalizer and its owner-UID index keep working for project-plane
contexts. Nothing about that path changes.

### Teardown and retained addresses

A `NetworkInterface` with `reclaimPolicy: Retain` outlives its workload, holding its
addresses so a replacement instance comes back to them. That interface lives in a cell. The
declaration keeping the network present in that cell is a binding on the hub owned by a
workload deployment that no longer exists. If the last binding going away tears the context
down, the retained interface has nothing to re-bind against when its slot returns — the exact
case retention exists to serve.

The cell cannot report upward. So the decision is made in two halves, on either side of a
one-way propagation.

**On the hub, the last binding going away deletes the context.** No grace period, no count to
maintain, no signal to wait for. The presence controller reconciles what the declarations
say, and when nothing declares the network is needed there, the hub says it is not.

**In the cell, a cell-local finalizer holds the copy while addresses are held.** Karmada
preserves what a cell-local controller adds to a propagated object. The cell adds a finalizer
to its `NetworkContext` while any `NetworkInterface` on that network exists in that namespace,
and removes it when the last one is released. A hub deletion therefore removes the cell copy
promptly in the ordinary case and blocks on a retained address in the case that matters.

When a retained slot comes back, its consumer creates a binding again, the presence
controller writes the context under the same deterministic name, and Karmada — which already
runs `conflictResolution: Overwrite` — adopts the lingering copy. The retained interface
never lost its context.

The honest cost: between the hub deleting and the cell releasing, the cell copy is a
terminating object that no hub declaration backs. It is readable, its spec is whatever it was
last given, and it will not receive updates until a binding brings it back. For a context
carrying two scalars that is acceptable. It would not be acceptable for an object carrying
something that has to stay current, which is an argument for keeping `NetworkContext` as thin
as it is.

The rejected alternative is simpler and worse: leave every context in place forever and
reclaim on a slow sweep. It never wrongly tears down a retained address, and it also never
tears anything down — a project that stops using a location keeps its presence, its subnet
allocation, and its address space held there indefinitely, and the sweep needed to fix that
is exactly the cell-side liveness signal the finalizer already provides, minus the promptness.

### The address family default

`NetworkSpec.ipFamilies` defaults to `[IPv4]`. `NetworkInterfaceClaim.spec.ipFamilies`
defaults to `[IPv6]`, as does compute's `InstanceNetworkInterface.ipFamilies`. A claim asking
for a family the network does not carry is a hard rejection, not a pending condition. So a
default workload on a default network rejects, in every location, forever.

This blocks the design in the plainest sense: everything above can be correct and the common
path still fails. It is called out as an open question in both PR #360 and compute PR #210,
and it needs settling in one of them rather than being noted a third time.

**The recommendation is that an unset `ipFamilies` on a claim means "whatever the network
carries."** Not a different default — no default. The claim's list becomes an explicit
narrowing, validated as hard as it is today, and omitting it makes the network the single
source of truth for what its interfaces carry. Compute's `[IPv6]` default is removed with it,
so an unset field on a workload stays unset on the claim.

Flipping `Network`'s default to `[IPv6]` instead has the same surface effect on new objects
and does nothing for the networks that already persisted `[IPv4]` at creation. Those networks
would keep rejecting IPv6 claims, which is correct behaviour and an unpleasant migration.
Making the claim defer removes the entire class of mismatch, including for objects that
already exist.

This requires a compute API change and is therefore a dependency, not something this document
can land on its own.

### Two controllers, one name

NSO's existing `NetworkBinding` controller runs against project control planes and creates
project-plane contexts under a deterministic name. The presence controller does the same
thing on the hub, under the same name, in a different cluster.

**A new controller takes the hub role; the existing one keeps its job.** They cannot be the
same controller, because the hub role needs two clusters at once — the hub for declarations,
the project plane for the `Network` — and needs to run on the singleton manager, while the
project-plane role runs sharded across projects. Moving the existing controller to the hub
would also break the providers reading project-plane contexts today, which is a migration
this work does not need to own.

The shared name is deliberate. It is the same tuple, so an operator finds the same name in
the project plane, on the hub, and in the cell, and can tell at a glance which of the three
is missing. When the providers move off project-plane contexts, the old controller retires
and nothing else changes.

### A workload in two locations

One network, one workload, two placements. Every object this causes to exist:

**The consumer's project.** `Network/default`, `Workload/hello`. Nothing about presence
appears here.

**The hub**, in `ns-8c1d…`:

```
WorkloadDeployment/hello-default-us-central-1   (compute)
WorkloadDeployment/hello-default-eu-west-1      (compute)
NetworkBinding/hello-default-us-central-1       owned by the first
NetworkBinding/hello-default-eu-west-1          owned by the second
NetworkContext/default-…-us-central-1           ipFamilies, mtu
NetworkContext/default-…-eu-west-1              ipFamilies, mtu
Location/us-central-1, Location/eu-west-1       cluster-scoped, replicated
```

**Each cell**, after propagation:

```
us-central-1                              eu-west-1
  NetworkContext/default-…-us-central-1     NetworkContext/default-…-eu-west-1
  NetworkInterfaceClaim ×2                  NetworkInterfaceClaim ×2
  NetworkInterface ×2                       NetworkInterface ×2
```

Add a gateway on the same network in `us-central-1` and it creates its own binding, owned by
itself, with the same network and location labels. The presence controller lists two bindings
where it listed one and writes the same context. Delete the workload and one binding goes;
the gateway's remains, so the context does. Delete the gateway too and the context goes —
unless a retained interface in that cell is still holding an address, in which case the cell
copy stays until it is not.

### Failure, reported as itself

The scope item most easily lost in implementation is the last one: a network that is not
available in a location has to read as that.

| What is wrong | Reported on | As |
|---|---|---|
| The project cannot run in this location | `NetworkBinding` | `LocationNotAvailable` |
| The network does not exist in the project | `NetworkBinding` | `NetworkNotFound` |
| The project cannot be resolved from the namespace | `NetworkBinding` | `ProjectUnresolved` |
| The context has not reached the cell yet | `NetworkInterfaceClaim` | `NetworkNotAvailableInLocation` |
| The network does not carry a requested family | `NetworkInterfaceClaim` | `AddressFamilyNotCarried` |

Compute surfaces the claim's rejection reason on the instance, so the last two reach a
consumer running `kubectl get instance`. The first three are operator-facing and live on the
binding, which is where the declaration is.

## What this depends on

None of the following is provided here, and all of it is required.

- **No NSO manager runs on any edge cell today.** The claim reconciler, the cell-local
  finalizer, and everything else cell-side needs one. Every option in PR #360 and in this
  document needs it; it is the largest single dependency and it is not a networking design
  problem.
- **Nothing seeds per-project `IPClass` and `IPPool` objects.** They exist only as chainsaw
  fixtures. A context can be present in a location and a claim will still find nothing to
  allocate from.
- **Nothing sets `Programmed` in any repository**, so `Ready` is unreachable on both
  `NetworkContext` and `NetworkInterface`. Compute gates instances on `Bound` and
  `Allocated`, which is why anything works today, and that is a workaround rather than the
  design.
- **Compute's hub `ClusterRole` grants no `networking.datumapis.com` at all.** Compute cannot
  create a `NetworkBinding` on the hub until it does. This is a one-line dependency that
  blocks the entire path.
- **The address family default must be resolved**, in this document's terms or another's.
  See [The address family default](#the-address-family-default).
- **The cell fleet selector must be settled.** The existing `ClusterPropagationPolicy`
  selects gateway-enabled clusters. Whether compute cells carry that label, or need their own
  policy, is an infra question with a wrong answer that fails silently — the objects simply
  never arrive.
- **`Location` needs a CRD on the hub and on every cell.** Neither has one.

## Drawbacks

- **A fourth place a network's rules exist.** They are authored on the `Network`, projected
  onto a hub `NetworkContext`, propagated to a cell copy, and copied again onto every
  `NetworkInterface`. Each hop is a place they can be stale. The watches close the loop, and
  `networkGeneration` makes staleness visible, but the fan-out is real and it grows with the
  number of locations.
- **The cell-local finalizer is a second lifecycle owner on a propagated object.** It is the
  mechanism that makes retention safe across a one-way propagation, and it is also a way for
  a cell copy to get stuck terminating if the cell controller is down or wrong. That failure
  is quiet.
- **Ownership couples networking's lifetime to compute's object model.** The binding's
  cleanup is correct because the hub apiserver collects it. A consumer that is not a hub
  object gets none of that and must clean up after itself, and the design gives it only a
  convention.
- **Two controllers writing objects with the same name in different clusters** is easy to
  misread. An operator who does not know which cluster they are looking at will draw the
  wrong conclusion, and the shared name is precisely what makes that possible.

## Alternatives

- **Have the cell read the project control plane directly for the network.** Rejected: cells
  read state from Karmada, deliberately. IPAM is the one exception and stays one because
  allocation is a transaction against a central allocator, not a data read — a per-network
  data read has no such justification, and it would give every cell a credential for every
  project control plane.
- **Store a reference count on the `NetworkContext`.** Rejected: it is state that can be
  wrong, and every way it goes wrong strands a network's presence or tears one down under a
  running workload. A `LIST` over labelled bindings is derived and cannot drift.
- **Make the consumer create the `NetworkContext` directly and skip the binding.** Rejected:
  the context is shared by definition, so it cannot be owned by any one consumer, so its
  lifetime cannot be a consumer's. The binding exists because the thing with a per-consumer
  lifetime and the thing that is shared have to be different objects.
- **Let compute write the context's `ipFamilies` and MTU when it creates the binding.**
  Rejected: it makes compute read networking's internals again, which is what PR #360
  removed, and it freezes the values at creation. A network edited afterwards would never
  converge.
- **Propagate the `Network` itself to cells.** Rejected: it carries IPAM configuration and
  ranges no cell needs, gives every cell the whole network object as an implicit API surface,
  and has no per-location lifetime — there would be nothing to say a network is *not* wanted
  in a location any more.
- **Give the presence controller its own deployment.** Rejected: the central manager's local
  cluster is already the hub and its milo provider already engages project control planes in
  the same process. A second deployment adds a credential, a rollout, and an alert path for
  nothing.
- **Carry the network's rules in `NetworkContext.status`.** Not an alternative — Karmada
  strips status. Recorded because it is the obvious shape and it silently produces empty
  objects at the cell.
- **Leave contexts in place forever and reclaim on a slow sweep.** Discussed under
  [Teardown and retained addresses](#teardown-and-retained-addresses); rejected because the
  sweep needs the same cell-side signal the finalizer provides, and holds address space in
  the meantime.

## Open Questions

**Does one binding per consumer produce too many bindings?** Ten deployments on one network
in one location produce ten bindings and one context. That is the point — each has its own
lifetime — but at scale it is ten hub objects where a shared one would be one, and the list
per reconcile grows with it. A shared binding with a finalizer-maintained holder list is the
alternative, and it trades apiserver-managed cleanup for cleanup NSO has to get right.

**What happens to a location that is removed from the platform?** A `Location` that stops
being `Ready` is no longer replicated, so cells stop seeing it. Whether the contexts in that
location should be torn down, held, or reported as stranded is not decided here, and the
answer probably differs between a location being drained and one being deleted.

**Should a `NetworkContext` carry anything else?** Every field added is another thing that
can be stale in a cell and another reason to rewrite it. `ipFamilies` and MTU are what the
claim reconciler reads today. Network policy, connector reachability, and the network's
routing identity are all plausible next fields, and each should have to argue for itself.

**Where does a non-hub consumer's binding live?** A load balancer resident in a project
control plane cannot own a hub object. It can create a binding and delete it, but nothing
collects it if that controller dies mid-delete. A lease, or a hub-resident proxy object, is
the shape of the answer; it should be settled before the second consumer settles it by
accident.

**Does the presence controller belong to NSO at all?** It reads Milo, writes the hub, and
knows about compute's namespace labels. NSO is where it can run today with no new
deployment, which is a good enough reason for now and not an argument that networking should
own project-plane-to-hub projection in general.

## References

**What this completes**

- [network-services-operator#369](https://github.com/datum-cloud/network-services-operator/issues/369)
  — the issue this tracks.
- [A network interface a workload can be handed](network-interfaces.md) and
  [PR #360](https://github.com/datum-cloud/network-services-operator/pull/360) — the claim
  and interface this makes deliverable, and the two facts the cell reads.
- [network-services-operator#164](https://github.com/datum-cloud/network-services-operator/issues/164)
  — the parent: decoupling compute from networking's internals.

**The consumer side**

- [compute#112](https://github.com/datum-cloud/compute/issues/112) — per-instance
  allocation, addresses that reach `Instance.status`.
- [compute#210](https://github.com/datum-cloud/compute/pull/210) — the addressing model, IP
  classes, and the family-default question this inherits.
- [compute#224](https://github.com/datum-cloud/compute/pull/224) — per-instance claims and
  the per-instance scheduling gate on `Bound` and `Allocated`, behind the
  `NetworkingIntegration` feature gate.

**The types and the plumbing**

- [`Network`, `NetworkBinding`, `NetworkContext`, `Location`, `LocationBinding`](../../api/v1alpha)
- [datum-cloud/infra](https://github.com/datum-cloud/infra), `apps/network-services-operator/`
  — the manager deployment shapes, the sharded and singleton split, and the
  `ClusterPropagationPolicy` that already carries NSO resources to cells.
