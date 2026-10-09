---
status: provisional
stage: alpha
---

# Controller Field Ownership

- [Summary](#summary)
- [Motivation](#motivation)
  - [Issues this addresses](#issues-this-addresses)
  - [Earlier attempts](#earlier-attempts)
  - [The numbers](#the-numbers)
  - [Why it happens](#why-it-happens)
  - [What it costs](#what-it-costs)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [Terms](#terms)
  - [The rule](#the-rule)
  - [How each change is saved](#how-each-change-is-saved)
  - [Objects saved before this change](#objects-saved-before-this-change)
  - [What we do not do](#what-we-do-not-do)
  - [User Stories](#user-stories)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
  - [Finalizers](#finalizers)
  - [Status with one writer](#status-with-one-writer)
  - [Conditions with several writers](#conditions-with-several-writers)
  - [The Gateway](#the-gateway)
  - [Saves that keep resourceVersion](#saves-that-keep-resourceversion)
  - [Evidence](#evidence)
  - [Rollout](#rollout)
  - [Test Plan](#test-plan)
- [Open Decisions](#open-decisions)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)

## Summary

| | |
| --- | --- |
| Problem | The operator's controllers reject each other's saves: *"the object has been modified"*. The rejected controller backs off and tries again. |
| Cause | Two controllers change different parts of one object. Each save sends the whole object with the `resourceVersion` it read. Any other save in between changes that version. |
| Proposal | Each controller saves only its own part, and sends no `resourceVersion`. The patterns come from kubernetes-csi, cert-manager, controller-runtime and the Gateway API. |
| New code | About 60 lines copied from kubernetes-csi and about 120 lines of our own (code lines, without comments); each controller's saves change in place. |
| Evidence | A prototype on a kube-apiserver (envtest), Kubernetes 1.31 and 1.35. It does not run the operator's controllers, its webhooks, or Milo. |

## Motivation

### Issues this addresses

| issue | what it reports | how this design addresses it | confidence |
| --- | --- | --- | --- |
| [#305](https://github.com/datum-cloud/network-services-operator/issues/305) | the Gateway's two writers reject each other | phase 2: each controller applies its own fields | direct |
| [#329](https://github.com/datum-cloud/network-services-operator/issues/329) | the replicator's status saves fail the reconcile on a conflict | phase 1: the status save sends no `resourceVersion` | direct |
| [datum-cloud/infra#3930](https://github.com/datum-cloud/infra/issues/3930) | controllers fail a large share of their reconciles | removes the conflict share of those errors | direct, measured on 2026-10-09 |
| [datum-cloud/infra#3881](https://github.com/datum-cloud/infra/issues/3881) | criterion: *"A customer's gateway settles when two writers touch it, instead of looping on conflicts (#305, #329)"* | phases 1 and 2 | direct |
| [datum-cloud/infra#3828](https://github.com/datum-cloud/infra/issues/3828) | a BackendTrafficPolicy's status arrives late in e2e | phase 1, if #329 is the cause | likely; the issue names #329 as the likely cause and has not confirmed it |
| [#326](https://github.com/datum-cloud/network-services-operator/issues/326) | the reconcile error-ratio alert flaps | conflicts leave the ratio | partial; the flapping has other causes |
| [#295](https://github.com/datum-cloud/network-services-operator/issues/295), [datum-cloud/infra#3622](https://github.com/datum-cloud/infra/issues/3622) | HTTPProxies take long to stand up | rejected saves no longer back off | one contributor among several |

### Earlier attempts

| | approach | outcome |
| --- | --- | --- |
| [#169](https://github.com/datum-cloud/network-services-operator/pull/169) | requeue after 1 s on a conflict | closed. @scotwells: *"We need to understand why conflicts are happening before we just throw requeues at the problem. Seems like we should be using server side apply or better conflict resolution."* |
| [#170](https://github.com/datum-cloud/network-services-operator/pull/170) | server-side apply for the HTTPProxy's child objects | closed in favour of #178, which addressed a different cause (connector ownership, #174); not reviewed on its merits |
| [#303](https://github.com/datum-cloud/network-services-operator/pull/303) | requeue after 1 s on a conflict | merged; fixed [#166](https://github.com/datum-cloud/network-services-operator/issues/166). In its review @scotwells asked what caused the conflict, and whether server-side apply would prevent it |

### The numbers

`sum by (job, resource) (increase(apiserver_request_total{code="409"}[24h]))`, 24 hours to
2026-10-09 14:34Z:

| API server | resource | 409 | in scope |
| --- | --- | --- | --- |
| `milo-apiserver` | httpproxies | 1,871 | yes |
| `milo-apiserver` | gateways | 1,452 | yes |
| `milo-apiserver` | httproutes | 1,088 | yes |
| `milo-apiserver` | networkinterfaces | 545 | yes |
| `milo-apiserver` | backendtlspolicies | 517 | yes |
| `karmada-apiserver` | networkinterfaces | 819 | no (the hub) |
| `karmada-apiserver` | networkbindings | 441 | no (the hub) |

- The count includes all clients (users, the portal, `datumctl`, Karmada) and `AlreadyExists` on create. Each phase takes its baseline per resource and per verb before it ships.
- The operator's own log shows fewer: #303 turned some conflicts into a silent retry. For gateways it logged 445 `Reconciler error` lines with *"the object has been modified"* in the 24 hours to 2026-10-09 13:50Z.

### Why it happens

| object | writers inside the operator | what each saves |
| --- | --- | --- |
| HTTPProxy | HTTPProxy controller | its finalizer ([`httpproxy_controller.go:248-249`](https://github.com/datum-cloud/network-services-operator/blob/7351cf673e02039436cfe4d3e038c16895d2f6c5/internal/controller/httpproxy_controller.go#L248-L249)); the status ([`:238`](https://github.com/datum-cloud/network-services-operator/blob/7351cf673e02039436cfe4d3e038c16895d2f6c5/internal/controller/httpproxy_controller.go#L238)) |
| | replicator | its finalizer ([`gateway_resource_replicator_controller.go:280-281`](https://github.com/datum-cloud/network-services-operator/blob/7351cf673e02039436cfe4d3e038c16895d2f6c5/internal/controller/gateway_resource_replicator_controller.go#L280-L281)) |
| Gateway | HTTPProxy controller | the spec ([`httpproxy_controller.go:280`](https://github.com/datum-cloud/network-services-operator/blob/7351cf673e02039436cfe4d3e038c16895d2f6c5/internal/controller/httpproxy_controller.go#L280)) |
| | gateway controller | its finalizer and the default listeners' hostname, derived from the Gateway's UID ([`gateway_controller.go:225-247`](https://github.com/datum-cloud/network-services-operator/blob/7351cf673e02039436cfe4d3e038c16895d2f6c5/internal/controller/gateway_controller.go#L225-L247)); the status |
| Connector | connector controller | conditions `Accepted`, `Ready`; `leaseRef` |
| | iroh-dns controller | condition `IrohDNSPublished`; its finalizer |
| | replicator | its finalizer |

- Each of these saves is a full-object `Update`, `Status().Update` or `CreateOrUpdate`.
- Each sends the `resourceVersion` the controller read.
- Every save changes the `resourceVersion`, so any two of them can reject each other.

### What it costs

| cost | source |
| --- | --- |
| A rejected save is returned as an error. controller-runtime retries it with a growing delay, 5 ms to 1000 s, and drops any `RequeueAfter` | [`controller.go:483-494`](https://github.com/kubernetes-sigs/controller-runtime/blob/v0.24.1/pkg/internal/controller/controller.go#L483-L494) |
| One rejection causes a second: the HTTPProxy controller's finalizer save is rejected and asks for a 1 s retry; its deferred status save then runs on the same old object, is rejected too, and cancels that retry | [`httpproxy_controller.go:229-251`](https://github.com/datum-cloud/network-services-operator/blob/7351cf673e02039436cfe4d3e038c16895d2f6c5/internal/controller/httpproxy_controller.go#L229-L251) |
| Rejections count as reconcile errors, which the error-ratio alerts read | #329, #326 |

### Goals

- A controller's save is not rejected because another controller changed a different part of the object.
- No finalizer is lost, and no object is left stuck in deletion.
- The change works on objects saved before it, while old and new replicas run side by side, and after a rollback.
- Patterns used in production by other projects; little new code.

### Non-Goals

- Saves on the Karmada hub. Their writers are Karmada and Envoy Gateway.
- Writers outside the operator: users, the portal, `datumctl alb`, the connector agent, dns-operator.
- DNSRecordSets, which dns-operator also writes.
- HTTPRoute `status.parents`: one controller writes one entry per parent Gateway, and the list is `atomic`.

## Proposal

### Terms

| term | meaning |
| --- | --- |
| save | any write to the API server |
| field manager | the name a save is recorded under; the API server records which field manager set each field |
| legacy managers | `manager` and `network-services`: the binary's old and present names. Every save before this change is recorded under one of them, whichever controller made it |
| 409 | the API server rejects a save whose `resourceVersion` is old, or an apply that claims a field another field manager owns |
| 422 | the API server rejects a save that fails validation, or a JSON patch whose `test` fails |
| apply | a server-side apply: the controller sends only the fields it owns |
| force | an apply option: the controller takes the fields it sets from any other field manager |
| Milo | the API server that serves each project's control plane |
| replicator | the `gateway_resource_replicator` controller: it copies a project's objects to the hub and puts its finalizer on each |
| connector agent | the program a customer runs; it writes the Connector's `connectionDetails` and `capabilities` |

### The rule

- Each controller saves only the part of an object it owns.
- No save sends a `resourceVersion`, except where two writers share one field (see [Saves that keep resourceVersion](#saves-that-keep-resourceversion)).
- Each controller saves under its own field manager, `network-services/<controller>`.

### How each change is saved

| a controller changes | it saves | taken from |
| --- | --- | --- |
| its finalizer | a JSON patch: `test` the list, then add or remove its own entry | kubernetes-csi [external-attacher](https://github.com/kubernetes-csi/external-attacher/blob/v4.13.0/pkg/controller/util.go#L229-L294), written for the same race ([external-provisioner#1217](https://github.com/kubernetes-csi/external-provisioner/issues/1217)) |
| a status no other controller writes | a merge patch of the changed fields | controller-runtime `client.MergeFrom` |
| its conditions on a status that two controllers write | an apply of only its own condition types, with force | cert-manager [design](https://github.com/cert-manager/cert-manager/blob/v1.21.2/design/20220118.server-side-apply.md#L206-L215); kubebuilder book [example](https://github.com/kubernetes-sigs/kubebuilder/blob/v4.16.0/docs/book/src/reference/server-side-apply.md#L80-L113) |
| its fields of a spec that two controllers write (the Gateway) | an apply of only its own fields, with force | cert-manager's cainjector [apply](https://github.com/cert-manager/cert-manager/blob/v1.21.2/pkg/controller/cainjector/reconciler.go#L131-L135); Gateway API's typed apply configurations |
| a Gateway listener it no longer wants | a JSON patch: `test` the listener's name, then remove it | an apply cannot remove an entry while another field manager owns a field in it ([kubernetes#128102](https://github.com/kubernetes/kubernetes/issues/128102)) |

Rules that keep this safe:

1. A controller never removes a shared condition by leaving it out of its apply. It sets the condition `False` or `Unknown`.
2. The HTTPProxy controller never drops a default listener; it always sets them ([`httpproxy_controller.go:1053`](https://github.com/datum-cloud/network-services-operator/blob/7351cf673e02039436cfe4d3e038c16895d2f6c5/internal/controller/httpproxy_controller.go#L1053)).
3. The HTTPProxy controller removes unwanted listeners by patch **before** it applies.
4. Every apply to an existing object carries its UID; the first apply that creates the Gateway carries none.
5. Each apply carries the field manager's full intent for that object: a field it leaves out, it gives up.

### Objects saved before this change

No migration step. Each save works whichever field manager recorded a field:

| save | why it works on an object saved before |
| --- | --- |
| finalizer and listener patches | they `test` and change one entry, whoever recorded it |
| merge patch | it replaces the fields it changes |
| apply with force | it takes the fields whose value it changes; a field it sets to the same value is shared with the legacy manager, so the controller never removes such a field by leaving it out (rules 1-3) |

An HTTPProxy read from production on 2026-10-09 (`kubectl get httpproxy <name> -o yaml --show-managed-fields` on its project control plane):

| finalizer | recorded under |
| --- | --- |
| `networking.datumapis.com/httpproxy-cleanup` | `manager`, `Update` |
| `gateway.networking.datumapis.com/gateway-resource-replicator` | `network-services`, `Update` |

### What we do not do

| | why not |
| --- | --- |
| client-go `csaupgrade` over the legacy managers | the legacy managers are shared by every controller; moving them to one controller gives it the others' fields, and its next apply deletes them. cert-manager can use it because its legacy managers are per controller |
| delete all ownership records (Crossplane) | it also deletes the records of users, the portal and the CLI |
| apply `metadata.finalizers: [mine]` for finalizers | a removal by leaving the entry out does not remove an entry the legacy managers also recorded, so the object would never finish deleting |

### User Stories

| as | I see | measured by |
| --- | --- | --- |
| a user who creates a tunnel | it ready sooner, because a rejected save no longer backs off | `nso_httpproxy_programming_duration_seconds` |
| an engineer on call | fewer reconcile errors that are not faults | the reconcile error ratio per controller |

### Risks and Mitigations

| risk | impact if it happens | mitigation | status |
| --- | --- | --- | --- |
| A mutating webhook changes the object during a patch or an apply: the HTTPProxy webhook stamps annotations on every update ([`httpproxy_webhook.go:38-41`](https://github.com/datum-cloud/network-services-operator/blob/7351cf673e02039436cfe4d3e038c16895d2f6c5/internal/webhook/v1alpha/httpproxy_webhook.go#L38-L41)); the Gateway webhook restores missing default listeners ([`gateway_webhook.go:175-179`](https://github.com/datum-cloud/network-services-operator/blob/7351cf673e02039436cfe4d3e038c16895d2f6c5/internal/webhook/v1/gateway_webhook.go#L175-L179)) | saves fail, or fields flap | run the operator in kind with its webhooks | open: before phase 1 |
| Milo handles a JSON patch with `test` differently | finalizer saves fail | Milo is built on `k8s.io/apiserver` v0.35 ([go.mod L27](https://github.com/milo-os/milo/blob/718749b934aba630da4efb834b416aed4f5bfedb/go.mod#L27)), the same patch code as envtest 1.35; a server-side dry run on a staging project | open: before phase 1 |
| Server-side apply on Milo | applies fail | on 2026-10-09 the `datum-cloud` project control plane held DNSRecordSets applied by `network-services-operator/iroh-dns` and a Domain applied by Flux | checked |
| Two replicas, or one replica with a stale cache, add one finalizer twice | an extra entry | removal takes out every copy | tested |
| Two controllers change the finalizer list at the same moment | a 422, and one more patch | the patch is read again and sent again at once, at most 5 times; after that the reconcile returns an error | tested |
| During a shard hand-over two replicas merge-patch one status | the older one may win briefly | the next reconcile writes the current status | tested with old code overwriting |
| An apply reaches an object that was deleted, or deleted and created again | an object re-created | the UID in the apply: 409 when deleted, 422 when created again; nothing is created; the controller treats it as gone | tested |
| With force, two of our controllers claim one field | one silently wins | a unit test per controller holds its apply to its own fields | open: in each phase |
| A user sets a hostname on a default listener of their own Gateway | the controller overwrites it | the gateway controller applies the hostname only where it is empty or already its own | tested |
| Permissions | saves fail with 403 | Milo registers `patch` for every resource phase 1 changes, and for their status (`config/iam/protected-resources/`); the operator's role allows it (`config/rbac/role.yaml`) | checked |
| The activity feed | wrong or missing activity entries | its rules treat `update` and `patch` alike; a save without `spec` matches no rule (`config/milo/activity/policies/`) | checked |
| A rollback | old code meets objects the new code saved | `Update` ignores who recorded a field | tested for finalizers, status and conditions |

## Design Details

### Finalizers

- `addFinalizerPatch` and `removeFinalizerPatch` are copied unchanged from external-attacher v4.13.0, with their Apache-2.0 notice.
- Add: if the list is absent or empty, `test` that, then set it; otherwise append.
- Remove: `test` that slot `i` holds the finalizer, then remove slot `i`.

Our wrapper, `Patcher`:

| case | behaviour |
| --- | --- |
| the finalizer is present (add) or absent (remove) | no save |
| the object is terminating (add) | no save |
| `404` | done |
| `422`, and an uncached read shows the object changed | build the patch again and send it, at once; at most 5 patches |
| `422`, and the object is unchanged | a validation rejection; returned |
| still failing after 5 patches | an error; the reconcile requeues |
| a duplicated finalizer (remove) | one patch per copy, until none is left |
| every patch | sent under the controller's field manager |

A patch changes no field outside `metadata.finalizers`, so a status or spec save cannot make it fail. A change to the finalizer list makes its `test` fail.

### Status with one writer

- The controller sends a merge patch of the difference between the object it read and the status it computed. No `resourceVersion`.
- A list it changes is replaced whole, so an entry it drops goes, whoever recorded it.
- The controller must reconcile again after its own status save, so that a save computed from an old cached copy is corrected. The HTTPProxy controller and the replicator have no generation filter on their watch ([`httpproxy_controller.go:744`](https://github.com/datum-cloud/network-services-operator/blob/7351cf673e02039436cfe4d3e038c16895d2f6c5/internal/controller/httpproxy_controller.go#L744), [`gateway_resource_replicator_controller.go:902-904`](https://github.com/datum-cloud/network-services-operator/blob/7351cf673e02039436cfe4d3e038c16895d2f6c5/internal/controller/gateway_resource_replicator_controller.go#L902-L904)).

### Conditions with several writers

| controller | applies |
| --- | --- |
| connector | `Accepted`, `Ready`, `leaseRef` |
| iroh-dns | `IrohDNSPublished` |
| the connector agent (not changed) | `connectionDetails`, `capabilities` |

- Each controller applies only its own conditions, under its own field manager, with force. `conditions` is a map keyed by `type`, so one controller's apply does not touch the other's conditions.
- The controller keeps each condition's `lastTransitionTime`, as `SetStatusCondition` does.
- The operator's CRDs have no generated apply configurations. The phase chooses between generating them (`controller-gen applyconfiguration`) and `client.ApplyConfigurationFromUnstructured`.

### The Gateway

| step | who | save |
| --- | --- | --- |
| 1 | HTTPProxy controller | removes, by patch, every listener its intent no longer has. Listeners are named by the hostname's position (`http-hostname-<i>`, [`httpproxy_controller.go:1056-1072`](https://github.com/datum-cloud/network-services-operator/blob/7351cf673e02039436cfe4d3e038c16895d2f6c5/internal/controller/httpproxy_controller.go#L1056-L1072)), so a stale listener can make the new spec fail the uniqueness rule for port, protocol and hostname |
| 2 | HTTPProxy controller | applies its spec, with the default listeners **without** a hostname. Replaces `CreateOrUpdate` and the read-back at [`httpproxy_controller.go:293-305`](https://github.com/datum-cloud/network-services-operator/blob/7351cf673e02039436cfe4d3e038c16895d2f6c5/internal/controller/httpproxy_controller.go#L293-L305). Keeps the `hasControllerConflict` check before the apply ([`:281`](https://github.com/datum-cloud/network-services-operator/blob/7351cf673e02039436cfe4d3e038c16895d2f6c5/internal/controller/httpproxy_controller.go#L281)) |
| 3 | gateway controller | applies the canonical hostname on each default listener whose hostname is empty or already canonical: `Gateway(name, ns).WithUID(uid)` with `Listener().WithName(...).WithHostname(...)` |
| 4 | gateway controller | its finalizer by patch; the Gateway status by merge patch |

- The gateway controller cannot set a hostname on a listener that does not exist yet: validation rejects an entry without port or protocol. It waits for step 2.
- Steps 2 and 3 may run in either order.

### Saves that keep resourceVersion

| save | where | why it stays |
| --- | --- | --- |
| HTTPRoute `status.parents` | `result.go` | one controller, one entry per parent Gateway, an `atomic` list |
| DNSRecordSet | [`gateway_dns_controller.go:290`](https://github.com/datum-cloud/network-services-operator/blob/7351cf673e02039436cfe4d3e038c16895d2f6c5/internal/controller/gateway_dns_controller.go#L290) | dns-operator also writes it; out of scope |
| saves on the hub | e.g. [`gateway_controller.go:2383`](https://github.com/datum-cloud/network-services-operator/blob/7351cf673e02039436cfe4d3e038c16895d2f6c5/internal/controller/gateway_controller.go#L2383) | out of scope |

The `if apierrors.IsConflict(err)` guards on these saves stay. The guards on saves that move go with them, one phase at a time.

### Evidence

The prototype is in [`prototype/`](prototype/), behind the build tag `prototype`:

```sh
KUBEBUILDER_ASSETS="$(bin/setup-envtest use 1.31.0 --bin-dir "$PWD/bin" -p path)" \
  go test -tags prototype ./docs/enhancements/controller-field-ownership/prototype -v
```

Three runs on Kubernetes 1.31 (the version `make test` uses) and three on 1.35 (the `k8s.io/apiserver` version Milo uses). 18 of 18 scenarios passed in all six runs.

| scenario | save it replaces | proposed |
| --- | --- | --- |
| finalizers, 100 objects, two controllers add and remove at the same moment; 9 runs per version | 178-200 × 409 | 0 × 409; 141-195 × 422, each sent again at once; 0 lost, 0 stuck |
| finalizers, 50 objects, while two other controllers save the status and labels | 150-153 × 409 | 0 × 409, 0 × 422 |
| finalizers recorded under `manager` and `network-services` | — | removed; object deleted |
| one controller on old code, one on new, 100 objects | old code: 192-196 × 409 | new code: 0 × 409, 0-5 × 422; 0 lost, 0 stuck |
| two adds from one cached copy | — | 2 copies; one removal call removed both |
| rollback: added by the new code, removed by the old | — | object deleted |
| stale read; terminating object; absent finalizer; deleted object; invalid name | — | each handled; nothing created; the validation rejection returned |
| status from a controller's cache, 20 objects × 10 changes, another controller saving labels | 1-12 × 409 | 0 × 409; every status settled |
| a status list saved under `manager` shrinks | — | the dropped entry goes |
| old code overwrites the status 5 times | — | each time the controller restored it |
| conditions, two controllers, 100 concurrent applies | merge patches: one controller undid the other's `Ready=False` | 0 failures; each controller's last values kept; the agent's field kept |
| old code overwrites all conditions | — | each controller's next apply restored its own |
| a new Gateway, two controllers, 20 saves | — | 0 failures; hostnames kept |
| a Gateway saved before the change, both orders, every version watched | — | 0 versions without a default hostname; the legacy-recorded finalizer and annotation kept |
| a hostname removed from a Gateway saved before the change | apply only: rejected by the uniqueness rule | patch, then apply: accepted |
| a user's hostname on a default listener, 10 passes | — | kept; the canonical hostname stable |
| an apply to a deleted Gateway, and to one created again | — | 409, then 422; nothing created |
| old code saving with `Update` during new-code applies, 60 saves | old code: 20 × 409 per run | new code: 0 failures; the next apply restored the new intent |

The prototype does not run:

- the operator's controllers; it reproduces their saves;
- the webhooks;
- Milo or the hub.


### Rollout

| phase | saves that move | objects | API server |
| --- | --- | --- | --- |
| 1 | HTTPProxy controller: finalizer, status. Replicator: finalizer on every kind it copies (configured in `Gateway.ResourceReplicator.Resources`), upstream status of the policy kinds | HTTPProxy; the replicated kinds | Milo |
| 2 | HTTPProxy controller: Gateway spec. Gateway controller: hostname, finalizer, status | Gateway | Milo |
| 3 | HTTPProxy controller: HTTPRoute, HTTPRouteFilter, EndpointSlice. The other controllers' finalizers and statuses. Connector conditions | the rest on Milo | Milo |

Each phase: staging first; production a day later.

| measure | pass | stop and roll back |
| --- | --- | --- |
| `apiserver_request_total{code="409"}` per resource and verb (`UPDATE`, `PATCH`, `APPLY`), as a share of all saves | falls on the phase's resources | — |
| `apiserver_request_total{code="422", verb="PATCH"}` | small next to the 409s it replaces | rises without a matching fall in 409s |
| objects stuck with a deletion timestamp | none new | any |
| e2e suite | green | a new failure |
| `nso_httpproxy_programming_duration_seconds` | no worse | worse |

### Test Plan

- Unit tests of `Patcher`: every row of the table above.
- A unit test per controller: its apply carries only its own fields.
- envtest runs of the changed controllers under churn: no 409 between them; the previous release showing 409s as the control.
- The operator in kind with its webhooks, before and after.
- A server-side dry run of a finalizer patch and an apply on a staging project control plane.
- Staging: the e2e suite and the measures above.

## Open Decisions

1. **Field manager names.** Proposed `network-services/<controller>`. The iroh-dns controller and the VPC EndpointSlice write-back apply under `network-services-operator/…` with force; aligning them is separate.
2. **Default listener settings.** The HTTPProxy controller copies the live default listeners back, so a change to `ListenerTLSOptions` never reaches an existing Gateway. Under phase 2 it applies its desired default listeners, so such a change reaches every Gateway. Accept that, or keep the old behaviour.
3. **Phase order.** #305 suggested the Gateway first. Phase 1 is HTTPProxy and the replicator: the most 409s on its resources and the simplest saves.
4. **Apply payloads** for the operator's CRDs: generated apply configurations, or unstructured.

## Drawbacks

| drawback | size | mitigation |
| --- | --- | --- |
| three save mechanisms instead of one: JSON patch, merge patch, apply | each controller uses the one that fits each save | one wrapper; rules above |
| two controllers changing the finalizer list at once still collide | 141-195 × 422 per 100 objects in the worst case, in the prototype | each is sent again at once, without backoff |
| force hides a claim by two of our controllers on one field | no 409 to see it | a unit test per controller |
| a field only old code saved stays recorded under the legacy managers | removing such a field needs a patch, not an apply | rules 1-3; cert-manager accepted the same cost |
| a duplicated finalizer | one extra patch at removal | removal takes out every copy |
| one more managed-fields entry per controller on each object | small | — |

## Alternatives

| alternative | why not |
| --- | --- |
| retry on conflict everywhere (#303, #329) | the conflict still happens on every save; each costs a round trip |
| Cluster API's patch helper with `WithOwnedConditions` | still rejected, and retried inside the helper; brings in the Cluster API module |
| merge patch for every save | a merge patch replaces a whole list, so two writers of `finalizers` or `conditions` undo each other; the prototype shows it |
| apply for finalizers | see [What we do not do](#what-we-do-not-do) |
| strategic merge patch | the API server does not serve it for custom resources |
| fewer reconcile triggers, e.g. a filter on the Connector watch | fewer conflicts, not none |
| one writer per object | the gateway controller also sets hostnames and its finalizer on Gateways that users create ([`gateway_controller.go:225-247`](https://github.com/datum-cloud/network-services-operator/blob/7351cf673e02039436cfe4d3e038c16895d2f6c5/internal/controller/gateway_controller.go#L225-L247)); the replicator's finalizer sits on kinds other controllers own |
