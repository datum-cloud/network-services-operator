# IPAM test fixtures

Test data for the suites that exercise NetworkInterfaceClaim allocation against
a real IPAM. None of it is needed to deploy IPAM — that lives in
`config/dependencies/ipam/`.

This directory holds no `chainsaw-test.yaml`, so chainsaw walks past it: it
recurses looking for test files and ignores directories without one, the same
way it ignores the `networkbinding/` grouping directory.

| file | what it is |
|---|---|
| `RANGES.md` | The range allocation per project, the class model, and the rules that keep concurrent suites from colliding. **Read this before adding a pool.** |
| `project-alpha/`, `project-beta/` | `IPClass` and `IPPool` seeds. Identical class names in both projects, disjoint address space, so a controller routing to the wrong project allocates from visibly wrong space instead of quietly succeeding. |
| `namespaces.yaml` | The `ipam-e2e-*` namespaces a claim resolves its project from, carrying the real encoded `meta.datumapis.com/upstream-cluster-name` label — plus one deliberately without it, for the fail-closed case. |
| `rbac.yaml` | Binds the identity the suites impersonate to the operator's own tenant role. |

Applied by `task test-infra:ipam-fixtures` and `task test-infra:ipam-namespaces`,
both of which run as part of `test-infra:up` and again at the head of every
suite-running task.

These are seeded, not asserted on directly: they are the address space the
suites allocate from. `IPPool` and `IPClass` are cluster-scoped, so chainsaw's
namespace teardown never reaches them — `task test-infra:ipam-fixtures-clear`
is what stops a run that died mid-suite from poisoning the next one.
