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

- **HTTPProxy backend model** (`api/v1alpha/httpproxy_types.go`) has no
  discriminated backend kind — just `HTTPProxyRuleBackend{Endpoint, Connector,
  TLS, Filters}`. Needs a new mutually-exclusive field.
- **`HTTPProxyReconciler.collectDesiredResources`**
  (`internal/controller/httpproxy_controller.go:752-1074`) currently
  *synthesizes its own* `EndpointSlice` per backend
  (`httpproxy_controller.go:1010-1039`) and points the generated
  `HTTPBackendRef` at it (`Group=discovery.k8s.io, Kind=EndpointSlice`,
  lines 1041-1051). PR #851 requires the *opposite* for a VPC backend: resolve
  to the **existing** CNI-published `EndpointSlice`, never synthesize a
  second one — synthesizing would separate the pod address from its SID
  annotation, which is the one thing the whole mechanism depends on staying
  joined.
- **`GatewayReconciler.processDownstreamHTTPRouteRules`**
  (`internal/controller/gateway_controller.go:2250-2483`) already has a
  `case KindEndpointSlice:` (lines 2281-2387) but today it *always* builds a
  downstream `Service` + `EndpointSlice` pair and rewrites the backendRef to
  `Kind=Service`. A VPC backend must **not** go through a ClusterIP `Service`
  — Envoy needs a real endpoint address for the VRF/SID mechanism to work, so
  this needs a second branch under `KindEndpointSlice` that passes the
  EndpointSlice through untouched (by tenant-id annotation, not a new Kind).
- **Extension server** (`internal/extensionserver/`) only implements
  `PostTranslateModify` (`server/server.go:96`), and its cache only primes
  `TrafficProtectionPolicy`, `HTTPProxy`, `Connector`, `Namespace`
  (`cache/builder.go:71-78`) — **no `EndpointSlice` watch today**. There is
  also **no existing per-cluster socket-option mechanism anywhere in the
  repo** (`grep -rn "SocketOption"` — zero hits); this is new functionality.
  All existing xDS mutation in this package goes through
  `protojson.Unmarshal` of a hand-built JSON literal into a go-control-plane
  proto (see `mutate/connector.go:186-232`, `buildConnectorCluster`) — follow
  that convention rather than building `clusterv3.Cluster{}` field-by-field.
