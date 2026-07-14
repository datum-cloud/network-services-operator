---
description:
alwaysApply: true
---

# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Response Style

**Be concise. Always.**
- Short, direct answers
- Bullet points over paragraphs
- Code examples over lengthy explanations
- Tables for comparisons
- Minimize context unless explicitly asked
- Get to the point immediately

## Code Comments

**Default to zero comments.** Well-named identifiers and the surrounding code
should convey what the code does. Do not narrate changes, reference tasks/PRs,
or annotate "added for X" / "removed Y".

The only exception: if a passage is genuinely difficult for a human to
comprehend, leave a single short comment that reads exactly `here be dragons`.

## What This Is

The network services operator defines APIs and core controllers for network
entities such as Networks, NetworkContexts, Subnets, Gateways, HTTPProxies, and
Domains. It is a Kubebuilder/controller-runtime operator (Go module
`go.datum.net/network-services-operator`, Go 1.26).

The operator does **not** provision resources onto data planes. It reconciles
intent declared in custom resources and relies on infrastructure providers (e.g.
[infra-provider-gcp](https://github.com/datum-cloud/infra-provider-gcp)) and
downstream components (Envoy Gateway, cert-manager, external-dns) to satisfy
that intent.

## Common Development Commands

Run `make help` for the full list. Most-used targets:

### Build & Run
- `make build` — build the manager binary to `bin/network-services`
- `make run` — run the controller from your host against the dev config
- `make docker-build IMG=<ref>` — build the manager image
- `make docker-push IMG=<ref>` — push the manager image

### Code Generation (run after editing `*_types.go` or RBAC markers)
- `make manifests` — generate CRDs, RBAC, and webhook configs into
  `config/crd/bases` (via `controller-gen`)
- `make generate` — generate `zz_generated.deepcopy.go` and defaulters
- `make api-docs` — regenerate CRD reference docs in `docs/api/` (via `crdoc`)

### Lint & Format
- `make lint` — run `golangci-lint` (config in `.golangci.yml`)
- `make lint-fix` — run `golangci-lint` with `--fix`
- `make fmt` / `make vet` — `go fmt` / `go vet`

### Tests
- `make test` — unit/integration tests via **envtest** (excludes `test/e2e`);
  runs `manifests generate fmt vet` first, writes `cover.out`
- `task test-infra:test-e2e` — **Chainsaw** e2e suite in `test/e2e` against the
  prod-fidelity env (below); pass a scenario path after `--` for a single test
- `make test-conformance` — Gateway API conformance suite in
  `test/conformance/gatewayapi`

### Prod-Fidelity Test Environment (Taskfile)
The e2e environment is two Kind clusters that mirror how the edge runs in
production: an **upstream** cluster (`nso-upstream`) running the operator, and a
**downstream** cluster (`nso-downstream`) running Envoy Gateway v1.7.4, the
extension server + Coraza WAF data plane, cert-manager, and external-dns. It is
defined in `Taskfile.test-infra.yml` and is what CI runs.

- `task test-infra:up` — build the operator image, create both clusters, install
  the downstream data plane, deploy + link the operator
- `task test-infra:test-e2e [-- <path>]` — run the e2e suite against the live env
  (JSON report written to `$TMPDIR`); optionally a single scenario
- `task test-infra:down` — tear down both clusters
- `task validate-kustomizations` — `kustomize build` every kustomization
- `task test-prometheus-rules` — `promtool` tests for the alerting rules in
  `test/prometheus-rules/`

## High-Level Architecture

### Multi-Cluster (upstream / downstream)
The operator is built on `multicluster-runtime`. It watches the **upstream**
control plane where users declare intent and reconciles into one or more
**downstream** clusters that host the data-plane components.

- `internal/downstreamclient/` — strategies for mapping upstream resources into
  downstream namespaces and enqueuing upstream owners from downstream events.
  Downstream objects carry the `compute.datumapis.com/upstream-namespace` label
  to map back to their upstream owner.
- Replicator/GC controllers mirror selected NSO CRDs (Connector, HTTPProxy,
  TrafficProtectionPolicy) into the downstream cluster and garbage-collect them.
- `make downstream-crds` installs the CRDs the replicator expects downstream.

### Binary subcommands (`cmd/main.go`)
- `network-services manager` — the controller manager (default workload)
- `network-services extension-server` — the Envoy Gateway extension server
  (see `internal/extensionserver/` and `docs/enhancements/envoy-gateway-extension-server/`)

### Source Layout
- `api/v1alpha`, `api/v1alpha1` — CRD type definitions (`*_types.go`) for group
  `networking.datumapis.com`. Kinds include Network, NetworkBinding,
  NetworkContext, NetworkPolicy, Subnet, SubnetClaim, Location, HTTPProxy,
  Domain, TrafficProtectionPolicy, Connector, ConnectorAdvertisement,
  ConnectorClass.
- `internal/controller/` — one file per reconciler (plus `_test.go`). Gateway
  handling spans several controllers (DNS, downstream cert solver, downstream GC,
  resource replicator).
- `internal/webhook/` — admission webhooks, including for external Gateway API
  and Envoy Gateway types.
- `internal/` also: `config/` (operator config + defaulters), `gatewayapi/`,
  `extensionserver/`, `validation/`, `iroh/`, `registrydata/`, `util/`.
- `config/` — Kustomize bases and overlays (`default`, `dev`, `e2e`), RBAC,
  CRDs, webhooks, and tooling installs under `config/tools/` (cert-manager,
  envoy-gateway, external-dns, Kind cluster configs).
- `test/` — `e2e/` (Chainsaw), `conformance/gatewayapi/`, `prometheus-rules/`,
  `utils/`.
- `docs/` — `api/` (generated CRD reference), `enhancements/` (design docs),
  `runbooks/`.
- `hack/` — codegen boilerplate.

### Tooling
Tool binaries are pinned in the `Makefile` and installed on demand into `bin/`:
`controller-gen` v0.16.4, `kustomize` v5.5.0, `golangci-lint` v2.12.2,
`chainsaw` v0.2.15, `kind` v0.27.0, `cmctl`, `crdoc`, `setup-envtest`. The
project follows the Kubebuilder v4 layout (`PROJECT`, domain `datumapis.com`).

## Key Development Patterns

### Changing an API
1. Edit the relevant `api/.../*_types.go` and kubebuilder markers.
2. Run `make manifests generate` to regenerate CRDs, RBAC, deepcopy, defaulters.
3. Run `make api-docs` if the CRD reference under `docs/api/` should update.
4. Add/extend reconciler logic in `internal/controller/` and a `_test.go`.
5. `make test` (envtest) and, when relevant, `task test-infra:test-e2e`.

Never hand-edit generated files (`zz_generated.*`, `config/crd/bases/*`,
`docs/api/*`) — change the source and regenerate.

### RBAC
RBAC is generated from `+kubebuilder:rbac` markers on controllers. Add the
marker, then `make manifests` — do not edit `config/rbac/role.yaml` by hand.

### Kustomize-First Config
Operator deployment config is composed from Kustomize bases and overlays under
`config/`. Validate with `task validate-kustomizations` before committing.

### CI
GitHub Actions enforce the same commands locally available:
`make test` (test.yml), the `test-infra` e2e env (test-e2e.yml, via
`task test-infra:up` + `task test-infra:test-e2e`), `golangci-lint`
(lint.yml), and kustomize validation (validate-kustomize.yaml). Run them
locally before pushing.

## Git Commit Message Format

Follow this format for all commits:

```
<type>: <subject line max 50 chars>

<Body wrapped at 80 chars explaining what and why>

Key changes:
- Bullet points for important details, wrapped at 80 chars
- Use present tense ("add" not "added")
```

**Rules:**
- **Subject line**: Imperative mood, no period, under 50 characters
- **Body**: Hard wrap at 80 characters; blank line between subject and body
- **Commit types**: feat, fix, refactor, docs, test, chore, perf, style, ci
- **No watermarks** or co-author tags
- **Focus on what/why**, not how
