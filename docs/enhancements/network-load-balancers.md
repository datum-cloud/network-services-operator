---
status: provisional
stage: alpha
latest-milestone: "v0.x"
---

# Network Load Balancers

- [Summary](#summary)
- [Motivation](#motivation)
  - [Goals](#goals)
  - [Non-Goals](#non-goals)
- [Proposal](#proposal)
  - [User Stories](#user-stories)
  - [Risks and Mitigations](#risks-and-mitigations)
- [Design Details](#design-details)
- [Production Readiness Review Questionnaire](#production-readiness-review-questionnaire)
- [Implementation History](#implementation-history)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
- [Infrastructure Needed](#infrastructure-needed)

## Summary

A project can publish a `NetworkService` on a public TCP port through a
`NetworkLoadBalancer`. The load balancer owns listeners and public address
requests; a separate `NetworkLoadBalancerRoute` connects a listener to the
service. Consumers choose distinct IPs in each location, one IP shared across
locations, or both on the same listener. Status shows the allocated IPs and
where each is announced.

## Motivation

`NetworkService` identifies workloads and their ports, but does not give them a
public Layer 4 entry point. Consumers need a stable address and port without
managing Galactic resources. They also need to choose whether clients use a
location-specific address or a shared address reachable from several locations.

### Goals

- Publish a TCP `NetworkService` through a named listener and separate route.
- Support per-location unicast and multi-location anycast address requests,
  including a mix of both on one load balancer.
- Show allocated addresses and serving locations without releasing an address
  when its backends are temporarily unavailable.
- Use an address request shape that can later serve Application Load Balancers.

### Non-Goals

- HTTP routing, TLS termination, or changing the current `HTTPProxy` API.
- UDP, active health probes, weighted backends, source-based matches, or
  cross-location backend fallback in the first release.
- Requiring consumers to create Gateway API or Galactic resources.

## Proposal

A `NetworkLoadBalancer` names a network, requests addresses, and defines TCP
listeners. Each listener names the address requests on which it serves. A
`NetworkLoadBalancerRoute` attaches to a named listener and points to a named
port on a `NetworkService`. The first release accepts one catch-all route per
listener; a conflicting route reports a condition.

`Unicast` allocates one IP per selected location. `Anycast` allocates one IP
announced from all selected locations. These names describe addressing *across
locations*: multiple edge nodes may announce an IP within one location.
`locations.from: All` follows current and future eligible locations. An
explicit `Names` selection limits the request to listed locations. Adding a
location to `All` creates another unicast IP while the anycast IP stays stable.

### User Stories

- A game operator publishes the same TCP port on a different IP in each region
  so clients can choose a region.
- The operator also publishes one global IP on that listener so clients can use
  a single endpoint; the status shows which regions currently announce it.
- If a region loses its serving backends, its IP remains allocated while its
  announcement is withdrawn.

### Risks and Mitigations

- **An announced IP without serving backends drops traffic.** Announce a
  location only after its rule is programmed with usable backends; withdraw it
  when the last usable backend disappears.
- **Reusing an anycast IP for another load balancer misroutes traffic.** Hold
  one globally unique allocation and propagate that allocation to every
  selected location.
- **An `All` request can expand over time.** Expose each added location and IP
  in status, and bound the number of locations and addresses per load balancer.

## Design Details

The consumer-facing shape is:

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: NetworkService
metadata:
  name: game
spec:
  networkInterfaces:
    selector:
      matchLabels:
        compute.datumapis.com/workload-name: game
  ports:
    - name: game
      port: 27015
      protocol: TCP
---
apiVersion: networking.datumapis.com/v1alpha
kind: NetworkLoadBalancer
metadata:
  name: game
spec:
  networkRef:
    name: default
  exposure:
    addresses:
      - name: regional
        mode: Unicast
        family: IPv4
        locations:
          from: All
      - name: global
        mode: Anycast
        family: IPv4
        locations:
          from: All
  listeners:
    - name: game
      protocol: TCP
      port: 27015
      addressRefs: [regional, global]
---
apiVersion: networking.datumapis.com/v1alpha
kind: NetworkLoadBalancerRoute
metadata:
  name: game
spec:
  parentRef:
    name: game
    listener: game
  backend:
    networkService:
      name: game
      port: game
```

The load balancer reports one entry per allocated IP. Unicast produces an entry
per location; anycast produces one entry with several announcements:

```yaml
status:
  addresses:
    - requestName: regional
      family: IPv4
      value: 192.0.2.10
      announcements:
        - location: us-central-1
          state: Announced
    - requestName: regional
      family: IPv4
      value: 192.0.2.11
      announcements:
        - location: eu-west-1
          state: Announced
    - requestName: global
      family: IPv4
      value: 198.51.100.20
      announcements:
        - location: us-central-1
          state: Announced
        - location: eu-west-1
          state: Announced
```

These IPs are illustrative. An allocation remains in status when a route or
backend is unavailable; its announcement state changes instead. Load balancer
conditions distinguish allocation and programming. Route conditions report
attachment, backend resolution, and programming. `Ready` means at least one
selected location serves traffic; location status identifies any that do not.

NSO resolves service membership, allocates IPs, selects eligible locations,
and reports status. Cell controllers turn resolved intent into Galactic
`NetworkRule` and `ServiceVIPBinding` objects. The initial route uses healthy
backends in the ingress location. The same anycast IP is installed in every
selected cell; the control plane must not allocate one IP per cell for it.

## Production Readiness Review Questionnaire

### Feature Enablement and Rollback

Enable the feature only in locations with a capable Galactic gateway and IP
pool. Disabling reconciliation alone must not strand announced IPs: rollback
needs an ordered withdrawal and release path that preserves allocations until
advertisements are gone.

### Rollout, Upgrade and Rollback Planning

Start with single-location TCP, then test multi-location unicast and anycast.
Exercise route updates, loss of the last backend, location removal, and
withdrawal before IP release. Existing `NetworkService` and `HTTPProxy`
behavior should be unaffected.

### Monitoring Requirements

Operators need counts of allocated VIPs, programmed rules, and announced
locations, plus failures to allocate, program, or withdraw. Consumers read
load balancer and route conditions and the per-address announcement status.

### Dependencies

IPAM must provide globally unique anycast VIPs and location-specific unicast
VIPs. Selected cells need Galactic gateways, backend VIP bindings, and a way
to report programming and announcement state.

### Scalability

Bound listeners, routes, address requests, and selected locations. An `All`
request adds an allocation and cell configuration when a new location becomes
eligible; reconciliation must handle those additions without changing existing
IPs.

### Troubleshooting

Report failures at the affected route, address request, and location. In
particular, distinguish an address that is allocated but not announced from an
address that could not be allocated.

## Implementation History

- 2026-10-11: Initial proposal.

## Drawbacks

Separate routes add an object to a simple single-backend setup. Unicast across
many locations consumes an IP in each one, and anycast requires coordinated
withdrawal across cells.

## Alternatives

- **Put exposure and backends on `NetworkService`.** This ties workload
  membership to public address and listener lifecycle.
- **Put the backend on each listener.** This is compact for one backend but
  leaves no independent route lifecycle or place for future route policy.
- **Expose Gateway API directly.** It adds concepts consumers do not need for
  this use case and does not express Datum's location and address policies.

## Infrastructure Needed

Public VIP pools, cell-level programming and status propagation, and a
multi-location traffic test environment are required before rollout.
