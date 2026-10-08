---
status: provisional
stage: alpha
---

# Severity Floor for Controller Error-Ratio Alerts

- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [User Stories](#user-stories)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
  - [Rule change](#rule-change)
  - [Choosing the floor](#choosing-the-floor)
  - [Test fixtures](#test-fixtures)
  - [Runbook update](#runbook-update)
- [Open Decisions](#open-decisions)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)

## Summary

`ControllerReconcileErrorRatioCritical` pages on-call whenever a controller's
reconcile error ratio exceeds 50%, regardless of how much work the controller is
actually doing. A controller retrying a handful of permanently-failing objects —
a few errors per ten minutes, nothing else in its queue — produces a 100% ratio
and pages with the same urgency as a controller failing hundreds of reconciles
of live customer traffic.

This enhancement adds an **absolute error-rate floor to the critical tier
only**: critical requires both a >50% error ratio *and* a sustained error rate
above a small absolute threshold. Below the floor, the same condition still
fires `ControllerReconcileErrorRatioHigh` (warning), which has no floor. A
broken-but-idle controller stays visible; it stops paging.

This is the remaining half of
[#326](https://github.com/datum-cloud/network-services-operator/issues/326).
The flapping and missing-label halves shipped in
[#333](https://github.com/datum-cloud/network-services-operator/pull/333)
(v0.25.2).

## Motivation

The ratio expression has no notion of volume. Cases measured on real clusters
during #326:

| Controller | Error volume | Ratio | Paged critical? | Right outcome |
| --- | --- | --- | --- | --- |
| `dnsrecordset-powerdns` (prod) | 193 errors / 10m | 24–100% | yes | yes — real workload failing |
| `instance-projector` (prod) | ~4 errors / 10m, 12 reconciles/h | 100% | yes, ~100 fire/resolve pairs in 14h | no — 3 orphaned objects, dead data |
| Grafana operator CRs (prod) | 1 error / 15m each | 100% | yes | no — dead data, one object each |

The `instance-projector` pages were pure noise for on-call: the underlying
fault ([datum-cloud/compute#194](https://github.com/datum-cloud/compute/issues/194))
was three orphaned Instances that drive no workload, and no amount of paging
urgency changes how such a fault gets fixed. #333 stopped the *flapping* — the
alert now latches instead of cycling — but a sparse, permanently-failing
controller still lands in the on-call escalation path at `critical`.

An earlier proposal on #326 — a minimum-volume guard on the alert itself — was
rejected with measurement: it would not have suppressed any of the pages caused
by the high-volume flapping cases, and it would have *entirely silenced*
`instance-projector`, a controller genuinely failing 100% of its reconciles.
The problem with low-volume failures is not that they alert; it is that they
page.

### Goals

- A controller failing most of a real workload continues to page on-call,
  unchanged.
- A sparse controller failing all of a tiny workload fires `warning`, stays
  latched, and remains visible on dashboards and in Slack — without paging.
- The boundary between the two is an explicit, tested threshold, not an
  emergent property of traffic.

### Non-Goals

- Suppressing low-volume failures entirely. The warning tier deliberately keeps
  no floor.
- Changing the ratio thresholds (20% / 50%), windows, or `keep_firing_for`
  behaviour shipped in #333.
- Resolving the rule's scope/ownership question — the rule still matches every
  controller-runtime binary scraped into the cluster, and an `nso-slo`-owned
  alert still fires about other teams' controllers. This proposal shrinks the
  paging blast radius of that problem but does not decide ownership. Tracked on
  #326.
- Alertmanager routing or grouping changes (infra repository).

## Proposal

Add a second condition to `ControllerReconcileErrorRatioCritical`: the
controller's absolute error rate over the same 30-minute window must exceed a
floor, defaulting to **1 error per minute**. `ControllerReconcileErrorRatioHigh`
is unchanged.

The severity split becomes:

| Condition | Sparse failures (below floor) | Volume failures (above floor) |
| --- | --- | --- |
| ratio > 20% | warning | warning |
| ratio > 50% | warning | **critical** |

### User Stories

- **As the on-call engineer**, I am paged only when a controller is failing at
  a rate that indicates real workload impact, so a page always warrants
  immediate attention.
- **As the team owning a controller with a few permanently-failing orphaned
  objects**, I see a latched warning naming the controller, cluster and
  namespace, so the fault is visible and attributable without waking anyone.

### Risks and Mitigations

| Risk | Mitigation |
| --- | --- |
| A genuinely urgent failure sits just under the floor and only warns. | The warning still fires and latches; the floor is set well below any measured real-workload failure rate (see [Choosing the floor](#choosing-the-floor)). Warning routing still reaches Slack. |
| A controller's error rate oscillates around the floor, flapping between severities. | Both tiers keep `keep_firing_for: 30m`, which holds each alert through dips of either the ratio or the rate. |
| The floor value goes stale as workloads grow. | The value is a named constant in one rule file with fixtures asserting behaviour on both sides of it; revisiting it is a one-line change with test coverage. |

## Design Details

### Rule change

`config/telemetry/alerts/controller-health.yaml`, critical rule only:

```yaml
- alert: ControllerReconcileErrorRatioCritical
  expr: |
    (
      sum by (cluster, namespace, controller) (rate(controller_runtime_reconcile_total{result="error"}[30m]))
      /
      sum by (cluster, namespace, controller) (rate(controller_runtime_reconcile_total[30m]))
    ) > 0.5
    and
    sum by (cluster, namespace, controller) (rate(controller_runtime_reconcile_total{result="error"}[30m])) > 0.0166
  for: 10m
  keep_firing_for: 30m
```

`0.0166` errors/second ≈ 1 error/minute ≈ 30 errors per 30-minute window. The
`and` keeps the ratio as the alert value, so annotations and dashboards continue
to show the percentage.

The warning rule is untouched.

### Choosing the floor

The measured cases separate by nearly two orders of magnitude:

| Case | Error rate | vs. 1/min floor |
| --- | --- | --- |
| Grafana operator CRs | ~0.07/min | far below — warning |
| `instance-projector` | ~0.4/min | below — warning |
| staging `gateway` (48h average) | ~0.8/min | borderline — see [Open Decisions](#open-decisions) |
| `dnsrecordset-powerdns` | ~19/min | far above — critical |

1 error/minute sits in the gap: comfortably above any retry loop over a handful
of dead objects (controller-runtime's capped backoff retries a single object at
most every ~16 minutes), and comfortably below a controller failing a real
workload.

### Test fixtures

`test/prometheus-rules/controllers/` gains cases asserting the split, run in CI
by `test-prometheus-rules.yaml`:

- The existing sparse `instance-projector` fixture (4 errors / 20-minute wake,
  100% ratio) asserts **warning fires, critical does not** — inverting today's
  expectation that both fire.
- A volume-failure fixture (errors well above 1/min, ratio > 50%) asserts
  critical still fires with the ratio as its value.
- A boundary fixture on each side of the floor pins the constant, so changing
  it is a deliberate act that shows up in a test diff.

### Runbook update

`docs/runbooks/controller-health.md`:

- Critical "Meaning" gains the floor: >50% of attempts failing *and* more than
  ~1 error/minute sustained.
- A note in the style of the existing "Alert timing" section explains that a
  sparse 100%-failing controller warns rather than pages by design, citing the
  `instance-projector` case the way the runbook already cites compute#194.

## Open Decisions

- **Exact floor value.** 1 error/minute is derived from the cases measured on
  #326. Staging `gateway`'s 48-hour *average* (~0.8/min) is just under it, but
  that average mixes healthy and failing periods; during its failing windows
  the rate was higher. Before merge, measure error rates across current prod
  and staging failing windows and confirm the gap holds. The fixtures make the
  chosen value explicit either way.

## Drawbacks

- A real fault confined to a small number of objects never pages, even if those
  objects matter (the runbook's orphan guidance already notes gateway orphans
  can serve live traffic). The warning tier and dashboards are the backstop;
  anything needing stronger guarantees deserves its own object-level alert
  rather than a ratio alert.
- Two conditions in one expression are harder to read than one. Mitigated by
  the rule-file comment and fixtures.

## Alternatives

- **Minimum-denominator guard** (require N total reconciles before alerting).
  Rejected on #326 with measurement: silences genuinely-failing sparse
  controllers entirely and would not have prevented the measured pages.
- **Alert on absolute error rate only, dropping the ratio.** Loses the
  distinction between a controller failing 5% of a busy workload (fine) and
  90% of it (outage); the ratio is the right primary signal.
- **Demote via Alertmanager routing** (route low-volume criticals away from
  paging). Puts the policy in a different repository from the rule and its
  tests, invisible to promtool; the severity decision belongs where it is
  tested.
- **Do nothing.** dns-operator#75 removed one error floor and `instance-
  projector` will be fixed by compute#194, but #326 shows each fixed controller
  is replaced by the next — the Grafana CRs are already the current instance.
