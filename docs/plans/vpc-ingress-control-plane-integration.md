# NSO control-plane integration for VPC ingress (#856)

## Context

[#856](https://github.com/datum-cloud/enhancements/issues/856) asks NSO to
integrate with VPC ingress, but its acceptance criteria (`VPCAttachment`
reconciler, single-VPC-per-gateway, GatewayClass→VPC mapping, Karmada
propagation of attachment state) describe a design that its parent issue,
[#796](https://github.com/datum-cloud/enhancements/issues/796), explicitly
rejected in a 2026-08-08 correction. The accepted design is
[PR #851](https://github.com/datum-cloud/enhancements/pull/851): Envoy Gateway
never attaches to a VPC. It stays on the cluster overlay network and the
shared, multi-tenant fleet serves every tenant concurrently. Disambiguating
tenants whose VPC address space collides happens via a per-tenant Linux VRF +
SRv6 encapsulation route on Envoy's own node (a sidecar, #855), driven by
per-pod `EndpointSlice` objects `galactic-cni` publishes (#854). Of #796's six
sub-issues, #854 and #855 already carry matching rescoping edits; #856
(this one), #857, and #858 do not.

This plan targets **what PR #851 actually needs from NSO**, not #856's
literal (stale) text. #856 should be edited to match once this plan is
agreed — it currently reads as a different feature than what gets built.

Two scope decisions are locked in:
- **Same-namespace only** for VPC backend resolution — no ReferenceGrant
  work. Matches every existing NSO backend-ref constraint
  (`internal/validation/httproute_validation.go:213-215`); the alternative
  (implementing `ReferenceGrant`) is materially more scope for a case PR #851
  itself leaves undesigned. Revisit only if tenants need cross-namespace
  placement.
- **TrafficProtectionPolicy VPC integration is cut.** PR #851 never mentions
  TPP, and a VPC-backed HTTPProxy is just another HTTPProxy — it already
  generates a same-named Gateway/HTTPRoute that TPP can target unchanged. No
  new field, controller logic, or webhook rule needed.

## What NSO actually owns here

Reading PR #851 against what #854/#855 already claim:

| Component | Owner | What it does |
|---|---|---|
| CNI plugin | #854 (galactic-cni) | Publishes one `EndpointSlice` per VPC pod, annotated with SID + tenant identifier |
| Sidecar | #855 | Watches that `EndpointSlice`, manages the per-tenant VRF device + SRv6 route on Envoy's node |
| **HTTPProxy backend API + translation** | **#856 (NSO)** | Let a tenant's `HTTPProxy` rule reference that same `EndpointSlice` as a backend, end to end to Envoy |
| **Extension server mutation** | **#856 (NSO)** | Patch the backend's Envoy cluster config with a socket-bind option naming the tenant's VRF device |
| **Sidecar injection into Envoy's pod** | **#856 (NSO, config only)** | Wire #855's sidecar container into the `EnvoyProxy` CR NSO already deploys |

NSO's job is the control-plane glue connecting #854's published state to
#855's kernel-level mechanism through Envoy's config, plus the deployment
wiring that gets the sidecar container running at all. It does **not**
implement the sidecar or the CNI plugin.

## Current codebase facts this plan builds on

- **HTTPProxy backend model** (`api/v1alpha/httpproxy_types.go:115-150`) has no
  discriminated backend kind — just `HTTPProxyRuleBackend{Endpoint, Connector,
  TLS, Filters}`. `Endpoint` is currently `+kubebuilder:validation:Required`
  (line 121) on every backend, including `Connector` ones — a `VPCPod` field
  needs the same three-way relaxation, not just a bolt-on CEL rule. No
  existing mutual-exclusion CEL rule targets *backend kind* in this file; the
  closest precedent (`HTTPProxyRuleBackend.Filters`, lines 144-148) is a
  filter-type exclusivity check, not a backend-target one — the
  `endpoint`/`connector`/`vpcPod` oneOf has to be authored fresh.
- **`HTTPProxyReconciler.collectDesiredResources`**
  (`internal/controller/httpproxy_controller.go:748-1070`) currently
  *synthesizes its own* `EndpointSlice` per backend
  (`httpproxy_controller.go:1006-1033`) and points the generated
  `HTTPBackendRef` at it (`Group=discovery.k8s.io, Kind=EndpointSlice`,
  lines 1037-1047). PR #851 requires the *opposite* for a VPC backend: resolve
  to the **existing** CNI-published `EndpointSlice`, never synthesize a
  second one — synthesizing would separate the pod address from its SID
  annotation, which is the one thing the whole mechanism depends on staying
  joined. **This isn't a clean skip**: the function unconditionally
  `url.Parse()`s `backend.Endpoint` and runs FQDN/IP/scheme-rewrite logic
  (lines ~877-996) *before* any backend-type branch exists today (the only
  existing branch, `backend.Connector != nil` at line 904, still runs through
  that same URL-parse path with a placeholder host) — a `VPCPod` backend
  needs to bypass that entire block, not just the synthesis lines at the
  bottom. There is also **no condition-setting path** for a
  `collectDesiredResources` error today — it bubbles up as a generic
  `fmt.Errorf`-wrapped requeue (`Reconcile`, lines 195-198), not a status
  condition. A missing/wrong-namespace `VPCPod` reference needs to be
  explicitly wired into the existing condition machinery
  (`acceptedCondition`/`HTTPProxyReasonInvalid`, or a new `programmedCondition`
  `NotFound` reason following the existing EndpointSlice-conflict pattern) to
  actually surface as "a clear condition" rather than a bare requeue loop.
- **`GatewayReconciler.processDownstreamHTTPRouteRules`**
  (`internal/controller/gateway_controller.go:2285-2518`) has a
  `case KindEndpointSlice:` (lines 2318-2493 — longer than it looks; the tail
  is `BackendTLSPolicy` create/delete logic, lines 2424-2492) that today
  *always*: (1) unconditionally `Get`s the upstream `EndpointSlice` and stamps
  `gatewayControllerGCFinalizer` on it (lines 2320-2339) *before anything else
  runs*, then (2) builds a downstream `Service` + `EndpointSlice` pair and
  rewrites the backendRef to `Kind=Service`, and (3) conditionally
  synthesizes a `BackendTLSPolicy` if the backend's app protocol is `https`.
  A VPC backend must **not** go through a ClusterIP `Service` — Envoy needs a
  real endpoint address for the VRF/SID mechanism to work. The tenant-id
  branch has to intercept **before step (1)**, not just skip the
  Service-synthesis step: the CNI-published EndpointSlice has no upstream
  counterpart (per the note below), so the unconditional upstream `Get` 404s
  if the branch doesn't short-circuit ahead of it. Step (3)'s
  `BackendTLSPolicy` logic also needs an explicit bypass for the pass-through
  path (there's no synthesized `Service` for it to target). Because the
  pass-through EndpointSlice gets no finalizer or owner reference under this
  design, its lifecycle is left entirely to `galactic-cni` (#854) — state
  that explicitly as an assumption; today's downstream-GC controller only
  acts on upstream deletions and synthesized-name matches, so it's a safe
  omission, but it should be a stated decision, not a silent one.
- **Extension server** (`internal/extensionserver/`) only implements
  `PostTranslateModify` (`server/server.go:96`), and its cache only primes
  `TrafficProtectionPolicy`, `HTTPProxy`, `Connector`, `Namespace`
  (`cache/builder.go:71-78`) — **no `EndpointSlice` watch today**. There is
  also **no existing per-cluster socket-option mechanism anywhere in the
  repo** (`grep -rn "SocketOption"` — zero hits); this is new functionality.
  All existing xDS mutation in this package goes through
  `protojson.Unmarshal` of a hand-built JSON literal into a go-control-plane
  proto (see `mutate/connector.go:186-232`, `buildConnectorCluster`) — follow
  that convention rather than building `clusterv3.Cluster{}` field-by-field,
  but note every existing interpolated value in that file is a
  Kubernetes-object-derived, DNS-1123-safe string; a VRF device name sourced
  from a CNI-published label should get the same charset assumption
  validated explicitly, since Go's `%q` escaping isn't guaranteed valid JSON
  for arbitrary bytes. **Extension-server RBAC is hand-maintained, not
  generated** — `config/extension-server/rbac/role.yaml` is a plain YAML file
  `make manifests`/`controller-gen` never touches (that target only writes
  `config/rbac/role.yaml`, the upstream manager role). Adding `EndpointSlice`
  to `primeObjects()` requires a **manual** edit to that role granting
  `discovery.k8s.io/endpointslices: get,list,watch` — there is no codegen
  step that covers it.
- **The socket-bind mechanism, verified against the kernel**: `SO_BINDTODEVICE`
  (`SOL_SOCKET`/`SO_BINDTODEVICE`, set via a generic Envoy `SocketOption` on
  `upstream_bind_config.socket_options` at `STATE_PREBIND`) is the correct,
  kernel-documented way to make a VRF-unaware process participate in a VRF —
  no first-class "bind to VRF" Envoy feature is needed, a raw sockopt entry
  is sufficient. Two hard constraints this plan must carry: (1) it requires
  **`CAP_NET_RAW` on the process making the bind call — i.e. the Envoy
  container itself**, not the sidecar (the sidecar's `CAP_NET_ADMIN` is for
  its own netlink VRF/route management and does nothing for Envoy's socket
  bind); (2) the device name is a null-terminated string capped at
  **`IFNAMSIZ - 1` = 15 characters** — the proposed `vrf-<tenant-id>` naming
  convention needs a length bound agreed with #855 or it silently fails to
  bind for longer tenant identifiers. Pod-network-namespace sharing between
  the sidecar (which creates the VRF device) and the Envoy container (which
  binds to it by name) is standard Kubernetes behavior and requires no
  special wiring, provided neither container runs with `hostNetwork` or a
  separate netns.
- **Sidecar injection mechanism**: NSO doesn't touch the Envoy `Deployment`
  in Go code at all. The precedent for adding a container to the generated
  Envoy pod is the Coraza WAF init-container in
  `config/dev/downstream_resources/downstream-gateway.yaml:55-74` (mirrored
  in `config/e2e-downstream/envoyproxy.yaml` — confirmed the only two
  `EnvoyProxy` CR instances in this repo). `pod`, `container`, and
  `initContainers` are **native, independent sibling fields** on
  `spec.provider.kubernetes.envoyDeployment` — they are not gated behind the
  `patch.type: StrategicMerge` block in that same manifest, which only
  patches `selector`/`template.metadata.labels` for an unrelated reason. The
  sidecar goes in as a long-running `container` (not `initContainer` — it
  needs to run for the pod's whole lifetime) with `CAP_NET_ADMIN`; the main
  Envoy `container` entry in the same manifest needs `CAP_NET_RAW` added per
  the constraint above. The "pin a version in a comment" mitigation for
  raw-patch fragility should point at `Taskfile.test-infra.yml`'s
  `ENVOY_GATEWAY_VERSION` (currently `v1.7.4`) — that's the actually-deployed
  EG controller that interprets this CR's schema — not `go.mod`'s
  `github.com/envoyproxy/gateway` (`v1.8.1`), which pins the unrelated Go API
  types the extension server code imports and is already a different version
  than what's deployed.
- **Multi-cluster/downstream wiring template**: `GatewayReconciler`
  (`internal/controller/gateway_controller.go:2521-2584`) is the reference
  for watching a downstream-only resource and mapping events back to the
  upstream owner — `downstreamclient.TypedEnqueueRequestForUpstreamOwner[T]`
  + `mcsource.TypedKind(...).ForCluster("", r.DownstreamCluster)` +
  `.WatchesRawSource(...)`. The CNI-published `EndpointSlice` is
  **downstream-native** (published directly in the edge cluster where Envoy
  and the VPC pod's node both live) — it has no upstream counterpart to
  replicate from. Confirmed by a full sweep of `internal/downstreamclient/`
  and every downstream watch in this repo (`Certificate`, `EnvoyPatchPolicy`,
  `DNSRecordSet`): every one maps back through labels NSO itself stamped on
  an object *it created* as a replica of something upstream. There is
  genuinely **no existing precedent** for reading a downstream object NSO did
  not create and enqueuing off it — this is novel infrastructure, not an
  application of an existing pattern, and should be prototyped as its own
  spike before the rest of step 3 is built on top of it.
- **Trust boundary on the tenant-id label**: the extension server would treat
  a bare label on a downstream `EndpointSlice` as the sole signal for which
  tenant's VRF a cluster binds to, for a mechanism whose entire purpose is
  tenant isolation. Worth stating plainly: NSO's own manager already holds
  unrestricted `create/update/patch/delete` RBAC on downstream
  `discovery.k8s.io/endpointslices` (`config/rbac_downstream/role.yaml`) — it
  synthesizes them today for ordinary backends — so a labeling bug in NSO's
  *own* existing code, not just a hypothetical external actor, could
  mis-route tenant traffic. This needs an explicit provenance answer (an
  owner-reference check, a reserved label namespace with admission control on
  who may set it, or equivalent) agreed with #854/#855 before step 4 ships,
  not just "confirm the label name."
- **IPv6 is the implicit end-to-end assumption.** Networks now default to
  IPv6-only addressing (#383, merged into `main`). Nothing in the
  backendRef/HTTPRoute path validates or special-cases `EndpointSlice`
  `AddressType` today — it's copied straight through — so the pass-through
  design isn't blocked by any IPv4-only assumption. But CNI-published
  EndpointSlices will very likely be `AddressType: IPv6` end to end, and
  there is no NSO-side gate to catch a mismatch if #854/#855 don't agree on
  that. State it as an explicit cross-team dependency, not an implicit one.

## Implementation plan

### 1. API: new HTTPProxy backend kind

`api/v1alpha/httpproxy_types.go` — add to `HTTPProxyRuleBackend`, mutually
exclusive with `Endpoint`/`Connector`. This requires relaxing `Endpoint` from
`+kubebuilder:validation:Required` to optional and authoring a new
three-way CEL oneOf across `endpoint`/`connector`/`vpcPod` — there's no
existing backend-kind exclusivity rule in this type to copy, only the
unrelated filter-type exclusivity on `Filters`:

```go
type HTTPProxyRuleBackend struct {
    Endpoint  string               `json:"endpoint,omitempty"`
    Connector *ConnectorReference  `json:"connector,omitempty"`
    VPCPod    *VPCPodBackendRef    `json:"vpcPod,omitempty"`
    TLS       *HTTPProxyBackendTLS `json:"tls,omitempty"`
    Filters   []gatewayv1.HTTPRouteFilter `json:"filters,omitempty"`
}

type VPCPodBackendRef struct {
    // Name of the EndpointSlice galactic-cni publishes for the target pod.
    // Must exist in the same namespace as this HTTPProxy.
    Name string `json:"name"`
    Port int32  `json:"port"`
}
```
Run `make manifests generate api-docs` after.

### 2. HTTPProxy controller: reference, don't synthesize

`internal/controller/httpproxy_controller.go`, `collectDesiredResources`:
when `backend.VPCPod != nil`, bypass the *entire* endpoint-URL-parsing/
scheme/host-rewrite block (~lines 877-996) as well as the
synthesize-EndpointSlice path (lines 1006-1033) — both run unconditionally
today before any backend-type branch, so this isn't a bottom-of-the-function
skip. Build the `HTTPBackendRef` directly from `backend.VPCPod.Name`/`.Port`
(still `Group=discovery.k8s.io, Kind=EndpointSlice`, same as today — just
naming an existing object instead of one this controller owns). Because a
`VPCPod` backend never gets appended to `desiredResources.endpointSlices`,
the existing `CreateOrUpdate`/`SetControllerReference` loop naturally never
touches the CNI-owned object — no extra guarding needed there.

Add an early existence/namespace check (`Get` the named `EndpointSlice` in
the HTTPProxy's own namespace) and **wire a NotFound result into an actual
status condition** — today a `collectDesiredResources` error just bubbles up
as a generic requeue (`Reconcile`, lines 195-198); it does not produce a
condition on its own. Reuse the existing `programmedCondition`
conflict-reason pattern (see the EndpointSlice-`AlreadyExists` handling)
with a new reason for "referenced EndpointSlice not found," rather than
assuming the `Get` alone is sufficient.

`internal/validation/httpproxy_validation.go`: reject `VPCPod` refs in
`validateHTTPProxyRuleBackend` (lines 134-231) the same way the existing
`Connector.Name` check works (lines 217-226) — namespace is implicitly the
HTTPProxy's own, so there's no cross-namespace field to even validate
against (this is what makes the "same namespace" decision above
zero-new-plumbing). Note this file is hand-rolled Go `field.ErrorList`
validation, not CEL, despite the CEL rule added in step 1 for backend-kind
exclusivity — the two validation mechanisms coexist in this codebase today.

### 3. Gateway controller: don't turn it into a Service

`internal/controller/gateway_controller.go`,
`processDownstreamHTTPRouteRules`, inside `case KindEndpointSlice:`
(lines 2318-2493): branch on whether the referenced upstream `EndpointSlice`
carries the CNI's tenant-id label (e.g. `galactic.datum.net/tenant-id`,
matching #855's expected label per its rescoped issue) **before the
unconditional `upstreamClient.Get` + `gatewayControllerGCFinalizer` stamp at
the top of the case (lines 2320-2339)** — the CNI EndpointSlice has no
upstream counterpart, so that `Get` 404s if the branch doesn't short-circuit
ahead of it, not just ahead of the Service-synthesis step further down. If
the label is present, skip Service+EndpointSlice synthesis *and* the
`BackendTLSPolicy` create/delete logic at the tail of the case
(lines 2424-2492 — there's no synthesized `Service` for it to target), and
instead resolve the **downstream-native** EndpointSlice directly off
`r.DownstreamCluster` (not the mapped-namespace upstream replication path —
this object was never upstream to begin with) and pass the backendRef
through with that name/namespace, unmodified `Kind=EndpointSlice`. If the
label is absent, fall through to the existing behavior unchanged.

This needs a watch on the downstream cluster's `EndpointSlice` objects keyed
by tenant-id label so a CNI-side change (pod added/removed) re-triggers the
owning Gateway — same `mcsource.TypedKind(...).ForCluster("",
r.DownstreamCluster)` shape as the existing downstream watches in
`SetupWithManager` (lines 2521-2584), except the enqueue function maps by
backendRef name rather than `TypedEnqueueRequestForUpstreamOwner` (there is
no upstream owner to map to). **Treat this watch as the plan's riskiest,
least-precedented piece** — confirmed there is no existing example anywhere
in this repo of NSO reading a downstream object it did not create and
enqueuing off it; prototype it in isolation before building the rest of this
step on top of it.

The pass-through EndpointSlice gets no finalizer or owner reference under
this path, so its lifecycle (created/deleted as the VPC pod comes and goes)
is left entirely to `galactic-cni` (#854) — state that as a deliberate
decision. It's a safe omission given today's downstream-GC controller only
acts on upstream deletions and synthesized-name matches, but it should be
written down rather than left implicit.

### 4. Extension server: watch the EndpointSlice, patch the cluster

`internal/extensionserver/cache/builder.go`, `primeObjects` (lines 71-78):
add `&discoveryv1.EndpointSlice{}` (this is the same downstream-native
object #3 resolves — the extension server already runs in the downstream
cluster, so this is a local, same-cluster watch, no cross-cluster plumbing).
This also needs a **manual** RBAC edit to
`config/extension-server/rbac/role.yaml` (`discovery.k8s.io/endpointslices:
get,list,watch`) — that file is hand-maintained, `make manifests` does not
generate it and will not catch a missing grant here.

New mutation function, same package/convention as
`mutate/connector.go:186-232` (`protojson.Unmarshal` into
`*clusterv3.Cluster` — do not hand-build the proto struct), invoked from
`PostTranslateModify`'s existing cluster loop: for any cluster whose backend
resolves to a tenant-annotated `EndpointSlice`, patch
`upstream_bind_config.socket_options` with a generic `SOL_SOCKET`/
`SO_BINDTODEVICE` socket option at `STATE_PREBIND`, `buf_value` set to the
VRF device name `#855` creates for that tenant identifier. The proto shape
is settled (verified against kernel/Envoy semantics, not just this repo's
prior art) — what's still open is naming, and two hard constraints:
- The device name is capped at **`IFNAMSIZ - 1` = 15 characters** — agree a
  length-bounded naming scheme with #855 (e.g. a hash of the tenant
  identifier) rather than the raw `vrf-<tenant-id>` string, which will
  exceed this for longer tenant IDs.
- `SO_BINDTODEVICE` requires **`CAP_NET_RAW` on the Envoy container itself**
  (step 5) — separate from, and in addition to, the sidecar's
  `CAP_NET_ADMIN`.

Validate the charset of any tenant-influenced value (tenant identifier, VRF
device name) before interpolating it into the hand-built JSON literal — the
existing convention's `%q` interpolation is only safe because every current
use is a Kubernetes-object-derived, DNS-1123-safe string; this is the first
mutation in this file driven by a value this codebase doesn't control end to
end.

**Resolve the tenant-id label's trust boundary before this ships**: NSO's
own manager already holds unrestricted CRUD RBAC on downstream
`EndpointSlice` objects (it synthesizes them today for ordinary backends),
so a labeling bug in NSO's own code — not just an external actor — could
mis-route tenant traffic if the label is the only signal this mutation
trusts. Agree a provenance check (owner-reference validation, a reserved/
admission-controlled label namespace, or equivalent) with #854/#855, not
just the label's name and schema.

Still validate end-to-end in #858's ContainerLab environment before trusting
this in production — the proto shape and capability requirements are now
settled, but Envoy's own container image may apply a seccomp profile that
blocks the `setsockopt` call even with the capability granted, and that's
only observable against a real kernel/Envoy build.

### 5. Sidecar injection

`config/dev/downstream_resources/downstream-gateway.yaml` and
`config/e2e-downstream/envoyproxy.yaml` (confirmed the only two `EnvoyProxy`
CR instances in this repo — no other overlay needs this edit): add the
sidecar container (image/binary from #855) to
`spec.provider.kubernetes.envoyDeployment.pod.container` list, alongside the
existing Coraza init-container block, with `CAP_NET_ADMIN`. `pod`,
`container`, and `initContainers` are native, independent sibling fields on
`envoyDeployment` — not gated behind that manifest's separate
`patch.type: StrategicMerge` block, which only patches
`selector`/`template.metadata.labels` for an unrelated reason. **Also add
`CAP_NET_RAW` to the existing Envoy `container` entry** in the same
manifest — required for its `SO_BINDTODEVICE` call in step 4, and easy to
miss since it's not the container being newly added.

Flag explicitly in review: PR #851 calls this raw-patch mechanism fragile
across upstream Envoy Gateway version bumps (no first-class API for it) —
worth a comment in the manifest pointing at `Taskfile.test-infra.yml`'s
`ENVOY_GATEWAY_VERSION` (the actually-deployed EG controller, currently
`v1.7.4`) so a future bump is a deliberate, tested action — not `go.mod`'s
`github.com/envoyproxy/gateway` version, which pins the unrelated Go API
types the extension server imports and is already a different version
(`v1.8.1`) than what's deployed.

### 6. Tests

- Unit/envtest (`internal/controller/httpproxy_controller_test.go`,
  `gateway_controller_test.go`): new backend kind resolves to the referenced
  EndpointSlice (not a synthesized one); missing/wrong-namespace reference
  produces a clear failure condition; tenant-annotated EndpointSlice skips
  Service synthesis in the Gateway controller; un-annotated EndpointSlice
  backends are unaffected (regression coverage for the existing path).
  `TestHTTPProxyCollectDesiredResources`'s shared post-table assertions
  hardcode a 1:1 backend↔EndpointSlice relationship and
  `Kind == "EndpointSlice"` for every case — a `VPCPod` case producing zero
  synthesized slices needs its own test function, not a new row in that
  table.
- Extension server unit tests (`internal/extensionserver/mutate/*_test.go`
  pattern, e.g. `connector_test.go`): cluster patch fires only for
  tenant-annotated EndpointSlices, socket option matches expected VRF device
  name (including the length-bound case), existing clusters are untouched.
- Sidecar presence: no existing test template covers this — the Coraza
  precedent (`test/e2e-edge/_steps/assert-config-dump.yaml`) validates a
  filter registered in Envoy's own xDS config, which doesn't apply to a
  sidecar process that never registers there. A pod-spec-level assertion
  (e.g. Chainsaw checking `spec.containers[*].name` on the generated
  Deployment) needs to be authored from scratch.
- e2e (`test/e2e/`): defer full end-to-end validation to whatever
  ContainerLab scenario #858 stands up (real VRF/SRv6/eBPF only exist there
  per PR #851's own Infrastructure Needed section) — a Chainsaw scenario in
  this repo can cover the API/CRD/controller behavior (backend resolves,
  status reflects readiness) but cannot validate actual traffic reaching a
  VPC pod without that environment's kernel-level pieces.

## Sequencing

Land as separate PRs in this order, each independently mergeable and
testable:
1. API + HTTPProxy controller (steps 1-2) — no behavior change until a
   tenant actually sets `vpcPod`.
2. Prototype the downstream-native EndpointSlice watch in isolation (see
   step 3's novelty note) before committing to the rest of the Gateway
   controller branch — this is the one piece of this plan with no existing
   pattern to fall back on if the first design doesn't work.
3. Gateway controller branch (step 3) — depends on #854 publishing the
   tenant-id label; coordinate the label name/schema **and its provenance/
   trust story** with #854 before merging, not just the name.
4. Extension server mutation (step 4) — depends on #855's VRF device naming
   convention (length-bounded, see step 4) and requires the manual
   `config/extension-server/rbac/role.yaml` edit; coordinate the naming
   convention before merging, and treat the tenant-provenance question as a
   blocker — the socket-bind proto shape itself is settled, not the open
   item here.
5. Sidecar injection manifest change (step 5) — can land independently,
   gated on #855 publishing a container image. Must include the
   `CAP_NET_RAW` grant on the Envoy container, not only `CAP_NET_ADMIN` on
   the sidecar.

## Verification

- `make manifests generate api-docs` after the API change; `make test`
  (envtest) after each controller change.
- Manually edit and diff-review `config/extension-server/rbac/role.yaml`
  after step 4 — `make manifests` does not cover this file.
- `task validate-kustomizations` after the manifest change.
- Cross-check the tenant-id label name, its provenance/trust story, and the
  VRF device naming convention (length-bounded to `IFNAMSIZ - 1`) directly
  against #854/#855's actual implementations (not just their issue text)
  before wiring steps 3-4 — those are the integration seams most likely to
  drift from this plan.
- Confirm #854/#855 agree on `AddressType: IPv6` end to end for
  CNI-published EndpointSlices — nothing in this repo gates on it, so a
  mismatch fails silently rather than loudly.
- Real traffic validation (client → Envoy → VRF/SRv6 → VPC pod) only happens
  in #858's ContainerLab lab, once that exists — not achievable in this
  repo's envtest/Chainsaw suite alone.

## Before writing code

Update #856's title/body to match this plan (mirroring the rescoping edits
#854 and #855 already got) — it currently documents `VPCAttachment`,
GatewayClass→VPC mapping, and Karmada propagation, none of which this plan
builds. Leaving it stale invites the same confusion this planning pass had
to untangle.
