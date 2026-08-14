# Controller sets

Proposal for [#368](https://github.com/datum-cloud/network-services-operator/issues/368).

## Problem

The manager registers every controller and every webhook wherever it runs. `internal/cmd/manager/manager.go:350-490` wires 20 controllers in a straight line: 14 ungated, 6 gated. Every existing gate exists for a *capability* reason — is Coraza compiled in, is dns-operator installed, is iroh configured — never for a *placement* reason.

That is safe while exactly one deployment exists. A second, per-location deployment (needed to fulfil `NetworkInterfaceClaim`s at an edge, #360) would also start the control-plane controllers, giving two reconcilers on the same objects.

`networkInterface.enabled` is the only placement-ish flag and it is one-directional: it turns the location controllers on, it cannot turn the control-plane ones off.

Three secondary gaps block a second deployment even if the controller list were solved. All three are real today:

- **Webhooks register unconditionally** (`manager.go:504`). Registering any webhook makes controller-runtime add the webhook server as a runnable, which needs a serving cert (`internal/config/config.go:346-360`). An edge with no cert secret crashloops on a server it does not need.
- **The downstream cluster is always built and started** (`manager.go:308-326`, `538-540`) and silently falls back to the operator's own in-cluster config when no kubeconfig is set (`config.go:499-505`) — at an edge, a second cache against the local cluster.
- **`LeaderElectionID` is hardcoded** to `6a7d51cc.datumapis.com` (`manager.go:286`). Two deployments of this image in one namespace contend for one lease and one parks silently.

## Design

A named, validated set list in the typed server config:

```yaml
controllers:
  sets:
    - control-plane
    - location
```

**Named sets, not a switch per controller.** Per-controller booleans admit "everything false" — boots healthy, reconciles nothing.

**Config file, not flags.** It is already the deployment-shaped surface (a mounted ConfigMap per overlay), is strictly decoded (`manager.go:60`), is validated in one place (`config.go:1358-1369`), and is logged at startup (`manager.go:193`), so the effective set is observable.

**Empty defaults to what the config already ran.** A config written before controller sets existed gets `control-plane`, plus `cell` when it set the deprecated `networkInterface.enabled`. Defaulting to every set instead would both start the location controllers where nothing asked for them and fail validation — `cell` requires a location name and namespace, which no such config carries — so every deployed ConfigMap would crashloop on upgrade. Unknown names are rejected, naming the offender and the legal values. An `all` sentinel was rejected: it is ambiguous the moment a third set lands.

**Independent of `discovery.mode`.** `initializeClusterDiscovery` (`manager.go:665-733`) has only `single` and `milo`; both dev and edge are `single`. Deriving the set from it would make "single cluster, everything" inexpressible.

### Membership

A controller is location-scoped iff its authoritative input arrives at the location **and** its output is local.

| Set | Controllers |
|---|---|
| `cell` | `networkinterfaceclaim`, `networkinterface` |
| `control-plane` | the other 18, plus all webhooks and indexers |

Only the two network-interface controllers move. Subnet, SubnetClaim, NetworkBinding, and NetworkContext look location-scoped but are near-stubs (50-184 lines each) whose propagation model is undecided — they stay control-plane until that is settled, and this file is where that decision gets recorded so it is not re-litigated from the data model.

Everything gateway-shaped stays control-plane; those controllers already hold a downstream handle. Note the DNSRecordSet collection lives *inside* the Gateway reconciler (`internal/controller/gateway_dns_controller.go:59,445`), so it cannot be placed independently of it.

### Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Set names `control-plane` / `cell` | NSO has a `Location` CRD and `networkInterface.location` config; compute's "cell" vocabulary does not otherwise exist here |
| D2 | `controllers: {sets: []}` struct, not a bare top-level list | Groups the setting like every other config section, and leaves room for per-set knobs later. Defaulting lives in `SetDefaults_NetworkServicesOperator` rather than a per-section hook because it has to read `networkInterface.enabled` |
| D3 | Webhooks tie to `control-plane` membership | Every webhook validates a gateway- or domain-shaped object owned by control-plane controllers. A separate `webhooks` set would be a knob with one legal value |
| D4 | `networkInterface.enabled` deprecated, then removed | Strict decoding means deleting it immediately crashloops any deployed config that still sets it, including `config/e2e/config.yaml:23`. Land the set with the field kept but ignored for gating; drop it once infra configs are updated. Do **not** AND the two — two switches for one thing is the silent-failure mode this is meant to kill. It is read in one place only: defaulting an absent `controllers.sets`, where it is the sole record of what the config used to run |
| D5 | Leader-election ID derived from the enabled sets, with a `--leader-election-id` override | Makes the co-location collision impossible by default. **Any set containing `control-plane` keeps the literal `6a7d51cc.datumapis.com`** — a renamed lease on upgrade means the new pod cannot see the old one and both briefly reconcile, and every in-repo overlay pins `[control-plane]`, so keying the literal to the full set alone would leave it unreachable |
| D6 | Downstream cluster built only when `control-plane` is enabled | Removes the surprise fallback at `config.go:499-505` and one full unstructured cache from the edge |
| D7 | Keep the single ClusterRole for now | controller-gen emits one role per `rbac:roleName`. A trimmed location role is a tracked follow-up, not a blocker — `TestManagerRoleGrantsEventCreation` (`internal/controller/networkinterfaceclaim_controller_test.go:792`) shows a hand-maintained role can be defended by a test |

## Implementation

Ordered. The Go change must land before the Kustomize component, or a cell pod crashloops on a missing webhook cert.

### 1. `internal/config/config.go`

- `type ControllerSet string` with `ControllerSetControlPlane` / `ControllerSetCell` constants and an `allControllerSets` slice.
- `ControllersConfig{ Sets []ControllerSet }` and a `Controllers` field on `NetworkServicesOperator` (after line 96).
- `SetDefaults_NetworkServicesOperator`: empty sets → `[control-plane]`, plus `cell` when the deprecated `networkInterface.enabled` is set.
- `(c *ControllersConfig) validate()`: reject unknown names and duplicates; non-empty post-default.
- `(c *NetworkServicesOperator) Enabled(set ControllerSet) bool`.
- Wire into `Validate()` (`config.go:1358`) ahead of the section checks.
- Move the location name/namespace requirement out of `NetworkInterfaceConfig.validate` (`config.go:166-178`) so it keys off `cell` membership rather than `enabled`. Mark `Enabled` deprecated in its doc comment.

### 2. `make generate`

Regenerates `internal/config/zz_generated.deepcopy.go` and `zz_generated.defaults.go`. Never hand-edited.

### 3. `internal/cmd/manager/manager.go`

- Compute `controlPlane` / `cell` after `Validate()` (line 195) and log both.
- Wrap under `if controlPlane`: lines 350-373, 382-457, 460-480, 482-490, 430-439, 420-428; `controller.AddIndexers` (492) and the DNSZone indexer (497); `setupWebhooks` (504) **and** the `webhookServer` construction at 225-229; the downstream cluster build (308-326) and start (538-540); `singletonMgr` (328-348).
- Change line 375 from `serverConfig.NetworkInterface.Enabled` to `cell`.
- Derive `LeaderElectionID` (286) and the singleton default (144); add the override flag.
- Fail loud when `cell` is enabled without IPAM/location config, following the existing pattern at `manager.go:633-650`.
- Extract the wiring into a testable `setupControllers(mgr, serverConfig, deps) ([]string, error)` returning registered controller names. This is the only way to make "a new controller was added and nobody classified it" a test failure.

### 4. Kustomize

`config/components/` currently holds one unreferenced component (`service-catalog`); the components overlays actually use (`../webhook`, `../rbac_deployment`, `../prometheus`, `../resource-metrics`) sit at the top level of `config/`. All four are **resource-additive**, which is load-bearing — see the validation notes below.

```
config/components/cell-controllers/   # NEW — Component: the cell Deployment
  kustomization.yaml
  deployment.yaml
  config.yaml
  metrics_service.yaml
config/cell/                          # NEW — standalone overlay for a real edge
  kustomization.yaml
  namespace.yaml
```

**The component is additive**: a second Deployment `cell-controller-manager`, its own generated ConfigMap, its own metrics Service. It contains no patches.

**No control-plane component is needed** — pinning the control plane is one line in `config/manager/config.yaml` and `config/e2e/config.yaml`. It is a no-op against the default, and states the placement rather than leaving it to a deprecated field.

**Config reaches the pod by a second generator, not a patch.** The whole operator config is a single `config.yaml` key, so a generator merge replaces it wholesale and no component can surgically patch `controllers.sets`. The component generates its own `cell-config` ConfigMap — a distinct name, so no `behavior:` juggling against the manager base's `config` (`config/manager/kustomization.yaml:6-12`). Keep `disableNameSuffixHash: true` to match; `Taskfile.test-infra.yml:243-250` pokes ConfigMaps by literal name.

Identity:

| Concern | Decision |
|---|---|
| Name | `cell-controller-manager` |
| Pod labels | `app.kubernetes.io/component: cell-controller-manager`; **never `control-plane: controller-manager`** |
| Leader election | `--leader-election-id=6a7d51cc.datumapis.com-cell` via an env indirection |
| ServiceAccount | reuse `controller-manager` — already bound to the ClusterRole (`config/rbac/role_binding.yaml:11-15`) and the lease Role, and test-infra mints the IPAM token against it |
| Metrics | `:8443` plus its own Service selecting the component label |
| Webhook cert | no volume, no `webhookServer` config |
| Downstream kubeconfig | not mounted |
| IPAM kubeconfig | moves here from `config/e2e/kustomization.yaml:37-52` |

The label rule is the sharpest co-location trap. `config/webhook/service.yaml:12-13` and `config/prometheus/metrics_service.yaml:15-17` both select `control-plane: controller-manager`. A cell pod carrying that label lands in the webhook Service's endpoints and receives admission requests it cannot answer. The same applies to the selector: `config/manager/manager.yaml:20-23` sets `matchLabels: {control-plane: controller-manager, app.kubernetes.io/name: ...}`, so a cell created by name-suffixing alone would share a pod selector with the control-plane Deployment and the two ReplicaSets would fight over each other's pods.

Args use `$(ENV_VAR)` indirections so overlays patch `env` by name instead of rewriting the args list — the pattern from `config/extension-server/deployment.yaml:51-61`, which is also the precedent for a second Deployment of this image.

The component's `config.yaml` carries `PLACEHOLDER-LOCATION` / `PLACEHOLDER-NAMESPACE` that every consuming overlay must replace, following the convention at `config/extension-server/deployment.yaml:142,149`. Two edge deployments sharing a location name would both fulfil and both release the same claims' addresses (`internal/controller/networkinterfaceclaim_controller.go:110-115`); nothing in the operator can detect this, so the placeholder is the only guard rail.

`config/cell` composes `namespace.yaml`, `../crd` (NSO CRDs only, no `../crd/gateway`), `../rbac`, the component, and `../rbac_deployment` for the namespace lease Role. It composes **no** `../webhook`, `../certmanager`, or `../prometheus`. Real edge overlays live in the infra repo and should override the location with their own `configMapGenerator` using `behavior: replace` — the form `config/e2e/kustomization.yaml:14-19` already uses — rather than a JSON patch on the config blob.

Existing files: add `controllers: {sets: [control-plane]}` to `config/manager/config.yaml` and `config/e2e/config.yaml`; add the component to `config/e2e/kustomization.yaml:9-11`; move the `networkInterface` block and the `ipam-cluster-kubeconfig` volume from the e2e overlay into the component, keeping `downstream-cluster-kubeconfig` on the control plane.

### 5. test-infra

`Taskfile.test-infra.yml:229-233` already applies `config/e2e`, so composing the component in ships the cell with no change to the apply path. Three additions:

- `wait-ready` (line 389) must wait on the new Deployment, or the NI scenarios can start before the cell has a leader.
- `ipam-kubeconfig` (~line 561) mints against SA `network-services-operator-controller-manager` before `prepare-upstream` — unchanged while the cell reuses that SA, but it must mint for a dedicated SA if one is introduced.
- Ordering is already correct; the cell crashloops briefly until IPAM is up, then recovers, same as the control plane today.

The `eppEmissionEnabled` sed (lines 243-250) and the memory patch (lines 252-254) address the control plane by name and are unaffected.

## Test plan

**envtest / unit**

- `internal/config/config_test.go`, following `TestGatewayConfig_ValidateLegacyTargetDomains` (`config_test.go:114`): empty defaults to all sets; unknown name rejected with the offender named; duplicate rejected; `cell` without a location name/namespace rejected; `control-plane`-only validates with no IPAM present; a config still setting `networkInterface.enabled` still decodes (strict-decode regression guard).
- New `internal/cmd/manager` test — the wiring block has no test today. Table-test `setupControllers`: sets → expected controller names, plus an exhaustiveness assertion that every `Named(...)` string in `internal/controller` appears in exactly one set.
- `setupWebhooks` registers nothing for a `cell`-only config.
- Leader-election ID: distinct for `[control-plane]` vs `[location]`, and the historical `6a7d51cc.datumapis.com` preserved for every set containing `control-plane`.

**Chainsaw e2e**

The upstream control-plane deployment keeps every control-plane controller, so every current scenario is an unchanged regression baseline. `test/e2e/networkinterfaceclaim-dual-stack` and `networkinterfaceclaim-reclaim-retain` keep passing, now served by the cell rather than the control plane. Add a scenario asserting the cell pod has no webhook port, no downstream kubeconfig mount, and holds a lease named `6a7d51cc.datumapis.com-cell`.

## Validation notes

`task validate-kustomizations` (`Taskfile.yaml:12-42`) runs `kustomize build` on every directory containing a `kustomization.yaml`, including component directories.

- A resource-additive component builds standalone. The proposed component passes.
- A component using the bare `patches: - path: foo.yaml` form **fails standalone** with `no resource matches strategic merge patch`. Any patch inside a component must use the `target:` selector form, which no-ops cleanly. This is the concrete reason the cell is a new Deployment rather than a patch on the existing one.
- `config/rbac/kustomization.yml` is `.yml`, so the `find -name "kustomization.yaml"` at `Taskfile.yaml:19` never validates it.
- `config/e2e/kustomization.yaml:104-135` has `replacements` targeting `kind: Certificate` with no name selector, injecting `webhook-service` DNS names. If the cell ever needs a cert, add a name selector there first.
- Component `apiVersion` must be `kustomize.config.k8s.io/v1alpha1` with `kind: Component`.

## Risks and open questions

- **Lease rename on upgrade** — mitigated by preserving the literal ID for the default set, covered by a test.
- **Strict decoding** — removing `networkInterface.enabled` before infra ConfigMaps drop it is an instant crashloop. Two steps.
- **Over-granted RBAC at the edge** until the location role is split. A location pod can, by credential, write Gateways. Worth its own issue.
- **Distinct location identity is unenforced.** Nothing in the operator detects two deployments claiming the same location; it is a deployment invariant. At minimum, log the location this process serves at startup.
- **DNSRecordSet collection cannot be placed separately** from the Gateway reconciler. Fine while both are control-plane.
- **Open:** where do Subnet / SubnetClaim / NetworkBinding / NetworkContext land?
- **Open:** does the edge need the singleton manager at all? Both singleton controllers are control-plane-only today, so it can be skipped — confirm no future location controller wants singleton semantics.
- **Compute precedent caveat:** compute's `--enable-management-controllers` / `--enable-cell-controllers` exists in a worktree, not on compute's `main`. It is a design precedent, not shipped code — confirm before citing it as settled.
