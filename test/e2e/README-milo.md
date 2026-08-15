# Milo in the e2e environment

> **This environment does not test per-project authorization.**
>
> A grant made anywhere in Milo authorizes **every** project path. If you are
> looking for evidence that project A's credentials cannot act in project B,
> this environment does not contain it, and no suite here asserts it. That
> property is verified against staging only.
>
> What this environment *does* prove is that a request lands in the project its
> path named. That is real, it is load-bearing, and it is what the interface
> suites assert.

`test-infra:up` runs a real Milo on the upstream cluster. It replaced a ~400-line
stand-in proxy (`test/e2e/milo-shim`) that terminated the project control-plane
path and re-issued the request against the kind apiserver as one fixed tenant.
The stand-in proved the operator addressed the right path; it could not prove
anything about what Milo does with that path, because it *was* the thing doing
it.

## Why authorization is not covered

Milo scopes a request to a project by putting the project in the caller's
**user extras** — `iam.miloapis.com/parent-api-group`, `parent-type`,
`parent-name` — in `ProjectContextAuthorizationDecorator`. Stock Kubernetes RBAC
ignores user extras entirely, and Milo's RBAC informers read its root object
store. So under `--authorization-mode=RBAC`:

- Roles and RoleBindings created *inside* a project control plane are stored,
  and are readable back through that project's path, but are **never
  evaluated**.
- A ClusterRoleBinding in Milo's root store authorizes its subjects on every
  project path at once.

This was measured on a scratch cluster before this environment was built, not
inferred. A Role and RoleBinding granting a test user `list configmaps`,
created inside project A's control plane, produced `Forbidden` through project
A's own path. Moving the identical grant to the root store made the request
succeed — through project A's path *and* through project B's, where no grant
existed at all.

Closing this would mean deploying the OpenFGA authorization provider
(`milo-os/openfga-provider`), whose `SubjectAccessReview` authorizer does read
the `parent-*` extras. That is a second repository, an OpenFGA server, a
controller-manager, `PolicyBinding` objects in place of RBAC, and webhook
certificate plumbing. It was considered and deliberately not done.

Milo's own e2e skips its authorization suite for the same reason
(`--selector "requires!=authorization-provider"`).

## What *is* covered, and how

Routing. IPAM is aggregated into Milo, not into the kind apiserver — the kind
registration is deleted at deploy time precisely so nothing can reach IPAM
without passing through Milo. A request travels:

```
suite / operator
  → https://<milo>/apis/resourcemanager.miloapis.com/v1alpha1/projects/<id>/control-plane/apis/ipam.miloapis.com/...
  → Milo strips the prefix, resolves <id>, injects the parent-* extras
  → Milo's aggregator proxies to ipam-system/ipam-apiserver,
    authenticating with its front-proxy client certificate
  → IPAM verifies that certificate against --requestheader-client-ca-file,
    believes X-Remote-Extra-*, and scopes storage to <id>
```

Nothing in any credential names a project. `networkinterfaceclaim-project-isolation`
leans on this directly: both projects define an IPClass called
`datum-endpoint-v4`, backed by disjoint space (`10.128.0.0/16` vs
`10.129.0.0/16`), so a request that routed to the wrong project still succeeds
but allocates from a visibly wrong range.

Its negative control also runs through Milo now. The "project-less" request is
the *same* identity and the *same* front door, addressed at Milo's root rather
than at a project — so only the path differs between the allowed read and the
blind one. Previously it was an impersonation against the kind apiserver: a
different server and a different mechanism, which could have kept passing even
if project routing were broken.

## The one hand-wired thing

Milo's aggregator refuses to proxy an APIService that is not marked `Available`,
and Milo's apiserver never marks a **remote** one: it sets
`DisableRemoteAvailableConditionController = true`
(`cmd/milo/apiserver/config.go`). `test-infra:milo-bootstrap` writes the
condition.

This matches production rather than diverging from it. In staging the condition
is written by `milo-controller-manager`'s `RemoteAPIServiceAvailabilityReconciler`,
which sets `Available=True` **unconditionally** — it performs no probe, no dial,
and no health check of any kind. Writing the condition here produces the same
object from the same evidence. The reconciler is not deployed because running
it would mean the entire Milo controller-manager plus an infrastructure-cluster
CRD set, to perform one unconditional write.

