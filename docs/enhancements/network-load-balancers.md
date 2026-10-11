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
  - [Backend Health](#backend-health)
  - [TLS](#tls)
  - [Route Controls](#route-controls)
- [Production Readiness Review Questionnaire](#production-readiness-review-questionnaire)
- [Implementation History](#implementation-history)
- [Drawbacks](#drawbacks)
- [Alternatives](#alternatives)
- [Infrastructure Needed](#infrastructure-needed)

## Summary

A project can publish a `NetworkService` on a public TCP or UDP port through a
`NetworkLoadBalancer`. The load balancer owns listeners and public address
requests; a separate `NetworkLoadBalancerRoute` connects a listener to the
service. Consumers choose distinct IPs in each location, one IP shared across
locations, or both on the same listener. Status shows the allocated IPs and
where each is announced. Active health checks keep failed backends out of
rotation. TLS listeners can route by SNI and either pass encryption to the
backend or terminate it with a Datum-managed certificate.

## Motivation

`NetworkService` identifies workloads and their ports, but does not give them a
public Layer 4 entry point. Consumers need a stable address and port without
managing Galactic resources. They also need to choose whether clients use a
location-specific address or a shared address reachable from several locations.

### Goals

- Publish a TCP or UDP `NetworkService` through a named listener and separate
  route.
- Actively check backend health before serving traffic.
- Support opaque TLS over TCP, SNI-based TLS passthrough, and managed TLS
  termination for non-HTTP applications.
- Support per-location unicast and multi-location anycast address requests,
  including a mix of both on one load balancer.
- Show allocated addresses and serving locations without releasing an address
  when its backends are temporarily unavailable.
- Use an address request shape that can later serve Application Load Balancers.
- Leave room for common L4 traffic controls without requiring them in the first
  release.

### Non-Goals

- HTTP routing or changing the current `HTTPProxy` API.
- Weighted backends, source-based matches, or cross-location backend fallback
  in the first release.
- Requiring consumers to create Gateway API or Galactic resources.

## Proposal

A `NetworkLoadBalancer` names a network, requests addresses, and defines TCP,
UDP, or TLS listeners. Each listener names the address requests on which it
serves. A `NetworkLoadBalancerRoute` attaches to a named listener and points to
a named port on a `NetworkService`. The initial TCP/UDP slice accepts one
catch-all route per listener; a conflicting route reports a condition.

A TLS listener adds `tls.mode: Passthrough` or `Terminate`. Routes attached to
it can match SNI hostnames so several TLS services share an address and port.
Terminate mode requests a Datum-managed certificate for verified hostnames.
Plain TCP listeners continue to carry TLS without inspecting it. The initial
delivery can ship TCP/UDP routing and health checks before TLS listeners; the
TLS API remains part of this enhancement.

`Unicast` allocates one IP per selected location. `Anycast` allocates one IP
announced from all selected locations. These names describe addressing *across
locations*: multiple edge nodes may announce an IP within one location.
`locations.from: All` follows current and future eligible locations. An
explicit `Names` selection limits the request to listed locations. Adding a
location to `All` creates another unicast IP while the anycast IP stays stable.

### User Stories

- A game operator publishes the same UDP port on a different IP in each region
  so clients can choose a region.
- The operator also publishes one global IP on that listener so clients can use
  a single endpoint; the status shows which regions currently announce it.
- If a region loses its serving backends, its IP remains allocated while its
  announcement is withdrawn.
- An operator exposes a TLS application through a TCP listener; the backend
  retains its certificate and the load balancer forwards the encrypted stream.
- An operator routes `db.example.com` and `mq.example.com` on the same TLS
  port by SNI while each backend terminates its own TLS.
- An operator uses a Datum-managed certificate on a TLS listener, so a
  non-HTTP backend can receive decrypted TCP without managing that certificate.

### Risks and Mitigations

- **An announced IP without serving backends drops traffic.** Announce a
  location only after its rule is programmed with healthy backends; withdraw it
  when the last healthy backend disappears. Workload readiness alone does not
  prove the application answers on its port.
- **Reusing an anycast IP for another load balancer misroutes traffic.** Hold
  one globally unique allocation and propagate that allocation to every
  selected location.
- **An `All` request can expand over time.** Expose each added location and IP
  in status, and bound the number of locations and addresses per load balancer.
- **Overlapping routes could select different backends for the same flow.** When
  match-based routes are added, require one default and deterministic precedence
  or reject ambiguous overlaps.
- **Probe failure can withdraw a healthy location.** Report probe errors
  separately from an unhealthy backend and avoid making one probe runner a
  single point of failure.
- **A TLS hostname could be served with the wrong certificate or backend.**
  Verify domain ownership before issuing managed certificates, require route
  hostnames to be covered by the listener's certificate in Terminate mode, and
  reject ambiguous SNI matches.
- **One unready listener can affect other ports on its IP.** BGP announces an
  address, not a port. Announce an address from a location only when all of its
  listeners are ready there; use separate address requests for independent
  failure domains.

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
      protocol: UDP
    - name: health
      port: 8080
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
        family: IPv6
        locations:
          from: All
      - name: global
        mode: Anycast
        family: IPv6
        locations:
          from: All
  listeners:
    - name: game
      protocol: UDP
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
  backends:
    - networkService:
        name: game
        port: game
      healthCheck:
        protocol: HTTP
        port: health
        path: /healthz
```

`backends` is a list so a later weighted split or ordered failover does not
require replacing the field. The first release requires exactly one entry.

The load balancer reports one entry per allocated IP. Unicast produces an entry
per location; anycast produces one entry with several announcements:

```yaml
status:
  addresses:
    - requestName: regional
      family: IPv6
      value: 2001:db8:100::10
      announcements:
        - location: us-central-1
          state: Announced
    - requestName: regional
      family: IPv6
      value: 2001:db8:200::11
      announcements:
        - location: eu-west-1
          state: Announced
    - requestName: global
      family: IPv6
      value: 2001:db8:300::20
      announcements:
        - location: us-central-1
          state: Announced
        - location: eu-west-1
          state: Announced
```

These IPs are illustrative. IPv6 is shown because Galactic's current public
ingress packet path handles IPv6. IPv4 address requests need IPv4 ingress
support before they can be served and must be rejected where unavailable. An
allocation remains in status when a route or backend is unavailable; its
announcement state changes instead. Load balancer conditions distinguish
allocation and programming. Route conditions report attachment, backend
resolution, and programming. `Ready` means at least one
selected location serves traffic; location status identifies any that do not.
An address is announced from a location only when every listener using it is
ready there.

NSO resolves service membership, allocates IPs, selects eligible locations,
and reports status. Cell controllers turn resolved intent into Galactic
`NetworkRule` and `ServiceVIPBinding` objects. The initial route uses healthy
backends in the ingress location. The same anycast IP is installed in every
selected cell; the control plane must not allocate one IP per cell for it.

For UDP, packets from the same client address and port select the same backend
while the backend set is unchanged. A backend change may move that flow; the
first release offers no UDP session or draining guarantee.

### Backend Health

Health checks are configured per route backend. A TCP backend defaults to a
TCP connect probe on its serving port; it may instead name a TCP, HTTP, or
HTTPS probe on another `NetworkService` port. A UDP backend must name a TCP,
HTTP, or HTTPS health endpoint that reports whether the UDP application can
serve. There is no generic UDP probe with reliable success semantics. Probe
interval, timeout, and healthy and unhealthy thresholds have platform defaults
and can be set on the health check.

An endpoint serves only when its workload reports `HolderAvailable` and its
active probe passes. Probes run from each serving location. The cell publishes
one healthy backend set for all of its gateway nodes so anycast nodes make
consistent selections. A location with no healthy backends withdraws its
announcement but keeps its IP allocation. Status reports healthy backend
counts and probe failures by location.

### TLS

A TCP listener forwards TLS bytes unchanged to a backend that owns its
certificate. A TLS listener adds two explicit modes:

- **Passthrough:** Inspect the ClientHello SNI only to choose a route, then
  forward the encrypted stream. The backend owns its certificate.
- **Terminate:** Select a certificate and end client TLS at the load balancer.
  `certificate.mode: Managed` uses Datum's domain verification and certificate
  lifecycle. The route must explicitly choose plain TCP or re-encrypted TLS
  for the backend connection.

The proposed TLS-specific fields are:

```yaml
# NetworkLoadBalancer spec fragment
spec:
  listeners:
    - name: secure
      protocol: TLS
      port: 443
      tls:
        mode: Terminate
        certificate:
          mode: Managed
          hostnames: [db.example.com, mq.example.com]
---
# NetworkLoadBalancerRoute spec fragment
spec:
  parentRef:
    name: services
    listener: secure
  sniHostnames: [db.example.com]
  backends:
    - networkService:
        name: database
        port: database
      transport:
        protocol: TCP
```

`sniHostnames` is valid only on a TLS listener. Initial matching uses exact
hostnames. Each hostname belongs to one route on that listener; an omitted SNI
match is the single default for clients without a matching SNI. Without a
default, the listener rejects those connections. Terminate mode requires
verified hostnames covered by a ready certificate before serving them. Status
reports hostname verification and certificate readiness per listener and
location, separately from VIP allocation. A certificate failure withdraws
that listener's address from the affected location until it is ready;
unrelated addresses remain available. A no-SNI client needs an explicit
`certificate.defaultHostname` and a default route, or the connection is rejected.

The terminated route chooses backend transport explicitly: plain `TCP` as
shown, or `TLS` with a server name and certificate validation. Passthrough
uses `tls.mode: Passthrough`, omits the certificate, and keeps end-to-end TLS;
its routes do not set backend transport. Only visible SNI can be matched;
connections without a matching visible name use the default or are rejected.

SNI inspection and TLS termination need a proxy-capable data plane; Galactic's
current DSR rule only forwards TCP and cannot provide either. HTTP routing and
termination remain with `HTTPProxy`. A terminating proxy opens a new backend
connection, so its source-IP behavior and optional PROXY protocol delivery
must be explicit to consumers.

### Route Controls

The listener selects destination IP, protocol, and port. The route selects a
backend for each TCP connection or UDP flow. The following controls are
candidates for later releases, after the single-backend catch-all route works:

- **Weighted backends:** Split new TCP connections or UDP flows among
  `NetworkService` backends by weight. Changing weights must not interrupt
  established TCP connections; UDP flow stability needs an explicit contract.
- **Priority failover:** Use a standby backend when the preferred one lacks
  enough healthy members, with an explicit threshold.
- **Backend locality:** Select `LocalOnly`, `PreferLocal`, or `Global` backends
  relative to the ingress location. This does not change which locations
  announce the VIP. Cross-location forwarding needs a verified DSR return path.
- **Source CIDR match:** Steer client networks to different backends. Multiple
  routes on one listener need a default and unambiguous precedence. This is
  traffic selection, not an access-control substitute.
- **Client affinity:** Keep new flows from one client on a consistent
  backend when needed. Clients sharing a NAT IP can create uneven load.

Connection draining, source-IP preservation, and PROXY protocol are delivery
settings rather than route matches; they need their own data plane contracts.
Galactic's current rule has a backend list but no weights, route
matches, or priority policy, so these controls need implementation before the
API accepts them.

## Production Readiness Review Questionnaire

### Feature Enablement and Rollback

Enable the feature only in locations with a capable Galactic gateway and IP
pool. Disabling reconciliation alone must not strand announced IPs: rollback
needs an ordered withdrawal and release path that preserves allocations until
advertisements are gone.

### Rollout, Upgrade and Rollback Planning

Start with single-location TCP and UDP with active probes, then test
multi-location unicast and anycast. Add SNI passthrough before managed TLS
termination. Exercise route updates, UDP flow stability, probe failures, loss
of the last backend, location removal, TLS certificate issuance and rotation,
and withdrawal before IP release. Existing `NetworkService` and `HTTPProxy`
behavior should be unaffected.

### Monitoring Requirements

Operators need counts of allocated VIPs, programmed rules, healthy backends,
and announced locations, plus failures to allocate, probe, program, issue
certificates, or withdraw. Consumers read load balancer and route conditions,
backend health, TLS hostname and certificate status, and per-address
announcement status.

### Dependencies

IPAM must provide globally unique anycast VIPs and location-specific unicast
VIPs. NSO must allow UDP `NetworkService` ports and require the route listener
and backend port protocols to match; `HTTPProxy` must continue to reject UDP
backends. Selected cells need a health probe runner, Galactic gateways,
backend VIP bindings, and a way to report programming and announcement state.
IPv4 VIPs additionally need Galactic IPv4 ingress support.
SNI routing and managed TLS need a proxy-capable edge path, hostname ownership
verification, and certificate issuance and distribution.

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
multi-location traffic test environment are required before rollout. TLS
listeners additionally need an edge proxy tier and managed certificate
distribution.
