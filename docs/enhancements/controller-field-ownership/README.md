---
status: implementable
stage: alpha
---

# Controller Field Ownership

- [Summary](#summary)
- [Motivation](#motivation)
- [Proposal](#proposal)
- [Risks and Mitigations](#risks-and-mitigations)
- [Rollout](#rollout)
- [Test Plan](#test-plan)
- [Open Decisions](#open-decisions)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
- [Evidence](#evidence)

## Summary

| | |
| --- | --- |
| Problem | The operator's controllers reject each other's saves: *"the object has been modified"*. The rejected controller backs off and tries again. |
| Cause | Two controllers change different parts of one object, but each save sends the whole object with the `resourceVersion` it read. |
| Proposal | Each controller saves only its own part, without `resourceVersion`. |
| Patterns from | kubernetes-csi, cert-manager, controller-runtime, Gateway API |

## Motivation

### Issues this addresses

| issue | what it reports | phase | confidence |
| --- | --- | --- | --- |
| [#305](https://github.com/datum-cloud/network-services-operator/issues/305) | the Gateway's two writers reject each other | 2 | direct |
| [#329](https://github.com/datum-cloud/network-services-operator/issues/329) | the replicator's status saves fail on a conflict | 1 | direct |
| [datum-cloud/infra#3930](https://github.com/datum-cloud/infra/issues/3930) | controllers fail a large share of their reconciles | 1-3 | direct |
| [datum-cloud/infra#3881](https://github.com/datum-cloud/infra/issues/3881) | *"A customer's gateway settles when two writers touch it, instead of looping on conflicts (#305, #329)"* | 1-2 | direct |
| [datum-cloud/infra#3828](https://github.com/datum-cloud/infra/issues/3828) | a BackendTrafficPolicy's status arrives late in e2e | 1 | likely; the issue names #329 as the cause, not confirmed |
| [#326](https://github.com/datum-cloud/network-services-operator/issues/326) | the reconcile error-ratio alert flaps | 1-3 | partial |
| [#295](https://github.com/datum-cloud/network-services-operator/issues/295), [datum-cloud/infra#3622](https://github.com/datum-cloud/infra/issues/3622) | HTTPProxies take long to stand up | 1-3 | one cause among several |

Earlier attempts: [#169](https://github.com/datum-cloud/network-services-operator/pull/169) and
[#303](https://github.com/datum-cloud/network-services-operator/pull/303) retried on conflict;
[#170](https://github.com/datum-cloud/network-services-operator/pull/170) proposed server-side apply and was closed
for an unrelated fix.

### The numbers

Rejected saves (`409`) per resource, 24 hours to 2026-10-09 14:34Z, from
`sum by (job, resource) (increase(apiserver_request_total{code="409"}[24h]))`:

| resource (`milo-apiserver`) | 409 |
| --- | --- |
| httpproxies | 1,871 |
| gateways | 1,452 |
| httproutes | 1,088 |
| networkinterfaces | 545 |
| backendtlspolicies | 517 |

The count includes every client. Each phase takes its own baseline per resource and verb.

### Why it happens

| object | writers | what each saves |
| --- | --- | --- |
| HTTPProxy | HTTPProxy controller; replicator | a finalizer each; the HTTPProxy controller also the status |
| Gateway | HTTPProxy controller; gateway controller | the spec; the default listeners' hostname, a finalizer and the status |
| Connector | connector controller; iroh-dns controller; replicator | conditions, `leaseRef`; condition `IrohDNSPublished`, a finalizer; a finalizer |

Every save changes `resourceVersion`, so any of these saves can reject another.
A rejected save is retried with a growing delay, up to 1000 s
([`controller.go:483-494`](https://github.com/kubernetes-sigs/controller-runtime/blob/v0.24.1/pkg/internal/controller/controller.go#L483-L494)).

### Goals

- A save is not rejected because another controller changed a different part of the object.
- No finalizer is lost, and no object is left stuck in deletion.
- It works on objects saved before the change, during a rollout, and after a rollback.

### Non-Goals

- Saves on the Karmada hub.
- Writers outside the operator: users, the portal, `datumctl`, the connector agent, dns-operator.
- HTTPRoute `status.parents`: one controller writes one entry per parent Gateway into an `atomic` list.

## Proposal

| a controller changes | it saves | taken from |
| --- | --- | --- |
| its finalizer | a JSON patch that tests the list, then adds or removes its own entry | kubernetes-csi [external-attacher](https://github.com/kubernetes-csi/external-attacher/blob/v4.13.0/pkg/controller/util.go#L229-L294) |
| a status no other controller writes | a merge patch of the changed fields | controller-runtime `client.MergeFrom` |
| its conditions on a status two controllers write | server-side apply of only its own condition types | [cert-manager](https://github.com/cert-manager/cert-manager/blob/v1.21.2/design/20220118.server-side-apply.md#L206-L215) |
| its fields of a spec two controllers write (the Gateway) | server-side apply of only its own fields | cert-manager's [cainjector](https://github.com/cert-manager/cert-manager/blob/v1.21.2/pkg/controller/cainjector/reconciler.go#L131-L135); Gateway API's apply configurations |
| a Gateway listener it no longer wants | a JSON patch that tests the listener's name, then removes it | an apply cannot remove an entry another field manager shares ([kubernetes#128102](https://github.com/kubernetes/kubernetes/issues/128102)) |

Rules:

1. Each controller saves under its own field manager, `network-services/<controller>`.
2. An apply carries the controller's full intent for the object; a field it leaves out, it gives up.
3. A controller never removes a shared condition by leaving it out; it sets it `False` or `Unknown`.
4. The HTTPProxy controller removes unwanted listeners by patch before it applies; it never drops a default listener.
5. The gateway controller sets the default hostname only where it is empty or already its own.
6. Every apply to an existing object carries the object's UID, so a deleted object is never re-created.

**Objects saved before this change need no migration.** Their fields are recorded under `manager` and
`network-services`, names every controller shares. The patches change one entry whoever recorded it; the merge
patch replaces what it changes; an apply takes what it changes and never removes by leaving out (rules 3 and 4).
We do not merge the shared names into one controller (`csaupgrade`): that controller's next apply would delete
the others' fields.

## Risks and Mitigations

| risk | mitigation | status |
| --- | --- | --- |
| A mutating webhook changes the object during a patch or an apply (the HTTPProxy and Gateway webhooks) | run the operator in kind with its webhooks | before phase 1 |
| Milo handles a JSON patch with `test` differently | server-side dry run on a staging project; Milo is built on `k8s.io/apiserver` v0.35 ([go.mod L27](https://github.com/milo-os/milo/blob/718749b934aba630da4efb834b416aed4f5bfedb/go.mod#L27)) | before phase 1 |
| Server-side apply on Milo | on 2026-10-09 Milo held objects applied by `network-services-operator/iroh-dns` and by Flux | checked |
| Two controllers change the finalizer list at once | the patch's test fails with `422`; it is read again and sent again at once | prototype |
| One finalizer added twice | removal takes out every copy | prototype |
| Two replicas save one status during a shard hand-over | the next reconcile writes the current status | prototype |
| With force, two of our controllers claim one field | a unit test per controller holds its apply to its own fields | each phase |
| Permissions | Milo registers `patch` for these resources and their status; the operator's role allows it | checked |

## Rollout

Each phase: staging, then production.

### Phase 1: HTTPProxy and the replicator

- [ ] HTTPProxy controller: its finalizer by JSON patch
- [ ] HTTPProxy controller: the HTTPProxy status by merge patch
- [ ] replicator: its finalizer by JSON patch, on every kind it copies
- [ ] replicator: the upstream status of the policy kinds by merge patch

### Phase 2: the Gateway (#305)

- [ ] HTTPProxy controller: remove unwanted listeners by patch, then apply its Gateway spec
- [ ] gateway controller: the default hostname by apply; its finalizer by JSON patch; the Gateway status by merge patch

### Phase 3: the rest

- [ ] HTTPProxy controller: HTTPRoute, HTTPRouteFilter, EndpointSlice by apply
- [ ] the other controllers' finalizers and statuses
- [ ] Connector conditions by apply, one field manager per controller

### Each phase is done when

- [ ] `409` on the phase's resources falls, per resource and verb (`UPDATE`, `PATCH`, `APPLY`)
- [ ] `422` on `PATCH` stays small next to the `409`s it replaces
- [ ] no object is stuck with a deletion timestamp
- [ ] the e2e suite is green, and `nso_httpproxy_programming_duration_seconds` is no worse
- [ ] the `IsConflict` retries on the moved saves are removed

## Test Plan

- [ ] unit tests of the finalizer patch wrapper: absent list, present finalizer, stale read, terminating object, deleted object, a validation `422`, duplicates
- [ ] a unit test per controller: its apply carries only its own fields
- [ ] envtest runs of the changed controllers under churn, with the previous release as the control
- [ ] the operator in kind with its webhooks
- [ ] staging: the e2e suite and the measures above

## Open Decisions

1. **Field manager names:** `network-services/<controller>` proposed.
2. **Default listener settings:** from phase 2, a change to `ListenerTLSOptions` reaches existing Gateways; before phase 2 it does not.
3. **Apply payloads** for the operator's CRDs: generated apply configurations, or unstructured.

## Drawbacks

| drawback | mitigation |
| --- | --- |
| three save mechanisms instead of one | one wrapper; the rules above |
| two controllers changing the finalizer list at once still collide, as a `422` | it is sent again at once, without backoff |
| force hides a claim by two of our controllers on one field | a unit test per controller |
| a field only old code saved stays recorded under the shared names | remove such a field by patch, not by apply |

## Alternatives

| alternative | why not |
| --- | --- |
| retry on conflict (#303, #329) | the conflict still happens on every save |
| merge patch for every save | two writers of one list undo each other |
| `csaupgrade` over the shared names | gives one controller the others' fields |
| strategic merge patch | not served for custom resources |
| one writer per object | the gateway controller also writes Gateways users create; the replicator's finalizer sits on kinds others own |

## Evidence

A prototype ran these saves against a kube-apiserver (envtest), three times on Kubernetes 1.31 and three times
on 1.35; 18 of 18 scenarios passed in every run. It is kept at
[`4189a59`](https://github.com/datum-cloud/network-services-operator/tree/4189a593c2a09c47d44473fadb6c284b358b7970/docs/enhancements/controller-field-ownership/prototype),
with the command to run it.

| scenario | `Update` | proposed |
| --- | --- | --- |
| finalizers, while other controllers save status and labels (50 objects) | 150-153 × `409` | 0 × `409`, 0 × `422` |
| finalizers, two controllers at the same moment (100 objects) | 178-200 × `409` | 0 × `409`; 141-195 × `422`, each sent again |
| status from a controller's cache (20 objects × 10 changes) | 1-12 × `409` | 0; every status settled |
| conditions, two controllers (100 concurrent applies) | merge patches undid each other | 0 failures; each controller's values kept |
| a Gateway saved before the change | — | no version without a default hostname |
| a hostname removed from such a Gateway | apply alone: rejected | patch, then apply: accepted |

The prototype does not run the operator's controllers, its webhooks, or Milo.