- **Sidecar injection mechanism**: NSO doesn't touch the Envoy `Deployment`
  in Go code at all. The existing precedent for adding a container to the
  generated Envoy pod is a static `EnvoyProxy` CR with
  `spec.provider.kubernetes.envoyDeployment.pod.{volumes,initContainers}` +
  `.patch.type: StrategicMerge` — see the Coraza WAF init-container in
  `config/dev/downstream_resources/downstream-gateway.yaml:55-74`. The
  sidecar goes in the same manifest, as a long-running `container` (not
  `initContainer` — it needs to run for the pod's whole lifetime, same
  constraint PR #851 calls out), with `CAP_NET_ADMIN`.
- **Multi-cluster/downstream wiring template**: `GatewayReconciler`
  (`internal/controller/gateway_controller.go:2485-2549`) is the reference
  for watching a downstream-only resource and mapping events back to the
  upstream owner — `downstreamclient.TypedEnqueueRequestForUpstreamOwner[T]`
  + `mcsource.TypedKind(...).ForCluster("", r.DownstreamCluster)` +
  `.WatchesRawSource(...)`. The CNI-published `EndpointSlice` is
  **downstream-native** (published directly in the edge cluster where Envoy
  and the VPC pod's node both live) — it has no upstream counterpart to
  replicate from, which is a different shape than every other
  `downstreamclient` use in this repo (those all replicate an
  upstream-owned object down). Model it as a plain **read** off the
  downstream cluster, not a replicated object.

## Implementation plan

### 1. API: new HTTPProxy backend kind

`api/v1alpha/httpproxy_types.go` — add to `HTTPProxyRuleBackend`, mutually
exclusive with `Endpoint`/`Connector` (CEL validation, same pattern as the
existing oneOf-style checks in this type):

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
when `backend.VPCPod != nil`, skip the existing synthesize-EndpointSlice path
(lines 1010-1039) entirely and build the `HTTPBackendRef` directly from
`backend.VPCPod.Name`/`.Port` (still `Group=discovery.k8s.io,
Kind=EndpointSlice`, same as today — just naming an existing object instead
of one this controller owns). Add an early existence/namespace check (`Get`
the named `EndpointSlice` in the HTTPProxy's own namespace) so a bad
reference fails at reconcile time with a clear condition, not a silent
dangling backendRef.

`internal/validation/httpproxy_validation.go`: reject `VPCPod` refs with
this rule reusing the same shape as
`validateBackendObjectReference`(`httproute_validation.go:213-215`) —
namespace is implicitly the HTTPProxy's own, so there's no cross-namespace
field to even validate against (this is what makes the "same namespace"
decision above zero-new-plumbing).

### 3. Gateway controller: don't turn it into a Service

`internal/controller/gateway_controller.go`,
`processDownstreamHTTPRouteRules`, inside `case KindEndpointSlice:`
(lines 2281-2387): branch on whether the referenced upstream `EndpointSlice`
carries the CNI's tenant-id label (e.g. `galactic.datum.net/tenant-id`,
matching #855's expected label per its rescoped issue). If present, skip the
downstream Service+EndpointSlice synthesis and instead resolve the
**downstream-native** EndpointSlice directly off `r.DownstreamCluster` (not
the mapped-namespace upstream replication path — this object was never
upstream to begin with, per the codebase-facts note above) and pass the
backendRef through with that name/namespace, unmodified Kind=EndpointSlice.
If the label is absent, fall through to the existing Service-synthesis
behavior unchanged.

This needs a watch on the downstream cluster's `EndpointSlice` objects keyed
by tenant-id label so a CNI-side change (pod added/removed) re-triggers the
owning Gateway — same `mcsource.TypedKind(...).ForCluster("",
r.DownstreamCluster)` shape as the existing downstream watches in
`SetupWithManager` (lines 2485-2549), except the enqueue function maps by
backendRef name rather than `TypedEnqueueRequestForUpstreamOwner` (there is
no upstream owner to map to).

### 4. Extension server: watch the EndpointSlice, patch the cluster

`internal/extensionserver/cache/builder.go`, `primeObjects` (lines 71-78):
add `&discoveryv1.EndpointSlice{}` (this is the same downstream-native
object #3 resolves — the extension server already runs in the downstream
cluster, so this is a local, same-cluster watch, no cross-cluster plumbing).

New mutation function, same package/convention as
`mutate/connector.go:186-232` (`protojson.Unmarshal` into
`*clusterv3.Cluster` — do not hand-build the proto struct), invoked from
`PostTranslateModify`'s existing cluster loop: for any cluster whose backend
resolves to a tenant-annotated `EndpointSlice`, patch
`upstream_bind_config.socket_options` (or `upstream_bind_config.source_address`
+ device-bind socket option — confirm exact Envoy proto field during
implementation, this repo has zero prior art here) naming the VRF device
`#855` creates for that tenant identifier (device-naming convention must be
agreed with #855 — likely `vrf-<tenant-id>`, confirm against #855's actual
implementation before wiring this, since #856 must match its naming exactly
or the socket bind resolves nothing).

Note the design doc's own caveat here as a real risk, not a formality: the
socket-bind-option proto shape is explicitly called out as unverified
against a real kernel/Envoy build in PR #851's Risks section — validate this
in the ContainerLab environment (#858) before trusting it, same as the doc
recommends.

### 5. Sidecar injection

`config/dev/downstream_resources/downstream-gateway.yaml` and the e2e
equivalent (`config/e2e-downstream/envoyproxy.yaml`): add the sidecar
container (image/binary from #855) to
`spec.provider.kubernetes.envoyDeployment.pod.container` list, alongside the
existing Coraza init-container block, with `CAP_NET_ADMIN` and no other
elevated privilege. No Go code — this is pure config, following the exact
precedent already in that file (lines 55-74).

Flag explicitly in review: PR #851 calls this raw-patch mechanism fragile
across upstream Envoy Gateway version bumps (no first-class API for it) —
worth a comment in the manifest pointing at the pinned EG version
(`go.mod`'s `github.com/envoyproxy/gateway` version) so a future bump is a
deliberate, tested action, not a silent break.

### 6. Tests

- Unit/envtest (`internal/controller/httpproxy_controller_test.go`,
  `gateway_controller_test.go`): new backend kind resolves to the referenced
  EndpointSlice (not a synthesized one); missing/wrong-namespace reference
  produces a clear failure condition; tenant-annotated EndpointSlice skips
  Service synthesis in the Gateway controller; un-annotated EndpointSlice
  backends are unaffected (regression coverage for the existing path).
- Extension server unit tests (`internal/extensionserver/mutate/*_test.go`
  pattern, e.g. `connector_test.go`): cluster patch fires only for
  tenant-annotated EndpointSlices, socket option matches expected VRF device
  name, existing clusters are untouched.
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
2. Gateway controller branch (step 3) — depends on #854 publishing the
   tenant-id label; coordinate the label name/schema with #854 before
   merging.
3. Extension server mutation (step 4) — depends on #855's VRF device naming
   convention; coordinate before merging, and treat the socket-option proto
   shape as unverified until tested against #858's environment.
4. Sidecar injection manifest change (step 5) — can land independently,
   gated on #855 publishing a container image.

## Verification

- `make manifests generate api-docs` after the API change; `make test`
  (envtest) after each controller change.
- `task validate-kustomizations` after the manifest change.
- Cross-check the tenant-id label name and VRF device naming convention
  directly against #854/#855's actual implementations (not just their issue
  text) before wiring steps 3-4 — those are the two integration seams most
  likely to drift from this plan.
- Real traffic validation (client → Envoy → VRF/SRv6 → VPC pod) only happens
  in #858's ContainerLab lab, once that exists — not achievable in this
  repo's envtest/Chainsaw suite alone.

## Before writing code

Update #856's title/body to match this plan (mirroring the rescoping edits
#854 and #855 already got) — it currently documents `VPCAttachment`,
GatewayClass→VPC mapping, and Karmada propagation, none of which this plan
builds. Leaving it stale invites the same confusion this planning pass had
to untangle.