The environment does not rely on that condition for readiness.
`test-infra:milo-ipam-gate` reads real IPPools through each project's path and
requires Milo's root path to reach IPAM and read nothing — strictly more
evidence than the production mechanism collects. A dead or unreachable IPAM
fails there, at env-up, rather than as an unexplained 503 inside a suite.

(Staging's live condition could not be read back directly — `apiservices` is
forbidden for ordinary users on that control plane — so the equivalence rests on
Milo's source and on `datum-system/milo-controller-manager` being deployed
there, not on inspecting the object.)

## Shape

- Milo's deployment comes from the kustomize bundle Milo publishes on release,
  `ghcr.io/milo-os/milo-kustomize`, fetched by digest — the same arrangement as
  IPAM and the dns-operator. `config/dependencies/milo/root-kustomization.yaml`
  composes the bundle's apiserver and its certificate components with this
  environment's additions; `test-infra:milo-bundle` assembles the two into a
  staging directory, and `test-infra:milo-render` prints the result.
- What is local is only what this environment adds. Apiserver and etcd, nothing
  else: no controller-manager, no gateway, no audit or tracing sinks. Project
  control-plane paths serve without any of them. Upstream's own
  `overlays/test-infra` pulls all of those in, which is why the bundle's pieces
  are composed rather than that overlay consumed.
- `config/dependencies/milo/overlay/` — the local additions, and nothing from
  the bundle, so it builds on a clean checkout where the bundle has not been
  fetched: the namespace, etcd, the static tokens, and the front-proxy client
  certificate. `config/dependencies/milo/patches/` holds the two patches that
  need the bundle's objects to patch.
- `config/dependencies/milo/bootstrap-in-milo.yaml` — applied **inside** Milo:
  the two `Project` objects, and the root-scoped RBAC discussed above.
- etcd is a plain single-member `StatefulSet` on `emptyDir`. Upstream ships it
  as a Flux `HelmRelease`; installing Flux to obtain one StatefulSet is more
  machinery than the StatefulSet.
- The bundle's apiserver drives every flag from an env var and ships no volumes,
  so `patches/apiserver-patch.yaml` is where storage, credentials and
  certificates are wired. A bundle default this environment cannot satisfy is a
  startup failure rather than a fallback, which is why the tracing config, the
  etcd client certificates and the authn/authz webhook configs are blanked
  rather than left alone.
- The Milo version is pinned by `MILO_BUNDLE_TAG` in `Taskfile.test-infra.yml`
  and by the image digest in `root-kustomization.yaml`. They must name the same
  release, or the manifests and the binary they configure have drifted.

## Identities

Milo authenticates by static token (`config/dependencies/milo/overlay/auth.yaml`).

| user | groups | used by |
| --- | --- | --- |
| `e2e-admin` | `system:masters` | env bootstrap only — creating Projects, registering the APIService |
| `nso-manager` | `system:authenticated` | the operator, via the `ipam-cluster-kubeconfig` secret |
| `e2e-tenant-tester` | `system:authenticated` | the suites and the fixture seeder |

The last two are deliberately not `system:masters`, so a suite that passes does
so on a real authorization decision — just not a per-project one.

Note that authorization happens in **two** places, and both grants must exist:
Milo authorizes the caller before its aggregator proxies anything
(`bootstrap-in-milo.yaml`), and IPAM then authorizes again through a delegated
`SubjectAccessReview` against the **kind** apiserver
(`config/dependencies/ipam/overlay/rbac.yaml`, `test/e2e/fixtures/ipam/rbac.yaml`).
IPAM's delegated authorization was left pointing at kind because, under
RBAC-only Milo, both answer the same question the same way — Milo's RBAC is
root-scoped and knows nothing about projects either. Moving it would add a
credential and a second RBAC universe and change no decision.

## Reaching Milo by hand

```bash
# as the suites do, scoped to one project
./hack/ipam-project-kubectl.sh project-alpha -n default get ipclaims

# as the env bootstrap does
kubectl --kubeconfig "${TMPDIR:-/tmp}/.milo-admin.yaml" get projects
```

Milo is published on NodePort 32450, mapped to the same host port by
`config/tools/kind/upstream-cluster.yaml`, because the chainsaw suites run on
the host.
