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
  - [IPv4 Ingress Data Plane](#ipv4-ingress-data-plane)
  - [Backend Health](#backend-health)
  - [TLS](#tls)
  - [Gateway API L4 Compatibility](#gateway-api-l4-compatibility)
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
service. Each selected location receives an IP per requested family and a
generated hostname users can target with a CNAME. Status shows the hostname,
allocated IPs, and announcement state for each location. By default, those
locations follow the network interfaces selected by the backend services.
Active health checks keep failed backends out of rotation. TLS listeners can
route by SNI and either pass encryption to the backend or terminate it with a
Datum-managed certificate.

## Motivation

`NetworkService` identifies workloads and their ports, but does not give them a
public Layer 4 entry point. Consumers need a stable address and port without
managing Galactic resources. They also need stable addresses in the locations
where their service runs.

### Goals

- Publish a TCP or UDP `NetworkService` through a named listener and separate
  route.
- Actively check backend health before serving traffic.
- Support opaque TLS over TCP, SNI-based TLS passthrough, and managed TLS
  termination for non-HTTP applications.
- Allocate a distinct public address in every backend location by default,
  including locations added after the address request is created.
- Issue a stable, platform-managed hostname in each selected location so users
  can point a DNS CNAME at that location's NLB endpoint.
- Show allocated addresses and serving locations without releasing an address
  when its backends are temporarily unavailable.
- Use an address request shape that can later serve Application Load Balancers.
- Cover Gateway API's TCPRoute, UDPRoute, and TLSRoute behavior, including its
  optional L4 features, before claiming full L4 compatibility.
- Leave room for common L4 traffic controls without requiring them in the first
  release.

### Non-Goals

- HTTP routing or changing the current `HTTPProxy` API.
- Source-based matches or cross-location backend fallback in the first release.
- Anycast or another single public IP shared across locations. A future Global
  Services Load Balancer will own global entry points and cross-location
  service behavior.
- Requiring consumers to create Gateway API or Galactic resources.

## Proposal

A `NetworkLoadBalancer` names a network, requests addresses, and defines TCP,
UDP, or TLS listeners. Each listener names the address requests on which it
serves. A `NetworkLoadBalancerRoute` attaches to a named listener and points to
named ports on `NetworkService` backends. TCP and UDP listeners have one
effective catch-all route each. If several attach, the oldest wins, breaking
timestamp ties by namespace/name; all attachments remain Accepted, and native
status identifies the effective route. Each route can distribute new
connections or flows across weighted backends.

A TLS listener adds `tls.mode: Passthrough` or `Terminate`. Routes attached to
it can match SNI hostnames so several TLS services share an address and port.
Terminate mode uses a Datum-managed certificate or a referenced TLS Secret.
Plain TCP listeners continue to carry TLS without inspecting it. The initial
delivery can ship TCP/UDP routing and health checks before TLS listeners; the
TLS API remains part of this enhancement.

Each selected location receives one IP per requested family. Multiple edge
nodes may announce each IP within the location. An address request sets
`families` to one or both of `IPv4` and `IPv6`. The list is required, unique,
and order-independent, so enabling IPv4 later never changes an existing
request implicitly. The listener references
the request once for both families. Omitting `locations` defaults to
`locations.from: Backends`: an address request follows the locations of the
`NetworkService` backends on listeners using that address. `All` explicitly
selects every eligible platform location; `Names` selects only listed
locations. Adding a backend location creates an IP for each family without
changing existing location addresses. Separate requests remain available when
families need different location selections or independent listener attachment.

Each address request produces one stable generated hostname per selected
location, reported as `status.endpoints[].canonicalHostname` and shared by
its IPv4 and IPv6 addresses. Datum assigns each address request an opaque
endpoint ID unique across the platform. The hostname is
`<endpoint-id>.<location>.<managed-zone>`, with a recognizable location and no
request name or IP embedded in it. The endpoint ID is reused across locations
and stays stable while the request exists. Consumers can CNAME a name they own
for that location to it. Datum publishes an A or AAAA record only when that
family's address is allocated and announced in the location. The hostname
stays assigned during temporary health loss, even when it has no address
records. Different requests in the same location receive different hostnames.
DNS names do not carry a port; clients still connect to the listener's port.

For an address shared by several listeners or SNI routes, `Backends` selects a
location only when every positive-weight backend service on the effective
routes has a selected, bound network interface there. This keeps every route
on the IP usable in the same locations. Consumers can use separate address
requests when services have different footprints. Interface presence selects
locations; health and programming determine announcement. With no attached
route, placement is pending and no location IP is allocated.

### User Stories

- A game operator publishes the same UDP port on a different IP in each region
  where the game service has members, so clients can choose a region without
  maintaining a location list.
- The operator adds a new region while existing regional client endpoints keep
  their addresses.
- The operator CNAMEs `us.game.example.com` to the generated hostname for the
  US location and `eu.game.example.com` to the EU location.
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
- **Reusing a public IP for another load balancer misroutes traffic.** Allocate
  every location address from a globally unique public pool.
- **A `Backends` or `All` request can expand over time.** Expose each added
  location, hostname, and IP in status, and bound the number of locations and
  addresses per load balancer.
- **DNS could direct users to an unannounced IP.** Publish A and AAAA records
  only after the corresponding VIP is announced; withdraw the DNS record when
  that announcement is withdrawn. Short TTLs limit stale answers; clients with
  cached IPs can still fail after withdrawal.
- **Overlapping routes could select different backends for the same flow.** Use
  Gateway API precedence for overlapping route attachments and report the
  effective route in native status.
- **Probe failure can withdraw a healthy location.** Report probe errors
  separately from an unhealthy backend and avoid making one probe runner a
  single point of failure.
- **A TLS hostname could be served with the wrong certificate or backend.**
  Verify domain ownership before issuing managed certificates, require route
  hostnames to be covered by the listener's certificate in Terminate mode, and
  apply deterministic SNI precedence.
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
        families: [IPv6]
  listeners:
    - name: game
      protocol: UDP
      port: 27015
      addressRefs: [regional]
---
apiVersion: networking.datumapis.com/v1alpha
kind: NetworkLoadBalancerRoute
metadata:
  name: game
spec:
  parentRefs:
    - name: game
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

The address request omits `locations`, so it follows the `game` service's
selected interfaces. `locations: {from: Backends}` expresses the same choice
explicitly. Once IPv4 ingress is available, changing the request to
`families: [IPv4, IPv6]` publishes both families on the same listener and
route.

`backends` accepts multiple entries with an optional `weight` (default 1,
zero disables selection). Weights split new connections or flows, not packets
within a flow. An unresolved backend or one with no endpoints retains its
weight and its share is rejected, as Gateway API requires. Failed health probes
exclude individual endpoints while healthy endpoints remain. Status
distinguishes resolution, endpoint availability, and health.

The load balancer reports one endpoint per address request and location. Each
endpoint groups its generated hostname and addresses by family:

```yaml
status:
  addressRequests:
    - name: regional
      locationSource: Backends
      resolvedLocations: [us-central-1, eu-west-1]
  endpoints:
    - requestName: regional
      location: us-central-1
      canonicalHostname: n-7f2k9q.us-central-1.datumproxy.net
      addresses:
        - family: IPv6
          value: 2001:db8:100::10
          conditions:
            - type: Allocated
              status: "True"
            - type: Announced
              status: "True"
            - type: DNSPublished
              status: "True"
    - requestName: regional
      location: eu-west-1
      canonicalHostname: n-7f2k9q.eu-west-1.datumproxy.net
      addresses:
        - family: IPv6
          value: 2001:db8:200::11
          conditions:
            - type: Allocated
              status: "True"
            - type: Announced
              status: "True"
            - type: DNSPublished
              status: "True"
```

These names and IPs are illustrative. Endpoint identity is the address request
and location; each requested family has an address entry even while allocation
is pending. Allocation, announcement, and DNS publication are reported
independently for each family. A missing IPv4 allocation does not hide a
working IPv6 address, but the request reports partial readiness until both
are available. The first NLB release accepts only `families: [IPv6]` because
Galactic's current public ingress packet path handles IPv6. The cell intent
carries the public VIP family and each backend address family separately. A
request containing IPv4 remains rejected until the IPv4 ingress
path and its capability checks are ready. An allocation remains in status when
a route or backend is unavailable; its announcement state changes instead.
Load balancer conditions distinguish allocation, programming, and DNS
publication. Route conditions report attachment, backend resolution, and
programming. `Ready` means at least one selected location has a serving IP
published under its generated hostname; location status identifies any that
do not. An address is announced from a location only when every listener using
it is ready there. Status reports the resolved locations for each address
request and shows `Backends` as the source when `locations` is omitted. Health loss
withdraws an announcement and its DNS record without releasing its IP; after
the last selected interface leaves a location, withdraw its DNS records and
announcement before releasing its IP.

NSO resolves service membership, allocates IPs, selects eligible locations,
and reports status. Cell controllers turn resolved intent into Galactic
`NetworkRule` and `ServiceVIPBinding` objects. The initial route uses healthy
backends in the ingress location. The control plane allocates one IP per
selected location and shares that location's IP among its serving cells. It
resolves `Backends` from actual membership, not the capped
`NetworkService.status.locations` display list.

For UDP, packets from the same client address and port select the same backend
while the backend set is unchanged. A backend change may move that flow; the
first release offers no UDP session or draining guarantee.

### IPv4 Ingress Data Plane

An IPv4 public VIP can front a service whose workload address is IPv6. The
ingress family and backend family are separate properties of cell intent. The
cell validates the VIP, every backend address, and their declared families
before programming traffic. An unsupported family pair remains unprogrammed
and unannounced, with a specific reason in status. IPv4 address requests stay
disabled in the consumer API until serving cells advertise this capability.

The IPv4 gateway path needs an IPv4 listener and flow table for TCP and UDP.
Its flow identity includes the original destination VIP, protocol, port, and
client tuple, so two VIPs that target the same backend cannot merge into one
flow. For an IPv4 client and IPv6 backend, use a stateful L4 proxy: it accepts
the IPv4 connection or UDP flow, opens an IPv6 backend flow, and sends replies
through the same state. The current IPv6 DSR forwarding path cannot supply
this cross-family return path. The proxy contract must specify what source
address the backend sees; client address delivery, if offered, needs an
explicit mechanism and cannot be assumed from DSR behavior.

The IPv4 path must handle IPv4 and IPv6 checksums, MTU and fragmentation,
ICMP errors, TCP resets and timeouts, and UDP flow expiry in both directions.
IPv4 VIPs receive distinct per-location /32 announcements, gated by healthy
backends and successful programming. Withdrawal completes before IPAM releases
an address. A location reports IPv4 and IPv6 programming and announcement
independently, so failure of one family does not hide the state of the other.

Rollout requires a public IPv4 IPAM class, family-aware cell and Galactic
programming, and a capability signal from every serving gateway in a location.
CI must exercise IPv4 TCP and UDP ingress to IPv6 backends and replies, two
VIPs targeting the same backend port, backend health changes, and announcement
withdrawal. Only then should admission accept `families` containing IPv4 and
allow one address request to publish both families on a listener.

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
one healthy backend set for all gateway nodes in that location so they make
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
  parentRefs:
    - name: services
      listener: secure
  sniHostnames: [db.example.com]
  backends:
    - networkService:
        name: database
        port: database
      transport:
        protocol: TCP
```

`sniHostnames` is valid only on a TLS listener. Exact and `*.` wildcard DNS
names use Gateway API hostname intersection and precedence rules with the
listener's optional `hostname`. Each effective hostname belongs to one route
on that listener; an omitted SNI match is the single default for clients
without a matching SNI. Without a
default, the listener rejects those connections. Terminate mode requires
verified hostnames covered by a ready certificate before serving them. Status
reports hostname verification and certificate readiness per listener and
location, separately from VIP allocation. A certificate failure withdraws
that listener's address from the affected location until it is ready;
unrelated addresses remain available. A no-SNI client needs an explicit
`certificate.defaultHostname` and a default route, or the connection is rejected.

The terminating listener alternatively accepts
`certificate: {mode: SecretRef, secretRef: {name: ...}}`; cross-namespace
references require an explicit grant. The terminated route chooses backend
transport explicitly: plain `TCP` as shown, or `TLS` with a server name and
certificate validation (system roots or referenced CA bundle). Passthrough
uses `tls.mode: Passthrough`, omits the certificate, and keeps end-to-end TLS;
its routes do not set backend transport. Only visible SNI can be matched;
connections without a matching visible name use the default or are rejected.

A CNAME to the generated location hostname changes DNS resolution, not the
client's TLS SNI. For TLS passthrough or termination on a customer's hostname,
that hostname must also appear in `sniHostnames`; termination needs a covering
certificate, while passthrough needs one on the backend. A generated hostname
does not automatically authorize or issue a certificate for the customer's
name.

SNI inspection and TLS termination need a proxy-capable data plane; Galactic's
current DSR rule only forwards TCP and cannot provide either. HTTP routing and
termination remain with `HTTPProxy`. A terminating proxy opens a new backend
connection, so its source-IP behavior and optional PROXY protocol delivery
must be explicit to consumers.

### Gateway API L4 Compatibility

The Datum API is the primary consumer experience. Its L4 behavior must cover
[TCPRoute](https://gateway-api.sigs.k8s.io/guides/user-guides/tcp/),
[UDPRoute](https://gateway-api.sigs.k8s.io/guides/user-guides/udp/),
[TLSRoute](https://gateway-api.sigs.k8s.io/reference/api-types/tlsroute/),
and their Core and Extended features. A Gateway API adapter must translate
Kubernetes objects into the same internal intent, so users can choose either
API. Full compatibility means passing the relevant Gateway API conformance
tests and documenting any implementation-specific backend mapping.

| Gateway API capability | Datum API behavior |
| --- | --- |
| Gateway addresses, named TCP/UDP/TLS listeners, ports and TLS mode | `NetworkLoadBalancer.exposure.addresses` and `listeners`; Datum additionally selects locations and allocates one address in each. |
| TCPRoute and UDPRoute, listener attachment, backendRefs and weights | `NetworkLoadBalancerRoute.parentRefs` and weighted `backends`; one effective catch-all per TCP/UDP listener, with Gateway API precedence. |
| TLSRoute SNI hostnames, wildcard matching and precedence | TLS listener `hostname` and route `sniHostnames`; passthrough and termination, including distinct TLS listeners on one port when hostnames do not conflict. |
| TLS certificates and backend re-encryption | Listener managed certificate or Secret reference; route backend TLS with SNI and trust validation, corresponding to BackendTLSPolicy. |
| AllowedRoutes, parentRefs, cross-namespace ReferenceGrant and status | Explicit attachment policy and grants; report Accepted, ResolvedRefs and programming per attachment, plus listener conflicts and readiness. |
| ListenerSet and multiple parent attachments | Adapter expands these into listeners and route attachments while preserving ownership, conflict resolution and status for each source object. |

The native route needs multiple `parentRefs` so one route can attach to several
listeners or load balancers, with optional listener name or port selection.
Listener attachment policy defaults to the same namespace; cross-namespace
attachment and backend or certificate references require grants. The adapter
maps Kubernetes Services and their numbered ports to resolved backends;
`NetworkService` remains the native backend type. It must preserve Gateway API
rules for invalid backend shares, conflicting listeners and routes, hostname
intersection, and per-parent status. TCP and UDP may share a port; TCP and TLS
may not. Datum-only source-CIDR and locality policies cannot
change the behavior of a Gateway API resource unless explicitly configured
through a Datum extension.

The first delivery may expose a smaller native feature set, but it must not
claim Gateway API L4 compatibility until TCPRoute, UDPRoute, TLSRoute, their
L4 Extended features, and the adapter pass conformance. Track the target
against a pinned Gateway API release; the upstream API can evolve independently.

### Route Controls

The listener selects destination IP, protocol, and port. The route selects a
backend for each TCP connection or UDP flow. The following controls are
candidates for later releases beyond Gateway API L4 parity:

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
Galactic's current rule has a backend list but no weights, route matches, or
priority policy. Weighted selection is required for Gateway API parity;
the other controls need implementation before the API accepts them.

## Production Readiness Review Questionnaire

### Feature Enablement and Rollback

Enable the feature only in locations with a capable Galactic gateway and IP
pool. Disabling reconciliation alone must not strand announced IPs: rollback
needs an ordered withdrawal and release path that preserves allocations until
advertisements are gone.

### Rollout, Upgrade and Rollback Planning

Start with single-location TCP and UDP with weighted backends and active probes,
then test multi-location operation with distinct addresses. Add SNI passthrough
before managed TLS termination. Exercise route updates, UDP flow stability,
probe failures, loss of the last backend, location removal, TLS certificate
issuance and rotation, CNAME resolution, A/AAAA publication, IP replacement
without hostname change, and withdrawal before IP release. Existing
`NetworkService` and `HTTPProxy` behavior should be unaffected.

### Monitoring Requirements

Operators need counts of allocated VIPs, programmed rules, healthy backends,
announced locations, and published DNS records, plus failures to allocate,
probe, program, publish DNS, issue certificates, or withdraw. Consumers read
load balancer and route conditions, backend health, generated location
hostnames, TLS certificate status, and per-address announcement and DNS status.

### Dependencies

IPAM must provide globally unique public IPv6 VIPs, one for each selected
location. NSO must allow UDP `NetworkService` ports and require the route
listener and backend port protocols to match; `HTTPProxy` must continue to
reject UDP backends. Selected cells need a health probe runner, Galactic
gateways, backend VIP bindings, and a way to report programming and
announcement state.
IPv4 VIPs additionally need a public IPv4 pool, family-aware cell intent,
Galactic IPv4 ingress and cross-family proxy support, and a serving capability
gate before API admission is enabled.
SNI routing and managed TLS need a proxy-capable edge path, hostname ownership
verification, and certificate issuance and distribution.
NSO needs a managed DNS zone and per-location A/AAAA record lifecycle tied to
VIP allocation and announcement; hostname assignment must remain stable across
IP replacement and temporary health loss.
Gateway API parity also needs an attachment/grant model, Service backend
translation, Secret certificate references, weighted selection, wildcard SNI,
mixed TLS modes, backend TLS validation, per-parent status, and conformance
tests against a pinned upstream release.

### Scalability

Bound listeners, routes, address requests, and selected locations. A dynamic
`Backends` or `All` request adds an allocation and cell configuration when a new
location qualifies; reconciliation must handle those additions without
changing existing IPs.

### Troubleshooting

Report failures at the affected route, address request, and location. In
particular, distinguish an address that is allocated but not announced from an
address that could not be allocated.

## Implementation History

- 2026-10-11: Initial proposal.
- 2026-10-12: Scoped NLB addresses to one IP per selected location; deferred
  global entry points to a separate Global Services Load Balancer concept.

## Drawbacks

Separate routes add an object to a simple single-backend setup. Serving many
locations consumes one public IP in each, and each IP requires coordinated
withdrawal across the cells serving its location.

## Alternatives

- **Put exposure and backends on `NetworkService`.** This ties workload
  membership to public address and listener lifecycle.
- **Put the backend on each listener.** This is compact for one backend but
  leaves no independent route lifecycle or place for future route policy.
- **Expose Gateway API directly.** It adds concepts consumers do not need for
  this use case and does not express Datum's location and address policies.
  Offer an adapter for portability while retaining the Datum API.

## Infrastructure Needed

Public VIP pools, cell-level programming and status propagation, and a
multi-location traffic test environment are required before rollout. TLS
listeners additionally need an edge proxy tier and managed certificate
distribution.
