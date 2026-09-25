# ALB agent capability

| | |
|---|---|
| **Status** | Implemented |
| **Author** | Engineering |
| **Created** | 2026-09-22 |

## Summary

Networking publishes knowledge, skills and read-only tools to the Datum
assistant, so a project entitled to `networking.datumapis.com` gets an assistant
that speaks about Application Load Balancers in product terms and can diagnose
one that is not serving.

This is the provider side of the agent framework, the same shape compute and DNS
already ship: a capability document names the URLs, entitlement decides which
projects receive them, and the assistant composes a conversation from exactly
the services a project has.

## Motivation

Without this, the assistant can call its generic base tools against
`networking.datumapis.com` and hand the model raw YAML. That fails in a specific
way: **this API cannot be read naively**. A straight reading produces answers
that are confident and wrong.

- The top-level conditions aggregate over a set of hostnames and name no member
  of it. `DNSRecordsProgrammed=PartialFailure` means "one or more", and which
  one is two objects away. An assistant that reports the aggregate has told a
  customer something is wrong and left them to find out what.
- `HostnamesInUse` is true when something is wrong. Every other condition here
  is true when it is healthy, so the ordinary reading marks a hostname collision
  as fine and drops it.
- `DNSZoneNotFound` and `NotApplicable` are set true to mean "Datum does not run
  this domain's DNS" — a normal arrangement. Reporting them as faults invents a
  problem; reporting them as pending tells the customer to wait for something
  that is never coming.
- The same reason word means different things on different conditions.
  `Pending` is three different answers.

None of that is discoverable from the schema. It is exactly what a provider is
supposed to publish.

## What is published

| Contribution | Where |
|---|---|
| Knowledge | `docs/agent/llms-full.txt` — orientation and how to read status |
| Skills | `docs/agent/skills/*.md` — ten procedures, loaded on demand |
| Tools | `alb_list`, `alb_get`, `alb_diagnose`, `alb_reason_explain` |
| Server | `cmd/alb-mcp`, deployed from `config/components/alb-mcp` |

`internal/agent` holds the reason catalog and the diagnosis walk behind the
tools.

## Decisions

**No mutating tool.** Changing a load balancer goes through the assistant's own
plan and apply path, which holds the plan token and the one confirmation step
every service shares. A write tool here would duplicate that and bypass the
token. This is enforced structurally: the tools reach the cluster only through a
`Reader` interface with no write on it.

**The catalog is keyed on `(conditionType, reason)`.** Reason strings are not
unique in this API. A map keyed on the reason alone answers every `Pending` with
whichever was registered last — the wrong-answer-that-reads-right failure the
catalog exists to prevent, arriving through the catalog itself.

**Root causes sort by scope before depth.** Ranking by depth alone surfaces a
traffic protection fault above a dead origin, and answers "your WAF is not
attached" to someone whose site is returning nothing.

**Confidence is a separate axis from actionability.** Actionability answers who
acts; confidence answers how much is known. Folding the second into the first is
how a load balancer that reports everything true, and serves nothing, comes back
as healthy.

**No convergence window is applied.** See below.

**The walk stops at the DNS zone.** On `DNSAuthorityMissing` or
`DNSZoneNotReady` it names the boundary and hands off rather than
re-implementing zone diagnosis, which would drift and which a project not
entitled to DNS cannot use anyway.

**`spec` is the single source of the product model.** The agent decodes routes,
origins, Force HTTPS, display names and protection through
`internal/cmd/alb/spec`, the same package the `alb` plugin reads, so the CLI and
the assistant cannot describe one load balancer differently. Making that
possible needed `spec` split away from the datumctl plugin host first; see the
`plugincli` package.

## Three things the platform does not report

These are limits this design works within, not bugs it routes around. The
published documents state them with issue numbers, and two tests are written to
**fail** once the platform publishes the signals, so a fix cannot leave a silent
workaround behind.

**Whether the edge can serve a load balancer.** Conditions go true within
seconds of create while the edge is still catching up, and nothing publishes
when that finishes ([#457]). No convergence window is documented anywhere, so
none is applied: guessing one would be exactly the unfalsifiable claim the rest
of this design avoids, and would make a load balancer that never serves look
healthy the moment the guess elapsed. A clean result is reported as `unverified`
and the summary names the request that would settle it.

`EdgeReachability` is not the substitute. It is hub-side, holds no status, and
records which addresses an edge *should* reach rather than whether any does.

**A generated hostname's DNS record.** No condition is published for it at all
([#458]), so its absence carries no information. The step is reported as
`notReported`, which is neither pass nor fail.

**Domain ownership, per hostname.** `HostnameConditionVerified` is declared in
`api/v1alpha` and no controller writes it — `Available`, `DNSRecordProgrammed`
and `CertificateReady` are written by the controllers; `Verified` is not.
Ownership is read from the `Domain` covering the hostname instead. Anything
waiting on the hostname's own condition waits forever. This has no issue filed
and is worth one.

[#457]: https://github.com/datum-cloud/network-services-operator/issues/457
[#458]: https://github.com/datum-cloud/network-services-operator/issues/458

## Keeping it honest

The risk with a knowledge base is that it drifts from the API and nobody
notices, because nothing fails. Five tests make specific drifts fail:

- A reason added to `api/v1alpha` without a catalog entry.
- A new file in `api/v1alpha` that nobody has argued in or out of scope.
- A reason catalogued against a condition the controllers do not set it on,
  which otherwise reads at runtime as an uncatalogued reason.
- Customer-facing copy using a word that only exists inside the implementation,
  each ban carrying its own argument so the list can be disputed.
- A skill named by a cause but not published, or published and pointed at by
  nothing.

## Not in this change

- An access-log tool. `internal/cmd/alb/util/o11y_logs.go` already queries the
  project logs API on the same client, so the code cost is small, but it is a
  second API and a second entitlement: a caller entitled to networking may not
  be entitled to observability. It should return "unavailable" rather than
  erroring, and `alb_diagnose` must never call it implicitly or a logs outage
  makes load balancers undiagnosable.
- A render tool. The encodings are undiscoverable from the schema, which argues
  for one, but `alb-create` covers the procedure and `schema_get` covers the
  fields. Worth revisiting once there is evidence the model gets the Force HTTPS
  or protection encoding wrong.
- Weighted backends and load-balancing algorithm, which are still in flight.
  When they land, `alb_get` and the knowledge document must show them, or the
  assistant will describe a traffic split wrongly — which is worse than not
  describing it. Because everything decodes through `spec`, that is one package
  to follow.
