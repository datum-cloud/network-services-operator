# Infra handoff: a network in every location it is used

What [the design](network-in-every-location.md) needs from outside this
repository. None of it is in the NSO change, and the feature does not work
without it. Ordered by how quietly it fails.

## 1. The fleet selector on the propagation policy

`config/federation/clusterpropagationpolicy.yaml` in this repository selects
clusters by one affinity:

```yaml
- key: infra.datum.net/gateways
  operator: In
  values: [enabled]
```

That is the gateway edge fleet. The fleet that needs `NetworkContext` and
`Location` is **every fleet a network consumer runs in**, which today also means
compute cells. If those are not the same set of clusters, the objects simply
never arrive and nothing reports an error: a consumer's binding goes `Ready` on
the hub while the location it names never sees the network.

**Decide one of:** converge the labels so compute cells carry
`infra.datum.net/gateways=enabled`, or give these two kinds their own
`ClusterPropagationPolicy` with its own affinity. This must be settled before
the feature ships.

## 2. CRDs where the objects land

- **`Location` on the hub, and on every cell it propagates to.** Neither has the
  CRD today.
- **`NetworkContext` on every cell.** `config/crd/downstream` installs only
  `TrafficProtectionPolicy`, `HTTPProxy` and `Connector` — the three the gateway
  resource replicator mirrors. `NetworkContext` has to be added to whatever
  installs CRDs on the fleets chosen in (1).

## 3. Consumer RBAC on the hub

Compute's hub `ClusterRole` grants no `networking.datumapis.com` at all, so the
first consumer cannot create a `NetworkBinding` and is blocked outright. Every
subsequent consumer needs the same grant: `create`, `get`, `list`, `watch` and
`delete` on `networkbindings` in the project's hub namespace.

## 4. An NSO manager on the cells

Nothing runs one today. The claim reconciler, the cell-local finalizer that
holds a context while a retained address is still held, and everything else at
the location need one. This is the largest single dependency and it is not a
networking design problem.

## 5. The `Location` copy's propagation label

The policy selects NSO's namespaced kinds by
`meta.datumapis.com/upstream-cluster-name` with `operator: Exists` — the value
is never matched, only its presence. A `Location` is cluster-scoped and belongs
to no project, so nothing in this repository can derive a correct value for it.

The replicator stamps a configurable constant,
`locationReplication.propagationClusterName`, defaulting to `datum-platform`.
Two consequences infra owns:

- **It must not collide with a real project name.** A project name there would
  silently claim every location for that project.
- **If the policy is ever tightened** from `Exists` to a value match, this
  default has to be revisited or locations stop propagating.

## 6. Things that are dependencies, not infra work

Recorded so they are not mistaken for the above.

- **Nothing sets `Programmed`** on a `NetworkContext` in any repository, and
  `Ready` is derived from it. On the hub this means a binding reports
  `NetworkContextNotReady` indefinitely. The presence controller is correct and
  the contract is unreachable until something programs the context.
- **The address family default** must be resolved: `Network.ipFamilies` defaults
  to `[IPv4]` and `NetworkInterfaceClaim.ipFamilies` to `[IPv6]`, so a default
  consumer on a default network rejects everywhere, forever. The fix is a
  compute-side API change (an unset `ipFamilies` on a claim meaning "whatever
  the network carries"), not one this repository can land.
- **Nothing seeds per-project `IPClass` and `IPPool` objects.** They exist only
  as chainsaw fixtures, so a network can be present in a location and a claim
  will still find nothing to allocate from.
