# Dependencies

Deployment configuration for services NSO depends on but does not own — other
teams' components, deployed here so the test environment can exercise the real
integration instead of a stand-in.

One named subdirectory per dependency.

| directory | what it is |
|---|---|
| `ipam/` | The Milo IPAM aggregated apiserver (`ipam.miloapis.com`), the project control plane NSO claims addresses from. |

## How this differs from the neighbours

- **`config/tools/`** — things that support the environment rather than
  participate in the feature under test: cert-manager, Envoy Gateway,
  external-dns, kind cluster configs. A dependency here is something NSO's own
  controllers talk to at runtime; a tool is scaffolding.
- **`config/e2e/`, `config/dev/`** — NSO's *own* deployment, per environment.

## What belongs here, and what does not

Deployment configuration only: what it takes to stand the dependency up. If a
file exists to make a test assert something, it is test data and lives in
`test/fixtures/<dependency>/` instead.

The line is easy to blur, because the only consumer of this directory today is
the test environment — which makes everything in it feel like test scaffolding.
It is not. The test:

> If the suites were deleted tomorrow, would this file still be needed to run
> the dependency?

For `ipam/`, `overlay/` and `patches/` survive that question and stay; the
IPClass/IPPool seeds, the `ipam-e2e-*` namespaces, and the `e2e-tenant-tester`
binding do not, and live in `test/e2e/fixtures/ipam/`.

Identities are split on the same line. `overlay/rbac.yaml` grants what the
operator needs to assert a project; the identity the suites impersonate is
bound to that same role from `test/e2e/fixtures/ipam/rbac.yaml`.

## Convention

A dependency is consumed at the version the Go module graph already pins, so
the manifests deployed and the client NSO compiles against cannot drift apart.
Prefer the upstream project's own published manifests over copies kept here:
`ipam/` pulls a digest-pinned OCI bundle at task time and keeps only this repo's
additions in `overlay/` and `patches/`. The name mirrors IPAM's own
`config/dependencies/postgres-operator`.
