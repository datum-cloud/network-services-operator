# Runbook: Cell location alerts

A cell controller manager works out the location it serves from the
ServingLocation delivered to its cluster, or from `location.name` in its server
config when none is delivered. It reports the result in
`nso_cell_location_identity_waiting`, which is 1 while it cannot, labelled with
the reason.

## CellLocationUnresolved

**Meaning (warning).** For 10 minutes, the cell could not name its location.
The reason label says why:

- `NoLocationIdentity`: no ServingLocation has been delivered and
  `location.name` is not configured.
- `AmbiguousLocationIdentity`: more than one ServingLocation has been delivered
  and no location is configured to fall back to.

**Impact.** The cell allocates no interface addresses and publishes no workload
records to the federation hub. Workloads in this location keep running, but new
interfaces stay unbound and HTTP proxies served from other locations cannot
reach them.

**Diagnose.**

```sh
kubectl get servinglocations
kubectl -n <namespace> logs <pod> | grep 'knows its location'
```

**Remediate.** Deliver exactly one ServingLocation to the cluster, or set
`location.name` in the cell's server config. The cell picks the location up as
soon as it arrives, publishes what it was holding back, and the alert clears.
