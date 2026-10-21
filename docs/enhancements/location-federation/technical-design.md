---
status: provisional
stage: alpha
---

# Location Federation — Technical Design

Follow-on to the enhancement *Locations Where the Work Actually Runs*
(`datum-cloud/enhancements` PR #867), which settles the product behaviour and defers all
mechanism. This document resolves the six deferred items and five open questions it names.

- [Amendments the enhancement needs](#amendments-the-enhancement-needs)
- [The two legs](#the-two-legs)
- [Published form](#published-form)
- [Delivery](#delivery)
- [The publisher](#the-publisher)
- [Removal and lifecycle](#removal-and-lifecycle)
- [Detection](#detection)
- [Verification](#verification)
- [Rollout](#rollout)
- [Compute](#compute)
- [Questions resolved](#questions-resolved)
- [Questions still open](#questions-still-open)

## Amendments the enhancement needs

Three claims in the enhancement did not survive design. They are corrections to the
argument, not to the conclusions — every product-level decision it makes still stands.

**1. The output-versus-input check is necessary but not sufficient.** The enhancement says
it is "the only check that can distinguish" the two states. It distinguishes *"is the
publisher writing what it decided"*; it does not distinguish *"is the publisher deciding at
all."* For a customer entitled to nothing, desired and observed are both zero. For a dead
publisher, every customer reads both as zero. The check passes identically. What closes the
gap is a **canary** with a pinned entitlement and a known non-empty expected set, where zero
is never correct. The enhancement's requirement should be amended to name it.

**2. The ownership argument does not hold, and the conclusion changes with it.**
The enhancement argues the publisher should be the component that already computes the
availability decision. But the identity leg consumes no entitlement, no service
configuration, and no availability record — its sole input is the location record itself, so
there is no decision to be the decider of. The correct principle is **sole writer, not sole
decider**: exactly one component must own each published kind for pruning to be unambiguous.
That argues for *one* owner, not for *which*.

Which one follows from what already exists. The network services operator owns the published
type, owns the schema bundle that installs it on the hub and every cell, owns the federation
configuration, already holds a hub client and a client to the platform control plane, is a
consumer of the result at the cell, and already runs this exact publish-and-prune shape in
its gateway resource replicator and downstream garbage collector. The service catalog holds
none of that and would build a hub client to republish another repository's type with no
decision content in it.

The objection that this splits ownership if the availability leg is ever built does not
survive either: that leg would publish a **different kind with a disjoint prune set**, and
the sole-writer invariant is per-kind, not per-hub. There is no migration waiting.

The cost, stated honestly: it deepens the coupling of the location type to the network
services operator at the moment the direction of travel is to move it out. That coupling is
symmetric — either owner reads a type it does not own — and whenever the type moves, the
publisher's source changes by one unstructured read either way.

**3. The 34-day figure is not documented anywhere.** The mechanism, the symptom string, and
a first-hand observation of the empty state on 2026-08-04 are all on the record. The
duration, and the causal link from the leader-election defect to the projection stoppage,
are not written down in any issue. Either source it or soften it — it is currently asserted
as fact in a document under review.

There is a fourth point worth adding rather than correcting. The 2026-08-04 observation
reads the empty state as *"the test asserts the wrong thing against an empty project"*
rather than *"the publisher stopped."* That is the enhancement's property 2 defeating a
competent engineer in real time, on the record. It is a stronger argument for the canary
than any hypothetical, and belongs in the enhancement.

## The two legs

The enhancement treats "getting location information outward" as one problem. Design
separates it into two legs with materially different properties, and most of the
reconciliation below follows from keeping them apart.

| | **Identity leg** (this design) | **Availability leg** (deferred) |
|---|---|---|
| What travels | a location's identity and locality | the resolved decision "is service S usable at L" |
| Input | the location record, and nothing else | availability record × service config × location readiness |
| Destination | the hub, then the one site that is that location | the hub only, never a site |
| Gated on | the location **existing** | all three gates passing |
| Legitimate zero? | **No** — expected count is the number of locations | Yes, per customer |
| Reader today | compute and NSO, at the site | none |

The last two rows carry most of the weight. Because the identity leg is gated on existence
rather than on gates, its expected output is simply the number of location records and is
never legitimately zero — **the enhancement's property 2 is structurally absent from it.**
That is a strong independent argument for keeping the identity leg free of the availability
gates, beyond the blast-radius argument the enhancement already makes.

Property 2 does still bite on the *existing* project-projection leg, which is where the
original failure happened and where the canary is required.

## Published form

**Publish a new cluster-scoped kind — `ServingLocation` — carrying identity and locality
only, with no status subresource at all.** Do not mirror the `Location` record.

Two arguments decide it, and neither is payload size:

- **Schema ownership.** The `Location` Go type is openly slated to leave the network
  services operator; it carries a standing note to that effect. Mirroring would put a type
  with an unsettled home onto the hub and 22 cells, and make the eventual move a fleet
  migration.
- **Mirroring makes a bug look like a feature.** Compute's site-side code does an
  unfiltered cluster-wide list-and-take-first. Against a mirrored `Location` that *appears*
  to work, quietly re-implementing the site-side selection this design exists to delete. A
  distinct kind makes the wrong code fail loudly instead.

The reduced-spec-same-GVK variant is rejected outright: it depends on silent apiserver
pruning and needs two hand-maintained CRDs for one Go type, which the repo forbids.

**No status, by construction.** The delivery layer strips status, so anything a site must
read has to be declared. Rather than treating that as a constraint to work around, the kind
has no status subresource declared at all — which removes the possibility of a future
contributor putting something load-bearing there. Freshness is carried as declared
spec-side markers under `spec.source` (the source record's generation, and the time its
content last changed), which is what lets a site and an operator reason about staleness
without a status field.

`publishedAt` means **when the content last changed**, not when the publisher last ran. A
timestamp stamped on every reconcile would make the object change on every pass and
propagate to every cell forever for no content change; defining it as content-change time
keeps no-op reconciles genuine no-ops under server-side apply.

**Locality stays in the `topology` map**, keyed as it is on `Location` and `LocationBinding`
today. Uniformity across the three kinds means the consumer migration is a type swap rather
than a field-access rewrite — compute already reads the city code out of that map from two
different kinds. The known silent failure, where an empty topology makes every
location-scoped deploy fail with nothing catching it, is closed by validation rather than by
changing the shape: a CEL rule requires the city-code key present and non-empty, and the
publisher refuses to publish a source location lacking one, reporting it as a linkage
mismatch rather than skipping silently.

**The kind carries no cell reference.** An earlier draft proposed a `spec.cell` field for
per-cell attribution; it is dropped because it contradicts the delivery design, which
rejects keying on cluster identity precisely because that inverts the direction of the
assertion. A cell claims its location via its own label; the location does not name its
cell. Carrying both would be two copies of one fact that can disagree — the failure this
enhancement is named after, reproduced inside its own payload. Per-cell attribution comes
from where the object landed, which the delivery layer already reports per cluster.

Fields deliberately excluded, each additive later if a reader appears: the provider block,
coordinates, location class, and display name. A field earns its place by having a reader at
a cell, not by existing on the source record.

**The vestigial namespace field on location references is free to fix** — every stored value
is already empty, so there is no data migration. One wrinkle: NSO's configuration currently
*errors* when an operator omits it, forcing a meaningless value into every cell's config.
That validation should be relaxed in the same change.

### Schema installation

One bundle entry installs the kind on both the hub and the cells, since the same path is
applied to both.

**The existing Flux `dependsOn` does not suffice, and this is a real finding.** It is
hub-side only — both Kustomizations in that chain apply through the Karmada client
kubeconfig, while the cell-side installation has no `dependsOn` and no hub visibility. Cell
schema ordering is therefore eventual, and a cell missing the schema fails-and-retries
invisibly. That is a fresh instance of "absence looks like correctness," reappearing in the
install path, and it needs its own ordering guarantee rather than an assumption.

## Delivery

**A new cluster label, `topology.datum.net/location`, valued with the location's name, on
every edge cell. One generated propagation policy per location, selecting that one record
and targeting that one label. Kept separate from the existing NSO policy.**

Karmada has no primitive joining a resource label to a cluster label — verified against the
pinned v1.15 API. That forces the topology rather than merely suggesting it, and once
forced, the per-location form wins on every measurable axis.

**Why not the existing labels.** The gateway capability label is on all 22 cells today,
which makes reuse look free and is exactly the trap: it means "this cell runs the gateway
data plane," has no locality content, and is true of all 22 only by coincidence of the
current rollout. The day a compute-only cell exists without it, location delivery stops
silently, and the coupling runs both ways — pausing a gateway rollout would take that cell's
identity with it. The city code is worse: it is deliberately *non-unique* (three cells sit
in us-central-1), so a policy targeting it hands every cell in that city a small catalog
rather than one location, reintroducing the per-site "which entry is me" configuration.

**Why not the Karmada cluster name**, which needs no labelling work at all and deserved the
hearing it got: it inverts the assertion (a platform record would have to know infra's
cluster naming), it is unverifiable from the site side, and a name is a point where a
selector is a set — the moment two cells serve one location, the name form needs a catalog
edit and the label form needs nothing.

**Why "alongside" is not an option.** Adding a location selector to the existing NSO policy
does not produce a cheaper version of this design; it produces a different, rejected one.
That policy has a single placement, so any resource added to it goes to all 22 cells. There
is no arrangement of one policy that yields per-cell scoping.

**Per-location policies are cheaper inside Karmada**, which is counterintuitive enough to
state: 22 policies yield 22 Works. The single-policy alternative yields 22 Works *per
location* — 484 in total — to deliver the same information plus 21 copies nobody should
have.

**One variant to refuse pre-emptively.** A single policy with many `clusterAffinities` terms
looks like fan-out. It is failover ordering: Karmada activates one term at a time. Used as
fan-out it places every location on whichever cell satisfies the first term, while appearing
to work.

**Policies are generated output, not hand-maintained.** If a new location required an infra
pull request to add a policy, this design would have rebuilt its own founding problem — two
records that must agree with nothing checking that they do.

**Exactly one location per cell is structural, and reversible.** A label key holds one
value, so a cell cannot carry two. Enforcement costs nothing and no reader needs a tiebreak.
If many-per-cell is ever needed, the same generator emits the same policies against a
key-per-location form; policy count, ownership, and verification all survive untouched.

### The labelling prerequisite

Every one of the **22** edge cell overlays needs the new label. The 19 missing city codes
should be filled in the same pass — same literal, same line, same reviewer, and one of the
unlabelled cells runs compute and therefore has placement broken today for exactly this
reason.

Two things make this larger than it looks:

- **The city codes are a human determination, not a mechanical edit.** Nobody has written
  down which city 19 of these cells are in. This needs a named owner before scheduling or it
  is what stalls.
- **The labels come from a registration-time job**, so the pass carries a per-cell
  re-registration rather than 22 file edits.

**Incremental is safe; wrong is not.** An unlabelled cell matches no policy, receives
nothing, and falls back to its configured identity — absence, which is recoverable and
self-describing. A *duplicated or wrong* label produces a cell confidently identifying
itself as somewhere it is not, with every layer below behaving correctly on bad input.
Karmada will honour it faithfully. **Uniqueness needs a blocking static check on the
labelling change**, not a runtime alarm.

## The publisher

**A new single-cluster controller in the network services operator, leader-gated, reading
location records from the platform control plane and writing one published copy per location
to the hub.** It reuses the operator's existing hub client rather than introducing one, and
follows the publish-and-prune shape already running in the gateway resource replicator and
the downstream garbage collector.

It touches no project control plane. That single property makes it immune by construction to
the defect that has now stranded three services: that failure needs per-pod state populated
only by a leader-gated runnable *plus* a consumer of that state on every pod. This component
supplies neither.

**Its correctness requirement is the opposite of the engagement fix's.** Cluster engagement
is per-pod bookkeeping and must never be leader-gated. Publishing mutates shared state and
includes a prune, so it must be. Two replicas racing a delete on the object a cell reads to
learn its identity is the outage this enhancement exists to prevent.

**It must sit behind a named controller set.** The operator already carries fourteen
controllers that cannot be switched off, which is the problem NSO #368 exists to solve, and a
cell-side deployment must never start a control-plane publisher. Adding this one un-setted
would make that problem marginally worse at the moment it is being fixed.

Adding a controller here changes the operator's dependency surface not at all — it already
depends on hub availability. The same controller in the service catalog would have added a
hub dependency to a process that also serves admission webhooks, where a slow or unreachable
hub delays readiness and admission fails closed. That is the shape of the incident this
design keeps citing, arrived at from a new direction.

**The rule that generalises.** The lesson from the three prior incidents is not that leader
election is dangerous. It is that the runnable-group router silently supplies a
leader-election answer for any runnable that does not state one, and three teams shipped
runnables whose intent was never declared. So: **every runnable this component registers
declares its leader-election intent explicitly, including where the default is already
correct.** That has teeth for the hub client specifically — its cache would start only on
the leader by accident today, and becomes a trap the moment anything needs to read the hub
on every pod.

**Real watches, no inherited resync.** The existing 5-minute resync exists for a specific
structural reason: its request key carries a cluster name a root-cluster event cannot
supply. This component has no cluster-name dimension — one record in, one hub object out,
single-cluster on both ends — so it gets real watches on both edges, including correction of
out-of-band hub edits. A slow safety resync is fine as self-heal; the design must not depend
on it, and publish latency is measured from source observation to hub write confirmation,
never from a resync tick.

**Sole ownership.** Server-side apply under a dedicated field manager with `force: true` —
the publisher is authoritative and a manual hub edit must not wedge it. Because force hides
conflicts, each pass inspects managed fields and reports a foreign manager as an event and a
metric: same visibility, no wedge.

**Scope that check to spec, not to the whole object.** An earlier draft said "any foreign
manager," which is wrong on a Karmada hub and was caught by running it rather than by any
unit test: the federation controller legitimately co-owns metadata on every published copy,
so the check fires permanently and immediately becomes noise an operator learns to ignore.
Only foreign ownership of spec indicates something contending with the publisher. The prune candidate set is defined **by label**, never
by absence — an unlabelled hub object of the same kind is reported as a conflict, never
deleted. "Delete everything I didn't just write" is how a publisher with a stale cache eats
a fleet.

**There is no owner-reference safety net.** The existing projector leans on Kubernetes
garbage collection to reclaim its output if the controller is wrong or absent. Nothing
equivalent exists across clusters. The prune loop is therefore the *only* deletion path,
which is why the cache-sync gating and the empty-set rule below are load-bearing rather than
defensive padding.

### Failure behaviour

The governing rule is carried verbatim from the existing controllers: a transient read error
requeues; only a resolved not-found changes state.

- **Transient source read failure** — requeue with backoff. Publish nothing, prune nothing.
- **Hub read failure or degraded hub cache** — same, and the prune path hard-disables until
  resync. A prune computed against a partial list is indistinguishable from "delete
  everything."
- **Hub write failure** — requeue that location only. Never a batch that aborts and leaves a
  half-applied prune.
- **Partial success** — publishing is per-location and idempotent, so N−1 successes with one
  failure is a normal reportable state. The successes stand.
- **Leadership lost mid-reconcile** — an in-flight write is benign under apply semantics. A
  delete is not, so deletes re-verify the removal guard from a synced cache immediately
  before issuing.
- **Cold start** — the prune path is hard-gated on cache sync of both informers. A prune
  computed from an unsynced cache deletes everything. This is the existing engagement
  readiness gate translated from "don't admit yet" to "don't delete yet."

**An empty source list is never a valid desired state.** A location list returning zero when
the previous observation was non-zero is treated as a suspected read fault and blocks
pruning entirely. A fleet legitimately reaching zero locations is not a real event, so this
costs nothing and removes the single most destructive failure available to the component.

## Removal and lifecycle

**Stop placing** stays exactly where the enhancement puts it — driven by catalog state, in
the customer's control plane, already working. **Tear down** is an operation. Nothing below
changes that.

The removal guard is implementable, **but only if it fails closed on absence of evidence —
and the naive version is inert today.**

It evaluates against Karmada `Cluster` objects at the hub, which are the only place "this
site exists" is a readable fact. That requires the site-to-location match to be a **label on
the Cluster object** rather than only a selector inside a policy — a policy selector answers
"which clusters does this target," which is the wrong direction. The delivery design above
supplies exactly this, so the requirement is met.

| Evidence | Outcome |
|---|---|
| ≥1 Ready cluster labelled for this location | **Blocked** — names the clusters |
| 0 labelled, and every Ready cluster carries *some* location label | **Proceed** |
| 0 labelled, fleet not fully labelled | **Blocked** — distinct reason |

The third row is the actual content. Without it the guard finds no labelled clusters for any
location during the entire fleet backfill and blocks nothing — decorative, while looking
like protection, exactly when the fleet is least understood.

**Blocking retains rather than deletes.** The published set is deliberately allowed to be a
superset of the catalog. A retained copy is frozen — there is no source to update it from —
and keeps answering "which location is this" for the site still standing behind it, which is
the enhancement's own position that a degraded location's workloads still need to know where
they are.

Two consequences must be designed rather than discovered: a retained copy is **excluded from
the output-versus-input gap metric** — otherwise one blocked removal permanently trips the
primary alert and the alert gets muted, which is how the original failure survived — and is
counted instead into a removal-blocked gauge alerting on a threshold measured in days. This
is a human ordering error, not an outage.

**Override** is an annotation on the published hub object, because that is the only object
surviving the source's deletion. Deliberately two-step: the operator must find the retained
object, which the blocked condition names. Hand-deleting the hub object also works, which is
why the annotation must be the documented path — the hand-delete leaves no record of who
decided or why.

**Decommissioning order**, which the enhancement leaves open:

1. Drain the site.
2. Remove the site from the fleet — unregister the cluster, or at minimum drop its location
   label. **This is the step that changes the guard's answer.**
3. Delete the location from the catalog.
4. The publisher observes, finds nothing behind it, removes the published copy.

Doing 3 before 2 is not an error needing intervention: the publisher blocks, says which
clusters hold it, and clears itself once 2 happens.

### The configuration fallback

**Permanent — demoted, narrowed, and measured.**

Precedence is delivered-then-configured. Location becomes optional when the feature is
enabled, but neither present must not start silently: it reports the enhancement's visible
waiting state and heals when the copy arrives.

Fall back exactly when nothing has been delivered yet, **or** when delivery is ambiguous —
more than one copy present, which under one-location-per-cell is a labelling error where the
correct response is to refuse to guess. Never pick arbitrarily. On conflict between
delivered and configured, prefer **delivered** (it is reconciled and centrally correctable)
but raise a loud mismatch condition — this is verbatim the "two copies disagree and nothing
notices" failure the enhancement exists to kill.

**Transitional, not permanent.** An earlier draft argued for keeping it forever, so a cell
could still identify itself during a delivery freeze. That argument does not survive
scrutiny: a freeze does not delete an already-delivered copy, and a location's identity never
changes, so an existing cell rides a freeze on a stale copy that is still correct. The
fallback earns its place in three narrow cases only — a cell brought up new during a freeze,
a fleet bootstrap before the publisher runs, and the tiebreak when more than one copy is
delivered.

What genuinely needs it is the **fleet labelling migration**, during which cells are not yet
labelled and nothing is delivered to them. That is temporary by construction, so the field
should be too.

The end state is a cell with no location configuration at all: identity is delivered, or the
cell waits visibly and serves nothing. That removes the second copy rather than demoting it,
and deletes the precedence table, the mismatch condition, and the ambiguity tiebreak with it.
"This cell is not ready until it is registered properly" is how everything else on a cell
already behaves.

**The removal trigger is measurable rather than aspirational.** The per-cell
identity-source observable exists anyway, to stop a fallback becoming a hiding place. It
doubles as the retirement gate: when no cell has reported `Configured` for a sustained
period, the field goes. A deprecation with a metric behind it retires itself; one with a date
behind it does not.

**Two asks on NSO #368.** Lift location out of the network-interface block into the cell-set
configuration, since more than one cell controller will want the cell's identity. And extend
its validation stance: enabling the cell set with neither delivered nor configured identity
must be a reported waiting state, not a boot-time validation failure — failing at boot makes
a cell crash-loop whenever delivery is merely late, which is strictly worse than waiting
visibly.

## Detection

**Stage zero, before anything else: the service catalog is not scraped at all.** Milo,
compute, and the datum controller-manager have scrape configs; this component has none. It
also exports no custom metrics today, and the shared multicluster provider exports none
either. Every signal below is inert until that lands — which is precisely why nobody could
ask the question the enhancement says they must be able to ask.

**The output-versus-input pair, sampled in one pass.** The existing reconciler already
computes its desired set and already lists what it owns, in the same function, at the same
instant — and throws both away. Export them as a pair after the prune, aggregated by
default with per-customer detail behind a debug endpoint rather than a label, mirroring the
programmed-set endpoint the config-dump parity work already established. Per-customer labels
at production counts are a cardinality bomb.

Alert on a sustained non-zero gap over three resync cycles, with a hold that follows the
house idiom for low-volume controllers. Because both numbers come from one pass ending in a
prune, the pair is internally consistent by construction — there is no window where a
mid-flight write makes observed legitimately trail desired. One hazard: a disengaged
customer's contribution must be **deleted** from the gauge, not left at its last value, or a
dead publisher shows a permanently healthy-looking zero gap.

**The canary, which is what the gap check cannot do.** One provisioned customer, in staging
*and* production, with a pinned entitlement to a known enumerated non-empty set. Zero is
never correct there, and the expected count is a constant. This converts an undecidable
property into a decidable one for the cost of a single fixture.

**The signal that would have caught the original outage in ~10 minutes** is a per-replica
engaged-clusters gauge — and it belongs in the **shared Milo library**, not in the service
catalog. The evidence in the original issue was gathered by hand: the leader had 1840
engagement log lines and zero errors, the non-leader had zero engagements and 38 denials.
That asymmetry *is* the signal; it was invisible only because nobody turned it into a
number. A min-is-zero-while-max-is-positive expression across replicas fires within ten
minutes of a rollout reintroducing the defect, is not masked by retries succeeding on the
leader, and is exactly the "some replicas, not others" shape that made it look dismissible.
The same defect has now hit compute and the service catalog, and NSO imports the identical
provider — fixing the observability once, upstream, is the only version that does not recur
a fourth time.

**Error-rate alerting is structurally blind here** and should not be mistaken for a
detector. The watch is sourced *from* the engaged clusters, so zero engagements means zero
events, zero reconciles, and zero samples of any result. The series vanishes rather than
going red. Keep it as a secondary signal for the write-failure case only.

**Attribution is Alertmanager inhibition keyed on cluster, not a dashboard.** Fleet-wide
delivery freeze suppresses the per-cell alerts; a single unhealthy cell suppresses that
cell's. The operator sees one alert whose name states the layer. Delivery freshness reuses
the **existing** propagation canary and its three tuned, runbooked alerts rather than
minting a second one with different numbers.

One row needs work from the schema side: a missing schema on a cell presents as
work-not-applied, identical to a stalled agent, unless the not-applied **reason string** is
surfaced as a label. Without it the operator reads the wrong runbook.

**Per-location freshness at the publisher is the wrong shape** and is declined: the
reconciler is keyed on entitlement, not on locations, so there is no per-location publish
event to timestamp, and the gauge would be locations × customers reporting one number
repeatedly. Freshness is per-customer at the publisher and per-cell at delivery.

### Fixing the existing health check

It asserts on the wrong resource, in a project holding none of either kind, with an identity
that cannot read either. Rewrite it against the canary, asserting an **exact expected set**
rather than at-least-one, each entry available with the all-gates-open reason, and each
carrying a non-empty city code so a publisher writing empty husks fails rather than passes.

**Add the negative case in the same suite** — an unentitled customer holds exactly zero.
Positive and negative together are what let the *suite* distinguish "nothing to do" from
"not doing anything." Without the negative case, a publisher projecting everything to
everyone passes.

The two-month hunt for a cluster-scoped grant was chasing something that cannot exist: the
protected resource lists the project as its only parent, so the grant is project-scoped.
Un-skipping requires the binding observed **Ready** (a dry-run will not catch a dead role
reference, since that is checked after admission), the fixtures GitOps-provisioned rather
than hand-created, the expected set derived from the fixture rather than hardcoded, and —
because scheduled runs target production — **the fixture existing in production**, which is
an open question in the tracking issue and must be answered before un-skipping rather than
after.

Also in scope, two lines: the compute error message that says *Locations* while reading
*LocationBindings*, and states an infrastructure gap as a fact about the catalog. That
message is the misleading symptom the enhancement's last requirement is about.

## Verification

**Rung 1 — unit and rule tests.** Proves the pair comes from one pass, that gates evaluate
correctly, and that thresholds and holds behave under synthetic series. NSO has this
harness and runs it on every push. **The infra repo does not** — hand-written rules there
get YAML and kustomize validation only. So where a rule lives determines whether its
expression is ever tested. Either site these rules where the harness exists or add it;
shipping an untested expected-versus-actual expression into a repo with no idiom for it is
how you get an alert that never fires.

**Rung 2 — the two-cluster environment.** The only place with real federation, and per its
own README it exists *because incidents lived in the federation layer*. It can prove: a
published record selected by a per-location policy lands on the member and is readable
there; the per-cluster applied acknowledgement appears and the count invariant holds; the
schema-missing case yields a *distinguishable* reason, testable by deleting the schema; and
the negative direction — suspend the policy, confirm the gap and freshness signals move as
designed.

**What it cannot prove, stated plainly.** The environment joins its member in **push mode**;
all 22 cells run **pull-mode agents**. The entire agent failure class — including the
CA-mismatch freeze that took 13 of 21 edges — is not reproducible there, and the fleet's own
per-cell readiness signal is agent-emitted, so it does not exist in the environment at all.
**This is the largest fidelity gap in the plan.** Switching the environment to registered
pull mode would close it, and the environment's stated reason for existing is the argument
for doing so. It also has no Milo, no metrics stack, and one member cell, so the publisher
half of the chain, alert firing, and per-cell attribution are all absent.

**Two additions, in priority order.** Adding Milo is cheap and decisive — the existing
bundle-fetch tooling is already generic over repository and digest and already pulls a
sibling bundle the same way, and Milo runs from its published bundle without the identity
stack. That makes the entire chain present in one environment for the first time. A minimal
metrics stack is second: it upgrades assertions from "the metric moved" to "the alert fired."

**The minimum convincing assertion.** In an environment holding a real project control
plane, a real hub, and one member cell: create a location and an active entitlement for the
canary. Within one resync interval, assert the expected projection exists with a non-empty
city code, the published record is readable on the member cell, and the output-versus-input
pair is equal and non-zero. **Then delete the entitlement and assert all three return to
zero.** The delete half is not decoration — it is what proves the signals track reality
rather than being wired to look healthy.

**Run it at one replica and at two with leader election on.** The two-replica run is the
regression test for the defect itself. It recurred precisely because no test ever ran with
more than one replica, and the fixing PR's own test plan left the multi-replica verification
unchecked. Making two replicas the default in the environment is a one-line change worth
more than any assertion.

**What only production can prove**, honestly: that 22 real pull-mode cells across real WAN
links converge; that per-cell attribution holds under a real agent freeze; that the
thresholds suit production churn; and metric cardinality at production scale. For the
thresholds — the one item genuinely unverifiable pre-merge — the compensating control is to
ship every new alert at warning severity to a non-paging channel for a full week, review
firing history against known-good periods, then promote.

## Rollout

Rides the existing edge channels rather than inventing a grouping. **Every stage is gated on
signals that exist by stage 0** — the detection ships before the thing it detects.

| Stage | Scope | Gate to advance |
|---|---|---|
| **0. Observability** | Scrape the service catalog; ship the engagement gauge and publisher pair; alerts warning-only. **Nothing published anywhere.** | Gauges visible in both environments; engagement gauge equals project count on **every** replica; one clean week on the warning channel. |
| **1. Canary + test** | Canary fixtures in staging and production; un-skip the rewritten health check. | Green on scheduled runs in both for a week, **including** the negative case. |
| **2. One lab cell** | Publish to a single staging lab cell. | Count invariant holds; applied ack observed; the site reads its own identity; the fallback demonstrably still works with delivery suspended. |
| **3. Beta labs** | First multi-cell. | **Deliberately break one cell**; confirm the alert names *that* cell and that the fleet alert inhibits rather than duplicates it. The only stage where attribution is genuinely tested. |
| **4. Production, in tranches** | One region, then a third, then the rest. Never all at once. | Per tranche: fleet alert quiet, per-cell ack at 100%, no gap growth. |
| **5. Promote to paging** | Warning → critical. | A clean full-fleet week. Retiring the fallback is **not** part of this rollout. |

Stage 0 is the one to defend hardest: it ships before any new delivery path exists, and on
its own it would have caught the original defect.

## Compute

Unchanged from the enhancement in substance. The site-side resolution becomes an identity
read against the new kind rather than a catalog search; the first-generation instance
location defect is fixed by resolving before instance actions are computed, with a backfill
for instances already created without one; the vestigial namespace comparison goes; and the
missing city codes are filled in the labelling pass.

One addition from design: because the published kind is distinct rather than a mirror,
compute's existing unfiltered list-and-take-first **will not silently appear to work** during
migration. It fails loudly, which is the desired outcome and should be sequenced
deliberately rather than discovered.

## Questions resolved

| Enhancement question | Resolution |
|---|---|
| Published form | New cluster-scoped `ServingLocation`; no status subresource; name, `spec.topology` (city code CEL-required), `spec.source` freshness markers |
| Site-to-location matching | New `topology.datum.net/location` cluster label valued with the location name |
| Schema installation and ordering | One shared bundle entry; existing dependency chain is hub-only and insufficient for cells |
| Delivery configuration | One generated policy per location, separate from the existing NSO policy |
| Detection signals | Output-versus-input pair **plus** a canary; per-replica engagement gauge upstream; inhibition-based attribution |
| Availability decision published form | Deferred behind a named hub-side placement reader; four checkable preconditions |
| More than one location per cell? | No — structural via single label key, reversible if ever needed |
| Is the fallback permanent? | No — transitional for the labelling migration; retires when no cell reports `Configured` |
| Decommissioning order | Written down; guard blocks and self-clears rather than erroring |

## Questions still open

- **Who owns filling in the 19 missing city codes.** A per-cell human determination with no
  written source. Unowned, this is what stalls the labelling pass.
- **Whether the test environment moves to pull mode.** The environment owner's call, but it
  is the largest fidelity gap and the environment's own stated purpose argues for it.
- **Where the alerting rules live**, given only one of the two candidate repos has a rule-test
  harness.
- **Whether production holds the fixtures** the rewritten health check needs. Open in the
  tracking issue; must be answered before un-skipping.
- **Who creates locations, once anyone does.** Taken as hand-authored throughout. Fanning a
  hand-authored catalog to a fleet raises the cost of a typo, which strengthens the locations
  proposal's own direction.
