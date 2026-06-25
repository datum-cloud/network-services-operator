# Federation

The Datum edge runs across many clusters in many regions. Customers, though,
work against a single control plane: they create a Gateway, a route, or a
firewall policy in one place. **Federation is what carries that intent out to
the edge clusters that actually serve traffic.**

This directory holds the federation configuration the test environment applies,
mirrored from production so the test edge fans configuration out the same way
the real one does.

## Why it's tested as its own concern

For most of this system's history, the test environment copied configuration
between clusters with a simple direct mechanism — nothing like production. But
several real incidents lived specifically in the federation layer: some
information (a backend's online/offline status) is intentionally *not* carried
to the edge, and the timing of cross-cluster delivery created races. None of
that is visible unless the test edge federates the way production does.

So the production-fidelity environment stands up real federation and proves the
thing customers depend on: **configuration created in the control plane actually
arrives at the edge.** The test confirms a change made centrally shows up on a
downstream cluster within seconds.

## What's here

- A propagation policy describing *which* resources travel to the edge.
- Interpreter rules describing *how* each resource type is carried — including
  the deliberate choice to propagate configuration but not live status, which is
  the behavior that caused real "false offline" incidents and is now exercised
  directly.

## Implementation

Federation is implemented with [Karmada](https://karmada.io/). The directory is
named for the responsibility — fanning configuration out to the edge — rather
than the tool, so the intent stays clear even if the underlying mechanism
changes. The environment that applies these artifacts is described in
[`Taskfile.test-infra.yml`](../../Taskfile.test-infra.yml) (`task
test-infra:karmada-up`).
