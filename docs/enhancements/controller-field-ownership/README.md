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

- Writers outside the operator: users, the portal, `datumctl`, the connector agent, dns-operator.
- HTTPRoute `status.parents`: one controller writes one entry per parent Gateway into an `atomic` list.

## Proposal

| a controller changes | it saves | taken from |
| --- | --- | --- |
| its finalizer, added | server-side apply of the finalizer with the object's UID | cert-manager ([#8519](https://github.com/cert-manager/cert-manager/pull/8519)) |
| its finalizer, removed | a JSON patch that tests the entry, then removes it | kubernetes-csi [external-attacher](https://github.com/kubernetes-csi/external-attacher/blob/v4.13.0/pkg/controller/util.go#L262-L294) |
| a status no other controller writes: HTTPProxy, the replicator's upstream statuses, Gateway | a merge patch from the stored status | controller-runtime `client.MergeFrom` |
| its conditions on a status two controllers write | server-side apply of only its own condition types | [cert-manager](https://github.com/cert-manager/cert-manager/blob/v1.21.2/design/20220118.server-side-apply.md#L206-L215) |
| its fields of a spec two controllers write (the Gateway) | server-side apply of only its own fields | cert-manager's [cainjector](https://github.com/cert-manager/cert-manager/blob/v1.21.2/pkg/controller/cainjector/reconciler.go#L131-L135); Gateway API's apply configurations |
| a Gateway listener it no longer wants | a JSON patch that tests each listener's name, then removes it | an apply cannot remove an entry another field manager shares ([kubernetes#128102](https://github.com/kubernetes/kubernetes/issues/128102)) |

Rules:

1. Each controller saves under its own field manager, `network-services/<controller>`.
2. An apply carries the controller's full intent for the object; a field it leaves out, it gives up.
3. A controller never removes a shared condition by leaving it out; it sets it `False` or `Unknown`.
4. The HTTPProxy controller removes unwanted listeners by patch before it applies; its apply always carries both default listeners, without hostnames.
5. The gateway controller sets the default hostname only where it is empty or already its own, and carries its finalizer in that same apply.
6. Every apply to an existing object carries the object's UID, so a deleted object is never re-created.
7. A finalizer's field manager applies nothing else to the object.
8. A tested patch that fails answers `422 Invalid` with no cause, the same code and reason as a validation failure. The finalizer add is an apply, which cannot lose that race; the listener removal reads the Gateway again and reports `Invalid` only when no listener moved.

**Objects saved before this change need no migration.** Their fields are recorded under `manager` and
`network-services`, names every controller shares. The patches change one entry whoever recorded it; the merge
patch replaces what it changes; an apply takes what it changes and never removes by leaving out (rules 3 and 4).
We do not merge the shared names into one controller (`csaupgrade`): that controller's next apply would delete
the others' fields.

## Risks and Mitigations

| risk | mitigation | status |
| --- | --- | --- |
| A mutating webhook changes the object during a patch or an apply (the HTTPProxy and Gateway webhooks) | in kind, both run on every proposed save as on `Update` and keep their fields; neither runs on a status save | checked |
| The HTTPProxy controller's apply leaves out a default listener while its hostname is recorded under `Update` | the apply is admitted and the webhook restores the listener without its hostname; a unit test pins the apply body, and once the gateway controller has applied, such an apply is rejected | phase 2 |
| Milo handles a JSON patch with `test`, or an apply with a UID, differently | each save through Milo, in NSO's kind env with Milo serving the CRDs or on a staging project; Milo is built on `k8s.io/apiserver` v0.35 ([go.mod L27](https://github.com/milo-os/milo/blob/718749b934aba630da4efb834b416aed4f5bfedb/go.mod#L27)) | before phase 1 |
| Server-side apply on Milo | on 2026-10-09 Milo held objects applied by `network-services-operator/iroh-dns` and by Flux | checked |
| Two controllers remove finalizers at once | the removal's test fails with `422`, and the reconcile reads again | prototype, kind |
| One finalizer added twice | each removal takes one copy; the next reconcile takes the next | prototype |
| Two replicas save one status during a shard hand-over | the next reconcile writes the current status | prototype |
| With force, two of our controllers claim one field | a unit test per controller holds its apply to its own fields | each phase |
| Permissions | Milo registers `patch` for these resources and their status; the operator's role allows it | checked |

## Rollout

Each phase ships as one release: staging, then production. Phases 3 to 5 start with a prototype. Each phase has its issue under [datum-cloud/infra#6881](https://github.com/datum-cloud/infra/issues/6881).

| phase | saves | issue |
| --- | --- | --- |
| 1 | finalizers of the HTTPProxy controller and the replicator; HTTPProxy status; the replicator's upstream statuses | [#600](https://github.com/datum-cloud/network-services-operator/issues/600) |
| 2 | the Gateway: the HTTPProxy controller's spec; the gateway controller's finalizer, default hostnames and status | [#305](https://github.com/datum-cloud/network-services-operator/issues/305) |
| 3 | HTTPRoute, HTTPRouteFilter, EndpointSlice | [#601](https://github.com/datum-cloud/network-services-operator/issues/601) |
| 3 | Connector and TrafficProtectionPolicy status | [#602](https://github.com/datum-cloud/network-services-operator/issues/602) |
| 3 | Network, NetworkContext, Subnet, NetworkInterface | [#603](https://github.com/datum-cloud/network-services-operator/issues/603) |
| 4 | saves on the Karmada hub | [#604](https://github.com/datum-cloud/network-services-operator/issues/604) |
| 5 | the shared field-manager entries, conflict retries and helpers the old saves leave behind | [#605](https://github.com/datum-cloud/network-services-operator/issues/605) |

### Each phase is done when

- [ ] `409` on the phase's resources falls, per resource and verb (`UPDATE`, `PATCH`, `APPLY`)
- [ ] `422` on `PATCH` stays below the `409`s it replaces
- [ ] no object is stuck with a deletion timestamp
- [ ] the e2e suite is green, and `nso_httpproxy_programming_duration_seconds` is no worse
- [ ] the `IsConflict` retries on the moved saves are removed

## Test Plan

- [ ] the finalizer package against a kube-apiserver: absent list, two controllers adding at once, present, stale read, a legacy entry, a lost removal race, terminating, deleted, created again, duplicates
- [ ] the HTTPProxy controller's Gateway apply body: both default listeners, no hostnames, only its own fields
- [ ] the Gateway saves against a kube-apiserver: new, legacy, a hostname removed, a user's hostname, a stale intent, a lost and a rejected removal
- [ ] each new test fails on a deliberate defect
- [ ] NSO's kind env: the e2e suite three times, `main` as control, `409` and `422` per resource and verb
- [ ] staging: the e2e suite and the measures above

## Open Decisions

1. **Default listener settings:** phase 2 sends the configured `ListenerTLSOptions` to every existing Gateway. Production is read before phase 2; a difference ships as its own release first.
2. **Apply payloads** for the operator's CRDs, from phase 3: generated apply configurations, or unstructured. Phases 1 and 2 need neither: the finalizer apply carries only metadata, and the Gateway uses Gateway API's typed apply configurations.

## Drawbacks

| drawback | mitigation |
| --- | --- |
| four save mechanisms: apply, JSON patch, merge patch, and `Update` where a list stays locked | one package for finalizers; the rules above |
| two controllers removing finalizers at once still collide, as a `422` | the reconcile reads again; in kind, about 6 per e2e run, all retried |
| force hides a claim by two of our controllers on one field | a unit test per controller |
| a field only old code saved stays recorded under the shared names | remove such a field by patch; phase 5 removes the shared names' entries |

## Alternatives

| alternative | why not |
| --- | --- |
| retry on conflict (#303, #329) | the conflict still happens on every save |
| a JSON patch for the finalizer add too | a lost race answers `422` like a validation failure, so the HTTPProxy controller would report a valid HTTPProxy as invalid |
| merge patch for every save | two writers of one list undo each other |
| `csaupgrade` over the shared names | gives one controller the others' fields |
| strategic merge patch | not served for custom resources |
| one writer per object | the gateway controller also writes Gateways users create; the replicator's finalizer sits on kinds others own |

## Evidence

A prototype ran these saves against a kube-apiserver (envtest), three times on Kubernetes 1.31 and three times
on 1.35; 18 of 18 scenarios passed in every run. It is kept at
[`4189a59`](https://github.com/datum-cloud/network-services-operator/tree/4189a593c2a09c47d44473fadb6c284b358b7970/docs/enhancements/controller-field-ownership/prototype),
with the command to run it. It tested the earlier design, which added finalizers by JSON patch too; the operator run below uses the current design.

| scenario | `Update` | proposed |
| --- | --- | --- |
| finalizers, while other controllers save status and labels (50 objects) | 150-153 × `409` | 0 × `409`, 0 × `422` |
| finalizers, two controllers at the same moment (100 objects) | 178-200 × `409` | 0 × `409`; 141-195 × `422`, each sent again |
| status from a controller's cache (20 objects × 10 changes) | 1-12 × `409` | 0; every status settled |
| conditions, two controllers (100 concurrent applies) | merge patches undid each other | 0 failures; each controller's values kept |
| a Gateway saved before the change | — | no version without a default hostname |
| a hostname removed from such a Gateway | apply alone: rejected | patch, then apply: accepted |

The operator itself, with these saves for phases 1 and 2, in NSO's kind env (Kubernetes 1.35.5): the e2e suite three times, `main` as the control, counting the API server's `409` per resource and verb in each run.

| `409` per run | `main` | these saves |
| --- | --- | --- |
| `httpproxies` `PUT` | 7, 6, 6 | 0, 0, 0 |
| `httpproxies/status` `PUT` | 6, 5, 3 | 0, 0, 0 |
| `gateways/status` `PUT` | 6, 6, 8 | 0, 0, 0 |
| `networks` `PUT`, the replicator's finalizer | 8, 11, 11 | 1, 0, 2 |
| e2e suite passed, of 37 | 37, 36, 37 | 37, 37, 37 |

Neither run covers Milo; phase 1 checks it first.
