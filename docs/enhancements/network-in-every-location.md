---
status: provisional
stage: alpha
latest-milestone: "v0.x"
---

# A network in every location it is used

- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [What it feels like](#what-it-feels-like)
  - [The three objects](#the-three-objects)
  - [The consumer contract](#the-consumer-contract)
  - [Notes/Constraints/Caveats](#notesconstraintscaveats)
- [Design Details](#design-details)
  - [Where a location is answered from](#where-a-location-is-answered-from)
  - [Locations reach the cells](#locations-reach-the-cells)
  - [Declaring that a network is needed somewhere](#declaring-that-a-network-is-needed-somewhere)
  - [What a binding reports](#what-a-binding-reports)
  - [Consumers that are not hub objects](#consumers-that-are-not-hub-objects)
  - [Counting by listing](#counting-by-listing)
  - [The presence controller](#the-presence-controller)
  - [What a NetworkContext carries](#what-a-networkcontext-carries)
  - [Reaching the cell](#reaching-the-cell)
  - [What a consumer of the presence reads](#what-a-consumer-of-the-presence-reads)
  - [Keeping it current](#keeping-it-current)
  - [Garbage collection](#garbage-collection)
  - [Teardown and retained addresses](#teardown-and-retained-addresses)
  - [The address family default](#the-address-family-default)
  - [Two controllers, one name](#two-controllers-one-name)
  - [One network, two locations, two consumers](#one-network-two-locations-two-consumers)
  - [Failure, reported as itself](#failure-reported-as-itself)
- [What this depends on](#what-this-depends-on)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
- [Open Questions](#open-questions)
- [References](#references)

## Summary

A network is declared in a consumer's project. The things that attach to it run somewhere
else: in edge cells, in other control planes, in places that cannot read that project.
Nothing carries the network from where it is declared to where it is used, so the components
that attach to a network cannot answer the questions they must answer first, starting with
which address families the network carries and what MTU its interfaces use.

This document makes a network's presence in a location a real, declared thing.

**Anything that consumes a network** says "I need this network here" by creating a
`NetworkBinding` on the federation control plane. A controller resident there turns every
binding for the same (network, location) pair into one `NetworkContext` carrying the
network's rules, and the federation control plane delivers that context to the location.
Whatever needs the network there reads the context.

A consumer is any resource that attaches something to a network in a place. A compute
workload deployment is the consumer that exists today and the one that motivates the
timeline. A load balancer, a gateway, a connector, and an infrastructure provider standing
up an attachment are all the same shape, and none of them is a special case in this design.

Nothing here allocates an address. [PR #360](https://github.com/datum-cloud/network-services-operator/pull/360)
settles what holds an address; this settles what has to exist in a location before one can be
handed out.

## Motivation

A `Network` is one object in one project control plane, and it means something everywhere it
reaches: these address families, this MTU, this address space, one routing identity. Today
it means that in exactly one place, the control plane it was written in.

Every consumer that attaches to a network somewhere else hits the same wall, and each has so
far worked around it by reaching for something that happens to be nearby.

**The first consumer to hit it without a workaround is address allocation.**
[PR #360](https://github.com/datum-cloud/network-services-operator/pull/360) gives a consumer
a `NetworkInterfaceClaim` to write and a `NetworkInterface` to read. The claim reconciler
runs in the cell, and before it can bind anything it reads two facts off the network: the
address families it carries, so it can refuse a claim asking for a family the network does
not have, and the MTU, which it copies onto the interface. Everything else it needs is the
network's name, which the claim already carries. The `Network` holding those two facts is in
a project control plane. Cells do not read project control planes. So every claim rejects,
and the consumer waiting on it stalls.

Three things are missing, and they are one thing.

**A network's presence in a location is not declared anywhere.** `NetworkContext` is the
object that means it, and it exists only in project control planes, created as a side effect
of a binding.

**Nobody says who needs it.** `NetworkBinding` exists and its `spec.location` is required,
but no controller resolves that location; it is read only to build a name. There is no record
of which consumer wanted the network here, so there is nothing to key a lifetime to, and no
way for two consumers to share one presence.

**Locations are invisible where the work happens.** No `Location` object exists on the
federation control plane or on any cell, and neither has the CRD. The platform knows which locations exist and
which have which services enabled; the systems placing and running work have to be told
separately.

### Goals

- Carry a network's address families and MTU to every location a consumer of that network
  needs it in, and keep them current when they change.
- Give any consumer one object to create that means "I need this network here", with a
  lifetime tied to the consumer's own.
- Let several consumers of different kinds share one presence without anyone maintaining a
  count.
- Make a location visible to the systems that place and run work.
- Report a network that is not available in a location as exactly that, rather than as a
  consumer that never becomes ready.

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
- **Choosing locations.** Which locations a consumer needs is the consumer's decision, made
  before any of this runs.

## Proposal

A consumer declares that it needs a network in a location. NSO makes the network present
there. When the last consumer goes, the presence goes.

### What it feels like

Nobody writes anything new. Every consumer already names a network and already knows where it
runs, and the binding is derived from what it already has.

A compute workload names a network and picks placements:

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
      locations: [us-central-1, eu-west-1]
```

A load balancer names a network and the locations it fronts. A connector names a network and
the location it terminates in. In each case the consumer's controller creates a binding per
(network, location) it needs, and the network becomes present there.

What every consumer gets is the same guarantee: a network means the same thing in every
location it reaches. If it carries IPv6 only, that is true everywhere. Change its MTU and
every location learns.

When it does not work, it reads as a network problem rather than as a consumer that never
starts:

```console
$ kubectl get networkbinding lb-frontend-eu-west-1
NAME                    READY   REASON
lb-frontend-eu-west-1   False   LocationNotAvailable
```

### The three objects

Three control planes are involved, and the middle one is the federation control plane. It is
called **the hub** from here on, because what matters below is that bindings and contexts
live in its namespaces, not how it federates.

```
project control plane                 hub                          location
─────────────────────        ────────────────────────              ────────
Network                          NetworkBinding  ← owned by, or created for,
  ipFamilies                       network         the consuming resource
  mtu                              location
     │                               │
     │  read by                      │  listed by
     └───────────► presence controller ◄──┘
                             │ writes
                             ▼
                     NetworkContext  ─── propagated ──►  NetworkContext
                       network                            (same object,
                       location                            no status, no
                       ipFamilies    ◄── the network's        owner refs)
                       mtu               rules, carried            │
                                                                   │ read by
                                                                   ▼
                                                          whatever attaches to
                                                          the network here
```

`NetworkBinding` is the declaration. `NetworkContext` is the presence. The presence
controller is the only thing that reads a project control plane, and it runs in the one
process that already has both views.

### The consumer contract

Everything a consumer of a network has to know is this, and none of it is specific to a kind
of consumer.

**To need a network somewhere**, create a `NetworkBinding` on the hub, in the project's hub
namespace, naming the network and the location, carrying the two labels that make it
countable, and owned by the resource that needs it.

**To know it is there**, watch the binding for `Ready`. The presence controller reports the
same status onto every binding for the pair, so a consumer never has to find the shared
context or reason about the other consumers of it.

**To stop needing it**, delete the binding, or let the hub apiserver delete it when the
owning resource goes away.

**Do not read the `NetworkContext`** to make a decision about whether to proceed. It is
shared, it is not yours, and its readiness is already on your binding. Components that
attach to a network in a location read the context for the network's rules; consumers
declaring that they need one read their own binding.

A consumer never learns how many other consumers share the presence, never learns whether it
was the one that caused it to exist, and never has to clean up something shared.

### Notes/Constraints/Caveats

- **The binding moves to the hub; the context follows it.** Both objects exist in project
  control planes today and keep existing there for the providers that read them. What is new
  is a hub-resident copy of the same pair, which is the only one a cell ever sees.
- **A real `ownerReference` does the cleanup, where the consumer is a hub object.** The
  binding is then collected by the hub apiserver with no finalizer, no reconciler, and no
  leak on a controller being down. Consumers that are not hub objects get a weaker guarantee
  and are handled explicitly below.
- **Reference counting is a `LIST`, not a number.** A count stored on the context is state
  that can be wrong. Listing labelled bindings for a (network, location) pair cannot be.
- **Everything a location reads is in `spec`.** The federation control plane strips
  `status`, `uid`, `ownerReferences`, and finalizers from what it propagates. A context that carried its
  rules in `status` would arrive empty.
- **Nothing at the location re-decides anything.** It does not evaluate availability, does
  not read a `Location` to make a decision, and does not reach for a `Network`. It reads the
  context it was given.
- **A `NetworkContext` is cheap and its teardown is lazy.** It is a name and two scalars,
  plus whatever subnet allocation follows it. It is not worth racing a retained address to
  reclaim.

## Design Details

### Where a location is answered from

Three resources currently overlap on the question "can this consumer run here", and the
overlap is a real source of drift: compute filters city codes by `LocationBinding` at
admission and separately matches `Location` topology cell-side. Any second consumer arriving
today would pick one of the two and deepen the split.

This proposal fixes it by giving each resource one job.

| Resource | Where it lives | Answers | Read by |
|---|---|---|---|
| `ServiceAvailability` | Milo platform plane | which locations have which services enabled | the platform, to produce `LocationBinding` |
| `LocationBinding` | project control plane | **can this project use this location** | admission, and the presence controller |
| `Location` | platform plane → hub → location | what and where a location *is*: class, topology, coordinates | placement, and anything that needs a city code |

**`LocationBinding` is the answer to "can this consumer run here."** It already exists as a
per-project projection created once the location's class is supported, the `Location` is
`Ready`, and the matching `ServiceAvailability` is `Available`, which is to say it already
folds in every input. Nothing else should re-derive that decision, and in particular nothing
at the location should: a component that decides for itself can disagree with the admission
that let the work in.

`Location` at a location is identity and topology only. It exists so placement and the
operator can name a location and read its city code, not so anything can decide whether to
use it. `ServiceAvailability` never leaves the platform plane.

The presence controller therefore validates one thing about a location: that the consuming
project has a `LocationBinding` for it. If it does not, the binding reports
`LocationNotAvailable` and no context is created. The network is not made present somewhere
the project cannot use.

### Locations reach the cells

`Location` objects are copied out of Milo's platform control plane onto the federation
control plane, and propagated from there. The hub does not have the CRD today; it gets one, and so does every
cell.

A hub-resident replicator watches `Location` in the platform plane and maintains a matching
cluster-scoped copy on the hub. The copy is a projection, not a mirror: class, topology,
coordinates, and provider. Status does not survive propagation and is not worth
reconstructing; a location that is not `Ready` is not copied, and one that stops being
`Ready` is removed.

An existing `ClusterPropagationPolicy` already carries NSO's resources to a cell fleet.
`Location` is added to it as a cluster-scoped selector. One caveat, called out because it is
easy to get wrong: today's policy selects cells by `infra.datum.net/gateways=enabled`, which
is the gateway edge fleet. The fleet that needs `Location` and `NetworkContext` is every
fleet a network consumer runs in, which today means compute cells as well. Whether those are
the same set of clusters is an infra question that has to be answered before this ships:
either the labels converge, or this needs its own policy with its own affinity.

**`LocationReference` loses its namespace.** `Location` became cluster-scoped and the
reference type was never updated, so `NetworkBinding.spec.location.namespace` is required and
meaningless. It is deprecated: defaulted when unset, ignored when set, and dropped at the
next API version. It cannot be removed now, because the deterministic `NetworkContext` name
is built from it and existing contexts already own subnets under names containing that
segment. The name keeps its shape, with the namespace segment pinned to a constant, so
nothing renames and nothing is orphaned.

### Declaring that a network is needed somewhere

A consumer that needs a network in a location creates a `NetworkBinding` on the hub, in the
project's hub namespace.

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: NetworkBinding
metadata:
  # Per consumer, not per pair. Two consumers needing the same network in the
  # same location write two bindings and share one context.
  name: lb-frontend-us-central-1
  namespace: ns-8c1d…            # the project's hub namespace
  labels:
    networking.datumapis.com/network: default
    networking.datumapis.com/location: us-central-1
  ownerReferences:
    - apiVersion: networking.datumapis.com/v1alpha
      kind: LoadBalancer
      name: frontend
      uid: 4f2a…
      blockOwnerDeletion: false
spec:
  network:
    name: default
  location:
    name: us-central-1
  # Who asked, in a form that does not depend on being a hub object.
  consumer:
    apiGroup: networking.datumapis.com
    kind: LoadBalancer
    name: frontend
```

Nothing in the object is specific to a kind of consumer. Swap the owner and `spec.consumer`
for a `WorkloadDeployment` and it is compute's binding; swap them for a `Connector` and it is
a connector's.

**The `ownerReference` does the functional work, and `spec.consumer` is added anyway.** The
functional argument for the explicit reference is thin when the consumer is a hub object,
since the apiserver already collects the binding and NSO never resolves the field. It earns
its place for two reasons. It makes a binding legible on its own: an operator looking at a
stray binding sees who asked for it without resolving a UID against a kind they may not have.
And it is the only record of why the binding exists for a consumer that cannot be an owner,
which is the next section. NSO reads it for nothing, and a binding is never held open because
of it.

The two labels are what make counting cheap, and they are why the label is on the binding
rather than derived at list time.

### What a binding reports

The binding is the only object a consumer watches, so its status has to answer the whole
question on its own.

```yaml
status:
  # Set once the presence exists. A breadcrumb: it tells an operator which
  # shared object serves this binding. A consumer does not need to read it.
  networkContextRef:
    namespace: ns-8c1d…
    name: default-datum-cloud-us-central-1

  conditions:
    # The network is present in this location and the consumer may proceed.
    - type: Ready
      status: "True"
      reason: NetworkContextReady
      observedGeneration: 1
```

**One condition, and it means "you may proceed."** `NetworkBinding` already defaults and
sets exactly this condition, and the existing meaning — the binding is associated with a
context and the owner should expect functional network features — is the meaning this design
needs. Nothing is added to it.

`Ready` is false for one of two kinds of reason, and the split is worth keeping visible
because it decides who has to act.

| Reason | What is wrong | Whose problem |
|---|---|---|
| `LocationNotAvailable` | the project cannot use this location | the consumer's, or the platform's |
| `NetworkNotFound` | the network named does not exist in the project | the consumer's |
| `ProjectUnresolved` | the namespace does not resolve to a project | the platform's |
| `NetworkContextNotReady` | the presence exists and is not usable yet | nobody's yet, wait |

The first three are faults in this binding. The last is the shared presence still coming up,
and every binding for the pair reports it identically.

**Every binding for a pair carries the same answer, and that is deliberate.** The status is
a fan-out of one shared fact, written onto each declaration, so a consumer never has to find
the context, never has to know that other consumers exist, and never has to work out whether
it was the one that caused the presence to exist. The cost is a status write per binding per
change, which is why the presence controller writes status only when the answer differs from
what is already recorded.

**What the binding does not report.** It does not report how many consumers share the
presence, which would leak one consumer's existence to another and would be a number that
can be stale. It does not report the network's `ipFamilies` or MTU, which belong to whatever
attaches to the network rather than to whoever declared it should be present. And it does
not report data-plane programming: `Ready` on a binding means the network is present, not
that packets move, which stays true to what `Programmed` on the context is for.

### Consumers that are not hub objects

The clean cleanup story assumes the consumer is a hub object in the same namespace as its
binding. A consumer resident in a project control plane, or one that is not a Kubernetes
object at all, cannot be an owner. This is not a corner case to defer: the consumers that
exist in NSO today, load balancers and gateways among them, are project-plane objects.

Such a consumer creates its binding and is responsible for deleting it, with `spec.consumer`
as the record of what it is. That is a strictly weaker guarantee, and the failure mode is a
binding that outlives its consumer and holds a presence nobody needs.

Two properties keep the blast radius small. The presence it holds open is idempotent and
shared, so a leaked binding costs one context and its subnet allocation rather than anything
per consumer. And the binding names its consumer, so a sweep that reconciles bindings against
the consumers they name is possible later without an API change.

The design does not attempt that sweep now. What it does is refuse to bake the assumption
that a consumer is a hub object into the mechanism: nothing in the presence controller reads
an owner reference, and a binding with no owner at all is served identically.

### Counting by listing

There is no reference count anywhere. To answer "does this network still need to be present
in this location", the controller lists bindings in the project's hub namespace matching the
network and location labels. Non-empty means yes.

This is derived state. It cannot drift, cannot be double-decremented, cannot be left high by
a controller that crashed between deleting a consumer and decrementing a counter, and needs
no repair tooling. The cost is a label-selected list per reconcile against an indexed cache,
which is a cache read.

It also means consumers of different kinds compose without knowing about each other. A
workload deployment and a load balancer needing the same network in the same location are two
rows in one list, and neither controller has to learn that the other exists.

### The presence controller

A new controller on the hub. It watches `NetworkBinding` on the hub and, for each
(project, network, location) triple, maintains one `NetworkContext`.

Per reconcile it:

1. Resolves the project from the hub namespace's `meta.datumapis.com/upstream-cluster-name`
   and `upstream-namespace` labels. Compute stamps them on the namespaces it creates, and NSO
   already decodes exactly these to find a project, so the mechanism works unchanged here.
2. Confirms the project has a `LocationBinding` for the location. If not, the binding reports
   `LocationNotAvailable` and nothing is created.
3. Reads the `Network` from the project control plane, for `spec.ipFamilies` and `spec.mtu`.
4. Writes the `NetworkContext` into the same hub namespace, carrying those two facts, with
   the labels the federation control plane's policy selects on.
5. Reports readiness back onto every binding for the pair.

It never reads the consumer. It reads the binding, the location, and the network, which is
what makes it indifferent to what kind of thing asked.

**It runs on the singleton manager, not the sharded one.** The central NSO manager's own
deployment cluster is the federation control plane, and its milo provider engages project
control planes concurrently in the same process, so a hub-resident controller needs no new deployment and no
new credentials, and both reads it needs are already available to it. But the sharded
managers run three replicas with leader election disabled, so a controller watching the hub
from there would reconcile the same object in all three. This is a registration detail with a
correctness consequence, which is why it is stated here rather than left to implementation.

### What a NetworkContext carries

`NetworkContext` today is a pure (network name, location) tuple. It gains the two facts a
location needs, and they go in `spec`:

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
A reader treats an absent `ipFamilies` as "not yet carried" and refuses, with a reason that
says so, rather than defaulting to something and attaching to the wrong rules.

The `network-uid` label is not decoration. It is what garbage collection keys on, below.

`status` stays as it is and is not propagated. `Programmed` and `Ready` remain meaningful in
the project plane, where the existing controller sets them; on the propagated copy they
arrive empty and nothing reads them.

### Reaching the cell

The federation control plane propagates the hub `NetworkContext` under the existing
`ClusterPropagationPolicy`, selected by the `upstream-cluster-name` label the presence
controller stamps, which is the same selector every other NSO kind on that policy uses. The
namespace itself is already propagated by that policy.

What arrives is the spec and the labels. No owner references, no finalizers, no uid, no
status. That is the whole reason the network's rules live in `spec`.

Propagation is one-way. Nothing at the location can report back through it, which is a
constraint that shapes the teardown decision below rather than something to work around
here.

### What a consumer of the presence reads

Two different things read a `NetworkContext`, and the distinction is worth stating because
conflating them is how the shared object ends up with a per-consumer lifetime.

**Components that attach something to the network in that location** read the context for the
network's rules. The claim reconciler from PR #360 is the first: it stops reading the
`Network` and reads the context for the claim's network in the claim's namespace, applying
the same two checks it applies today. A claim asking for a family the context does not carry
is rejected, and the context's MTU is copied onto the interface. An infrastructure provider
bringing up an attachment is the same shape.

**Consumers declaring that they need the network** read their own binding, not the context.

The claim reconciler also stops creating a `NetworkBinding` itself. That write exists today
only to produce a context name; the context now arrives from the hub, and a binding created
at a location is a declaration nobody can see or count.

A missing context is a distinct, legible refusal: `NetworkNotAvailableInLocation`, not
`NetworkNotFound`. The difference matters to whoever is looking. `NetworkNotFound` says the
consumer named a network that does not exist; the new reason says the network exists and has
not reached here yet.

### Keeping it current

Three watches, replacing two requeues and a gap.

**On the hub, the presence controller watches `Network` in project control planes.** An
`ipFamilies` or MTU change enqueues every context for that network. Without this, a network
edited after a context exists never reaches the locations that carry it. The failure is
silent and can persist indefinitely, because nothing else would ever cause that context to be
rewritten.

**On the hub, it owns its contexts.** A context deleted out from under it is rebuilt.

**At the location, readers watch `NetworkContext`.** For the claim reconciler, a context
arriving or changing enqueues the claims naming that network in that namespace, which needs a
claims-by-network index. This is what turns first-claim latency from "up to the 60-second
reject requeue" into "as soon as the context lands", and what makes an MTU change converge on
interfaces that already exist rather than only on the next one created.

The 60-second reject requeue stays as a backstop for refusals with no watch behind them, such
as an unresolvable project or an IPAM failure, but it stops being the mechanism by which a
network becomes usable.

### Garbage collection

Network deletion today finds the contexts to delete through a field index on the
controller-owner UID, in the same control plane as the network. Hub contexts are not owned by
the network; they cannot be, being in a different cluster. That index returns nothing, so a
`Network` would delete cleanly while orphaning every hub context and every propagated copy
derived from it. That is the one place this design can lose objects permanently, so it gets
an explicit replacement rather than an inherited mechanism.

**The presence controller owns network deletion for hub contexts.** It is the only component
with both views, so it is the only one that can do this in one place:

- Every hub `NetworkContext` and `NetworkBinding` carries the network's UID as a label, and
  the hub indexes on it.
- The project-plane `Network` keeps a finalizer. The presence controller already watches
  `Network` for `ipFamilies` and MTU, so a deletion timestamp is another event on that watch.
- On deletion it lists hub bindings and contexts by network UID, deletes them, and removes
  the finalizer once the list is empty. Deleting the hub context deletes the propagated copy
  with it.

Deleting a network deletes bindings that consumers still own, which is correct and worth
being explicit about: the network is gone, so the presence cannot be kept, and each consumer
learns through its own binding disappearing rather than through a shared object it does not
watch.

The UID label, not the name, is what this keys on. A network deleted and recreated under the
same name is a different network with a different address space, and its predecessor's
contexts must not be adopted.

The existing project-plane finalizer and its owner-UID index keep working for project-plane
contexts. Nothing about that path changes.

### Teardown and retained addresses

A `NetworkInterface` with `reclaimPolicy: Retain` outlives the consumer that used it, holding
its addresses so a replacement comes back to them. That interface lives at the location. The
declaration keeping the network present there is a binding on the hub owned by a consumer
that no longer exists. If the last binding going away tears the context down, the retained
interface has nothing to re-bind against when its slot returns, which is the exact case
retention exists to serve.

The location cannot report upward. So the decision is made in two halves, on either side of a
one-way propagation.

**On the hub, the last binding going away deletes the context.** No grace period, no count to
maintain, no signal to wait for. The presence controller reconciles what the declarations
say, and when nothing declares the network is needed there, the hub says it is not.

**At the location, a local finalizer holds the copy while addresses are held.** The
federation control plane preserves what a local controller adds to a propagated object. The
location adds a finalizer
to its `NetworkContext` while any `NetworkInterface` on that network exists in that namespace,
and removes it when the last one is released. A hub deletion therefore removes the copy
promptly in the ordinary case and blocks on a retained address in the case that matters.

When a retained slot comes back, its consumer creates a binding again, the presence controller
writes the context under the same deterministic name, and propagation, which already runs
`conflictResolution: Overwrite`, adopts the lingering copy. The retained interface never lost
its context.

The honest cost: between the hub deleting and the location releasing, the copy is a
terminating object that no hub declaration backs. It is readable, its spec is whatever it was
last given, and it will not receive updates until a binding brings it back. For a context
carrying two scalars that is acceptable. It would not be acceptable for an object carrying
something that has to stay current, which is an argument for keeping `NetworkContext` as thin
as it is.

The rejected alternative is simpler and worse: leave every context in place forever and
reclaim on a slow sweep. It never wrongly tears down a retained address, and it also never
tears anything down. A project that stops using a location keeps its presence, its subnet
allocation, and its address space held there indefinitely, and the sweep needed to fix that
is exactly the location-side liveness signal the finalizer already provides, minus the
promptness.

### The address family default

`NetworkSpec.ipFamilies` defaults to `[IPv4]`. `NetworkInterfaceClaim.spec.ipFamilies`
defaults to `[IPv6]`, as does compute's `InstanceNetworkInterface.ipFamilies`. A claim asking
for a family the network does not carry is a hard rejection, not a pending condition. So a
default consumer on a default network rejects, in every location, forever.

This blocks the design in the plainest sense: everything above can be correct and the common
path still fails. It is called out as an open question in both PR #360 and compute PR #210,
and it needs settling in one of them rather than being noted a third time.

**The recommendation is that an unset `ipFamilies` on a claim means "whatever the network
carries."** Not a different default: no default. The claim's list becomes an explicit
narrowing, validated as hard as it is today, and omitting it makes the network the single
source of truth for what its interfaces carry. Compute's `[IPv6]` default is removed with it,
so an unset field on a workload stays unset on the claim, and any future consumer inherits
the same rule without having to pick a default of its own.

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
same controller, because the hub role needs two clusters at once, the hub for declarations
and the project plane for the `Network`, and needs to run on the singleton manager, while the
project-plane role runs sharded across projects. Moving the existing controller to the hub
would also break the providers reading project-plane contexts today, a migration this work
does not need to own.

The shared name is deliberate. It is the same tuple, so an operator finds the same name in the
project plane, on the hub, and at the location, and can tell at a glance which of the three is
missing. When the providers move off project-plane contexts, the old controller retires and
nothing else changes.

### One network, two locations, two consumers

One network. A workload deployed to two locations, and a load balancer fronting it in one of
them. Every object this causes to exist:

**The consumer's project.** `Network/default`, plus whatever the consumers are declared as.
Nothing about presence appears here.

**The hub**, in `ns-8c1d…`:

```
NetworkBinding/hello-us-central-1     owned by a WorkloadDeployment
NetworkBinding/hello-eu-west-1        owned by a WorkloadDeployment
NetworkBinding/lb-frontend-us-central-1   created for a LoadBalancer
NetworkContext/default-…-us-central-1     one context, two bindings
NetworkContext/default-…-eu-west-1        one context, one binding
Location/us-central-1, Location/eu-west-1   cluster-scoped, replicated
```

**Each location**, after propagation, holds its `NetworkContext` and whatever attaches to the
network there: claims and interfaces for the workload's instances, and whatever the load
balancer's data plane needs.

Three bindings, two contexts. In `us-central-1` two consumers of different kinds converge on
one presence and neither knows about the other. Delete the workload and its two bindings go
with it; the load balancer's remains, so the `us-central-1` context does and the `eu-west-1`
context does not. Delete the load balancer too and the last context goes, unless a retained
interface there is still holding an address, in which case the copy stays until it is not.

### Failure, reported as itself

The scope item most easily lost in implementation is the last one: a network that is not
available in a location has to read as that.

| What is wrong | Reported on | As |
|---|---|---|
| The project cannot use this location | `NetworkBinding` | `LocationNotAvailable` |
| The network does not exist in the project | `NetworkBinding` | `NetworkNotFound` |
| The project cannot be resolved from the namespace | `NetworkBinding` | `ProjectUnresolved` |
| The context has not reached the location yet | the attaching resource | `NetworkNotAvailableInLocation` |
| The network does not carry a requested family | the attaching resource | `AddressFamilyNotCarried` |

The first three are on the object the consumer created, which is why the consumer contract
says to watch the binding and nothing else. A consumer that surfaces its binding's reason
onto its own status gives an operator the answer without a second lookup; compute does this
by way of the claim's reason reaching the instance.

## What this depends on

None of the following is provided here, and all of it is required.

- **No NSO manager runs on any edge cell today.** The claim reconciler, the location-local
  finalizer, and everything else at the location needs one. Every option in PR #360 and in
  this document needs it. It is the largest single dependency and it is not a networking
  design problem.
- **Nothing seeds per-project `IPClass` and `IPPool` objects.** They exist only as chainsaw
  fixtures. A network can be present in a location and a claim will still find nothing to
  allocate from.
- **Nothing sets `Programmed` in any repository**, so `Ready` is unreachable on both
  `NetworkContext` and `NetworkInterface`. Compute gates instances on `Bound` and `Allocated`,
  which is why anything works today, and that is a workaround rather than the design.
- **A consumer needs permission to create a `NetworkBinding` on the hub.** Compute's hub
  `ClusterRole` grants no `networking.datumapis.com` at all, so the first consumer is blocked
  on a permission change, and every subsequent consumer needs the same grant.
- **The address family default must be resolved**, in this document's terms or another's. See
  [The address family default](#the-address-family-default).
- **The fleet selector must be settled.** The existing `ClusterPropagationPolicy` selects
  gateway-enabled clusters. Whether every fleet a network consumer runs in carries that label,
  or needs its own policy, is an infra question with a wrong answer that fails silently: the
  objects never arrive.
- **`Location` needs a CRD on the hub and everywhere it propagates.** Neither has one.

## Drawbacks

- **A fourth place a network's rules exist.** They are authored on the `Network`, projected
  onto a hub `NetworkContext`, propagated to a copy, and copied again onto whatever attaches.
  Each hop is a place they can be stale. The watches close the loop and `networkGeneration`
  makes staleness visible, but the fan-out is real and it grows with locations and with
  consumer kinds.
- **The location-local finalizer is a second lifecycle owner on a propagated object.** It is
  the mechanism that makes retention safe across a one-way propagation, and it is also a way
  for a copy to get stuck terminating if the local controller is down or wrong. That failure
  is quiet.
- **Cleanup is only as good as the consumer.** A hub-object consumer gets apiserver-managed
  collection. Everything else gets a convention and a `spec.consumer` field, which is a real
  asymmetry between consumer kinds in a design that otherwise treats them alike.
- **Two controllers writing objects with the same name in different clusters** is easy to
  misread. An operator who does not know which cluster they are looking at will draw the wrong
  conclusion, and the shared name is precisely what makes that possible.

## Alternatives

- **Have the location read the project control plane directly for the network.** Rejected:
  cells read state from the federation control plane, deliberately. IPAM is the one exception and stays one because
  allocation is a transaction against a central allocator, not a data read. A per-network data
  read has no such justification, and it would give every cell a credential for every project
  control plane.
- **Store a reference count on the `NetworkContext`.** Rejected: it is state that can be
  wrong, and every way it goes wrong either strands a network's presence or tears one down
  under a running consumer. A `LIST` over labelled bindings is derived and cannot drift.
- **Let each consumer create its own `NetworkContext`.** Rejected: the context is shared by
  definition, so it cannot be owned by any one consumer, so its lifetime cannot be a
  consumer's. The binding exists because the thing with a per-consumer lifetime and the thing
  that is shared have to be different objects.
- **Let the consumer write the context's `ipFamilies` and MTU when it creates the binding.**
  Rejected: it makes every consumer read networking's internals, which is what PR #360
  removed, and it freezes the values at creation. A network edited afterwards would never
  converge, and two consumers could write different answers.
- **Propagate the `Network` itself.** Rejected: it carries IPAM configuration and ranges no
  location needs, gives every location the whole network object as an implicit API surface,
  and has no per-location lifetime. There would be nothing to say a network is *not* wanted in
  a location any more.
- **Give the presence controller its own deployment.** Rejected: the central manager's local
  cluster is already the hub and its milo provider already engages project control planes in
  the same process. A second deployment adds a credential, a rollout, and an alert path for
  nothing.
- **Carry the network's rules in `NetworkContext.status`.** Not an alternative: the
  federation control plane strips status. Recorded because it is the obvious shape and it silently produces empty objects at
  the location.
- **Leave contexts in place forever and reclaim on a slow sweep.** Discussed under
  [Teardown and retained addresses](#teardown-and-retained-addresses); rejected because the
  sweep needs the same location-side signal the finalizer provides, and holds address space in
  the meantime.

## Open Questions

**Does one binding per consumer produce too many bindings?** Ten consumers on one network in
one location produce ten bindings and one context. That is the point, since each has its own
lifetime, but at scale it is ten hub objects where a shared one would be one, and the list per
reconcile grows with it. A shared binding with a finalizer-maintained holder list is the
alternative, and it trades apiserver-managed cleanup for cleanup NSO has to get right.

**Should a leaked binding be swept?** A consumer that is not a hub object can leave a binding
behind. `spec.consumer` makes a reconciliation possible, but resolving an arbitrary consumer
kind in an arbitrary control plane is a lot of machinery for a leak that costs one shared
context. Whether that is worth building, and whether a lease is a cheaper answer, is not
settled here.

**What happens to a location that is removed from the platform?** A `Location` that stops
being `Ready` is no longer replicated, so it stops being visible. Whether the contexts there
should be torn down, held, or reported as stranded is not decided here, and the answer
probably differs between a location being drained and one being deleted.

**Should a `NetworkContext` carry anything else?** Every field added is another thing that can
be stale and another reason to rewrite it. `ipFamilies` and MTU are what the first reader
needs. Network policy, connector reachability, and the network's routing identity are all
plausible next fields, and each should have to argue for itself, since each new consumer will
have a candidate.

**Does the presence controller belong to NSO at all?** It reads Milo, writes the hub, and
knows how project namespaces are labelled. NSO is where it can run today with no new
deployment, which is a good enough reason for now and not an argument that networking should
own project-plane-to-hub projection in general.

## References

**What this completes**

- [network-services-operator#369](https://github.com/datum-cloud/network-services-operator/issues/369)
  — the issue this tracks.
- [A network interface a workload can be handed](network-interfaces.md) and
  [PR #360](https://github.com/datum-cloud/network-services-operator/pull/360) — the claim and
  interface this makes deliverable, and the two facts read at the location.
- [network-services-operator#164](https://github.com/datum-cloud/network-services-operator/issues/164)
  — the parent: decoupling consumers from networking's internals.

**The first consumer**

- [compute#112](https://github.com/datum-cloud/compute/issues/112) — per-instance allocation,
  addresses that reach `Instance.status`.
- [compute#210](https://github.com/datum-cloud/compute/pull/210) — the addressing model, IP
  classes, and the family-default question this inherits.
- [compute#224](https://github.com/datum-cloud/compute/pull/224) — per-instance claims and the
  per-instance scheduling gate on `Bound` and `Allocated`, behind the `NetworkingIntegration`
  feature gate.

**The types and the plumbing**

- [`Network`, `NetworkBinding`, `NetworkContext`, `Location`, `LocationBinding`](../../api/v1alpha)
- [datum-cloud/infra](https://github.com/datum-cloud/infra), `apps/network-services-operator/`
  — the manager deployment shapes, the sharded and singleton split, and the
  `ClusterPropagationPolicy` that already carries NSO resources onward.
