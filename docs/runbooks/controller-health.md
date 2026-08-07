# Runbook: Controller reconcile-error alerts

These alerts fire when any controller-runtime reconciler's error rate is
persistently high. A reconcile error means the controller attempted to write or
update an object and was rejected — by the API server, an admission webhook, or
a CRD validation. While errors persist, whatever that controller manages is
frozen at its last successfully written state; new intent does not reach its
target.

Every controller in the operator emits the standard controller-runtime counter
`controller_runtime_reconcile_total`, labelled with `controller` (the reconciler
name) and `result` (`error` / `success`). These alerts group by `cluster`,
`namespace` and `controller`, so each reconciler is evaluated independently per
deployment and the firing alert names both the offending reconciler and where it
is running.

The counter is emitted by every controller-runtime binary scraped into the same
Prometheus, not only by this operator. A firing alert may therefore name a
controller belonging to another component — read the `namespace` label before
assuming the problem is in NSO.

## Alert timing

Both alerts evaluate a 30-minute rate window and hold with `keep_firing_for: 30m`.
Two consequences while you are working an incident:

- **A fix does not clear the page immediately.** The alert keeps firing for up to
  30 minutes after reconciles start succeeding again. A still-firing alert shortly
  after a fix is expected and is not evidence the fix failed — confirm recovery
  from the error ratio itself, not from the alert state.
- **A new failure takes longer to page.** A controller that was healthy and then
  fails outright reaches the critical threshold in roughly 26 minutes rather than
  16. The wide window is what stops a low-volume controller flapping; detection
  latency is the price.

## Shared diagnosis

The `cluster` and `namespace` labels tell you where to look; the `controller`
label tells you which reconciler is failing. Find the specific objects and error
messages in the controller logs:

```sh
kubectl -n <namespace> logs -l <controller-selector> | grep 'Reconciler error' | grep '"controller":"<controller>"'
```

Each error line names the namespace and name of the object that failed and the
reason the write was rejected. Group the messages — a single recurring error
across many objects points to a systemic cause (RBAC, admission, API server); a
single object failing repeatedly points to bad input for that object.

Common error classes:

- `Required value` / other CRD validation errors — the controller is producing
  an object the schema rejects. Example: the gateway controller withholds every
  listener whose TLS certificate is unusable, and a Gateway with zero listeners
  is rejected as `spec.listeners: Required value`; `GatewayListenerCertUnusable`
  then fires for the same gateway. See
  [issue #212](https://github.com/datum-cloud/network-services-operator/issues/212)
  and [PR #217](https://github.com/datum-cloud/network-services-operator/pull/217).
- `Forbidden` / `Unauthorized` — a permissions regression; check the operator's
  RBAC for that resource.
- `conflict` / `ResourceVersion` — transient write conflicts that resolve on
  their own and should not sustain a high error rate.
- `not found` for a parent or cluster that no longer exists — the object is
  orphaned and can never reconcile, so it retries forever and pins the ratio at
  100% with no successes to dilute it. Project deletion is the usual trigger
  ([milo-os/milo#750](https://github.com/milo-os/milo/issues/750)). **Do not
  assume an orphan is inert** — it depends on what the controller programs:
  - Compute orphans are dead data; an orphaned Instance drives no workload
    ([datum-cloud/compute#194](https://github.com/datum-cloud/compute/issues/194)).
  - Gateway orphans are not. Mirrored Gateways and DNS records keep propagating
    through Karmada, so a deleted project's listener can still serve live traffic
    ([#338](https://github.com/datum-cloud/network-services-operator/pull/338)).

  Establish which case you have before calling the alert benign, then remove the
  orphan.

## ControllerReconcileErrorRatioHigh

**Meaning (warning).** More than 20% of the named controller's reconcile
attempts are returning errors, sustained for 15 minutes. The reconciler is
struggling to write its objects and some intent is not reaching its target.

**Impact.** Whatever the controller manages is not receiving updates. The last
successfully written state continues to run.

**Diagnose.** Use the `controller` label to scope the log search (see Shared
diagnosis) and identify the failing objects and the rejection reason.

**Remediate.** Fix the root cause identified in the logs — correct the object
that fails validation, restore the missing permission, or roll back the change
that introduced the regression. The error ratio recovers automatically once
writes succeed again, though the alert itself holds for up to 30 minutes longer
(see [Alert timing](#alert-timing)).

## ControllerReconcileErrorRatioCritical

**Meaning (critical).** More than 50% of the named controller's reconcile
attempts are failing, sustained for 10 minutes. The reconciler has effectively
stopped applying changes.

**Impact.** Treat as an active outage for anything this controller programs. No
updates are being applied; consumers see whatever was in place before the errors
began (stale routing, un-rotated certificates, incorrect status).

**Diagnose.** Follow the steps in
[ControllerReconcileErrorRatioHigh](#controllerreconcileerrorratiohigh). At this
error rate the cause is usually systemic — determine whether one object is
failing or many, which distinguishes a single bad input from a broad regression.

Rule out an upstream problem:

```sh
kubectl get --raw /healthz
```

**Remediate.** Fix the root cause as for the warning tier. If one object's
validation failure is blocking the rest, correcting it unblocks the queue. If an
API server or permissions change caused a broad failure, roll it back and verify
the controller regains a healthy reconcile ratio. Judge recovery by the ratio,
not the alert state — the alert holds for up to 30 minutes after the ratio
recovers (see [Alert timing](#alert-timing)).
