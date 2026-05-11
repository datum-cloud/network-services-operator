---
status: draft
stage: awaiting-sign-off
created: 2026-05-11
github_issue: https://github.com/datum-cloud/network-services-operator/issues/155
---

# Design Brief: Register Network Services in the Service Catalog

- [Executive Summary](#executive-summary)
- [Problem Statement](#problem-statement)
- [Scope](#scope)
- [Billing Signals](#billing-signals)
- [Monitored Resource](#monitored-resource)
- [Service Registration](#service-registration)
- [Kustomize Bundle Layout](#kustomize-bundle-layout)
- [Usage Pipeline Integration — Investigation Findings](#usage-pipeline-integration--investigation-findings)
- [Open Decisions](#open-decisions)
- [Acceptance Criteria](#acceptance-criteria)
- [Implementation Plan](#implementation-plan)
- [References](#references)

---

## Executive Summary

Network Services operates an Envoy Gateway-based edge proxy that routes HTTP
traffic, terminates TLS, and enforces WAF and rate-limit policies on behalf of
platform customers. It currently has no billing presence — no `Service`
registration, no meter definitions, and no integration with the durable usage
pipeline.

This brief defines the scope, approach, and open decisions for registering
Network Services in the platform service catalog and putting it on a path to
usage-based billing.

The work splits cleanly into two independently deliverable phases:

- **Phase 1 (catalog):** Declare the `Service` resource (which carries the
  monitored-resource configuration for the catalog) and the `MeterDefinition`
  resources. No Go code changes — pure YAML, packaged in `config/billing/` and
  `config/services/`. Deployable as soon as the billing and services CRDs are
  present in the control plane. The `MonitoredResourceType` is created
  transitively by the service catalog from the `Service` configuration; it is
  not shipped as a standalone YAML in this bundle.
- **Phase 2 (emission):** Wire the edge proxy's telemetry into the billing SDK
  and emit usage events to the Vector Agent. Requires the investigation findings
  below to be confirmed before implementation begins.

This brief covers both phases but distinguishes which decisions are resolved and
which require sign-off.

---

## Problem Statement

Network Services is a billable service with measurable consumption signals. Today
those signals are captured only as operational Prometheus metrics — there is no
path from those metrics to a billing account, and there is no `Service` resource
that makes Network Services discoverable in the platform catalog.

Addressing this before any customer-facing billing launch is materially cheaper
than retrofitting metering after the fact. The `MeterDefinition` `meterName` and
`measurement.unit` fields are immutable once the resource reaches `Published`
phase. Getting them right at `Draft` costs a YAML PR. Getting them wrong costs a
new meter family, an updated SDK, and a customer migration.

---

## Scope

### In scope

- `services.miloapis.com/v1alpha1` `Service` resource for Network Services.
- `billing.miloapis.com/v1alpha1` `MonitoredResourceType` and `MeterDefinition`
  resources for the four billing signals described below.
- `config/billing/` and `config/services/` Kustomize bundles in this repo,
  following the pattern established in `datum-cloud/cloud-portal`.
- An investigation document (this brief) covering how to extract the four
  billing signals from the Envoy Gateway proxy and emit them via the billing SDK.

### Out of scope

- Rates, pricing tiers, or invoice generation.
- Changes to the `MeterDefinition` schema or billing pipeline contract.
- The billing SDK implementation (owned by `datum-cloud/billing`).
- Changes to Envoy Gateway's deployed configuration before the investigation
  findings are confirmed.
- Cross-project billing or shared-infrastructure cost attribution.
- Promotion of any resource from `Draft` to `Published` phase (that step is
  gated on billing-team sign-off and is a separate PR).

---

## Billing Signals

These four signals are the primary consumption dimensions for an HTTP edge proxy.
They are listed in order of commercial priority.

| Signal | Proposed meterName | Unit (UCUM) | Aggregation |
|--------|--------------------|-------------|-------------|
| Request count | `networking.datumapis.com/gateway/requests` | `{request}` | Sum |
| Egress bytes | `networking.datumapis.com/gateway/egress-bytes` | `By` | Sum |
| Ingress bytes | `networking.datumapis.com/gateway/ingress-bytes` | `By` | Sum |
| Active connection seconds | `networking.datumapis.com/gateway/connection-seconds` | `s` | Sum |

### Rationale

**Request count** is the standard billing unit for reverse-proxy services
(Cloudflare, Fastly, AWS CloudFront). It directly captures platform work and is
the unit customers most naturally reason about. It is also the signal least
likely to produce billing surprises.

**Egress bytes** captures bandwidth cost. It is standard in the hyperscaler
billing model and maps directly to infrastructure cost. Metering ingress and
egress separately gives the platform flexibility to price them differently — or
not at all — without introducing a new meter.

**Connection seconds** is needed for persistent connections (WebSocket,
gRPC-streaming, long-poll). A customer running a WebSocket-heavy workload has a
cost profile that request-count billing significantly underrepresents.

### Dimensions

Each meter carries three optional dimensions for cost attribution and future
pricing tiers:

- `region` — Datum deployment region serving the request.
- `gateway` — The `Gateway` resource name, enabling per-gateway cost drill-down.
- `gateway_class` — The `GatewayClass` of the underlying gateway, so different
  gateway classes can be priced or analyzed independently.

Dimensions are declared as optional (not required) so events from proxies that
cannot populate them are not rejected at the Ingestion Gateway.

---

## Monitored Resource

The billable Kind is the **`HTTPRoute`**, not `HTTPProxy` and not `Gateway`.
Rationale:

- An HTTP request is what is actually being processed and billed.
- `Gateway` is the underlying implementation; advanced consumers may interact
  with it directly, but it is not the natural per-request billing unit.
- `HTTPRoute` is the resource customers attach to a `Gateway` to describe the
  HTTP traffic they want routed — it is the closest model of "an HTTP service
  the customer has stood up."
- Modelling the billable resource at the route layer leaves room for separate
  `TCPRoute` and `UDPRoute` monitored resources later, with distinct meter
  families if TCP/UDP traffic ends up priced differently from HTTP (which is
  common practice — see below).

### Provider precedent: HTTP vs TCP/UDP billing

A survey of the major edge / load-balancing products supports modelling HTTP
and TCP/UDP as separate billable resources with separate meter families:

| Provider | L7 product | L7 units | L4 product | L4 units |
|---|---|---|---|---|
| AWS | ALB | hourly + LCU (max of new conns/sec, **active conns**, GB, rule evals) | NLB | hourly + NLCU (max of new conns, **active conns**, GB) — distinct thresholds per TCP/UDP/TLS |
| GCP | External Application LB | forwarding rule $/hr + $/GB | External Network LB | forwarding rule $/hr + $/GB (passthrough reduced/free) |
| Cloudflare | Workers / CDN | $/million requests + CPU-ms | Spectrum | $/GB over plan allowance |
| Azure | Application Gateway | hourly + Capacity Units (compute, **persistent connections**, throughput) | Standard LB | $/hr per rule + $0.005/GB |
| Fastly | Delivery | $/10k requests + $/GB egress | (no public L4 product) | — |

Three patterns recur:

1. **Separate SKUs with separate unit names.** L7 and L4 are essentially always
   different products with different billing dimensions. The only mainstream
   exception is AWS Global Accelerator, deliberately positioned as
   protocol-agnostic anycast transport.
2. **L7 prices the work; L4 prices the pipe.** L7 SKUs expose request, rule-
   evaluation, or CPU-second dimensions. L4 SKUs collapse to bytes and
   connections.
3. **Active / persistent connections is a first-class billing dimension when
   long-lived connections matter, but normalized differently per protocol.**
   AWS uses 3 k active/LCU on ALB vs 100 k active/NLCU on NLB precisely because
   L4 connections are cheaper to hold open. Azure App Gateway exposes
   "persistent connections" as one of its Capacity Unit dimensions.

Implications for this design:

- HTTPRoute meters stay request- and rule-evaluation-oriented (our four
  signals fit cleanly).
- Future TCPRoute / UDPRoute meters should be byte- and connection-oriented,
  with no "request count" meter.
- Connection-seconds is a legitimate cross-layer signal but, if it ever feeds
  pricing, should be calibrated with different thresholds per protocol.
- A single shared meter family covering HTTP and TCP/UDP is the minority
  pattern and only makes sense for L4-agnostic transport — not an
  Envoy-Gateway L7-aware proxy.

Sources: AWS ELB pricing, AWS Global Accelerator pricing, GCP Cloud Load
Balancing pricing, Cloudflare Spectrum billing docs, Cloudflare Workers
pricing, Azure Application Gateway and Standard LB pricing, Azure Front Door
billing, Fastly pricing.

**The `MonitoredResourceType` is not shipped as a standalone YAML in this
bundle.** The service catalog's service configuration resource (the `services.miloapis.com/Service` spec, in its current form, or a
dedicated sub-resource if one is introduced) is the source of truth, and the
catalog/controller materializes the `MonitoredResourceType` from it. Phase 1
ships only the `Service` and `MeterDefinition` resources; the
`MonitoredResourceType` shown below is illustrative of the eventual
controller-produced shape, not a file we author.

Illustrative shape (controller-produced, not authored YAML):

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: MonitoredResourceType
metadata:
  name: networking-datumapis-com-httproute
  labels:
    services.miloapis.com/owner-service: networking.datumapis.com
spec:
  resourceTypeName: networking.datumapis.com/HTTPRoute
  phase: Draft
  displayName: HTTP Route
  description: |
    A customer-defined HTTP routing configuration attached to a managed
    Gateway. One HTTPRoute represents a single HTTP service surface on
    the Datum Cloud edge. Usage events cover proxied request count,
    egress bytes, ingress bytes, and active connection seconds.
  gvk:
    group: gateway.networking.k8s.io
    kind: HTTPRoute
  labels:
    - name: region
      required: false
    - name: gateway
      required: false
    - name: gateway_class
      required: false
```

**Open decision OD-1** (now reframed, still blocking Phase 1): How does the
service catalog's service configuration resource express the monitored
resource? Specifically:

- Does the existing `services.miloapis.com/Service` spec already carry
  monitored-resource fields, or is a new sub-resource (e.g.
  `ServiceConfiguration`) being introduced?
- What field on that resource declares the `gvk`, `displayName`,
  `description`, and dimension labels?
- Will Phase 1 ship before that field/resource exists? If yes, we either (a)
  hold Phase 1, or (b) ship a temporary standalone `MonitoredResourceType`
  with a clear deletion plan.

Needs confirmation from Scot / services API owner before authoring Phase 1
YAML.

---

## Service Registration

The canonical `serviceName` is **`networking.datumapis.com`** and the
`producerProjectRef.name` is **`datum-cloud`** (resolves former OD-2 and OD-3).

Proposed `Service` resource:

```yaml
apiVersion: services.miloapis.com/v1alpha1
kind: Service
metadata:
  name: networking-datumapis-com
  labels:
    app.kubernetes.io/name: network-services-operator
    app.kubernetes.io/managed-by: kustomize
spec:
  serviceName: networking.datumapis.com
  phase: Draft
  displayName: Network Services
  description: |
    Managed HTTP/HTTPS edge proxy, routing, and traffic protection
    for Datum Cloud workloads. Provides programmable ingress via
    Gateway API HTTPRoute and WAF/rate-limit policies. Billed via
    the networking.datumapis.com/gateway meter family.
  owner:
    producerProjectRef:
      name: datum-cloud
  # Monitored-resource declaration goes here once the service
  # configuration shape is confirmed (see OD-1). The catalog will
  # materialize the MonitoredResourceType from this spec.
```

Meter names also move under the resolved service domain:

| Signal | meterName |
|--------|-----------|
| Request count | `networking.datumapis.com/gateway/requests` |
| Egress bytes | `networking.datumapis.com/gateway/egress-bytes` |
| Ingress bytes | `networking.datumapis.com/gateway/ingress-bytes` |
| Active connection seconds | `networking.datumapis.com/gateway/connection-seconds` |

---

## Kustomize Bundle Layout

Following `datum-cloud/cloud-portal`'s `config/billing/` and `config/services/`
pattern:

```
config/billing/
  kustomization.yaml
  meterdefinition-requests.yaml
  meterdefinition-egress-bytes.yaml
  meterdefinition-ingress-bytes.yaml
  meterdefinition-connection-seconds.yaml
  README.md

config/services/
  kustomization.yaml
  services_v1alpha1_service_networking.yaml
  README.md
```

Note: no `monitoredresourcetype-*.yaml` files. The `MonitoredResourceType` is
produced by the service catalog from the `Service` spec, not authored directly
here.

These bundles are **not** wired into the operator's Deployment overlay. They are
control-plane resources deployed independently via Flux into the billing and
services namespaces, as established by the cloud-portal pattern. Wiring them
into `datum-cloud/infra` Flux kustomizations is a separate step handled by the
platform/infra team after sign-off.

**Open decision OD-4** (blocking Phase 1): Confirm bundle layout. The issue
mentions packaging as a Kustomize component under `config/components/`. The
cloud-portal pattern uses top-level `config/billing/` and `config/services/`
directories instead. Both are valid. The top-level approach is recommended for
consistency with cloud-portal, but this needs confirmation.

---

## Usage Pipeline Integration — Investigation Findings

This section records the investigation into how to extract the four billing
signals from the Envoy Gateway proxy. The conclusion is that Phase 2 requires a
confirmed architectural choice before implementation begins.

### How Envoy Gateway exposes telemetry

Envoy Gateway exposes traffic telemetry through two primary surfaces:

1. **Prometheus scrape endpoint** — the Envoy data plane exposes a `/stats`
   endpoint with counters including `envoy_http_downstream_rq_total` (request
   count), `envoy_http_downstream_cx_rx_bytes_total` (ingress bytes),
   `envoy_http_downstream_cx_tx_bytes_total` (egress bytes), and
   `envoy_http_downstream_cx_active` (active connections, from which
   connection-seconds can be derived). These are gauge/counter deltas — they are
   not pre-attributed to a project or billing account.

2. **Access logs** — Envoy can be configured via the `EnvoyProxy` CR to emit
   structured JSON access logs (one line per completed request). Each line carries
   request bytes, response bytes, duration, upstream cluster, and extensible
   metadata. Access logs are the most natural source for per-request billing
   because each log line maps directly to one billable unit.

### Candidate approaches

#### Option A: Access log scraping via Vector Agent (recommended)

Configure Envoy Gateway via an `EnvoyProxy` CR patch to emit structured JSON
access logs to stdout. Deploy the Vector Agent DaemonSet (already planned for
the billing pipeline's tier-1 durability model) to tail those logs from the
node. Vector parses each line, constructs a `UsageEvent`, and forwards it to the
billing Ingestion Gateway as a CloudEvent.

**Pros:**
- Per-request granularity — maps naturally to the billing SDK's event model.
- Access logs already carry bytes in/out and duration per request.
- No changes to the network-services-operator Go binary.
- Vector is already the tier-1 durability component in the billing pipeline
  architecture.

**Cons:**
- High volume at scale. A gateway processing 10 k req/s produces 10 k log lines
  per second. Vector's disk buffering and batching handle this, but the Ingestion
  Gateway must be sized accordingly.
- Log format must be pinned — changes to the access log schema require a
  billing-side migration.
- Connection-seconds for WebSocket / long-lived connections is not directly
  available in per-request access logs (connections do not emit a per-request
  log line while held open). This signal requires a separate mechanism (see
  below).
- Requires an `EnvoyProxy` CR patch to configure the access log format.

#### Option B: Prometheus counter polling by a controller-side emitter

Run a periodic loop in the network-services-operator (or a sidecar) that reads
the Envoy `/stats` endpoint, computes counter deltas, and emits one `UsageEvent`
per billing interval (e.g., every 60 seconds) per gateway.

**Pros:**
- Dramatically lower volume — one event per gateway per metric per polling
  interval regardless of request rate.
- Connection-seconds is directly derivable from
  `envoy_http_downstream_cx_active` sampled at a known interval.
- No change to access log configuration.

**Cons:**
- Per-request granularity is lost — provenance drill-down in the portal would
  be at the gateway level, not the individual request level.
- The emitter must maintain stateful last-seen counter values across restarts
  without losing data or double-counting.
- Not a natural fit for the billing SDK's event-driven `Record()` interface.

#### Option C: gRPC metrics sink (OTel OTLP)

Configure Envoy's OTel metrics sink to push counters via gRPC OTLP to a
platform-deployed OTel Collector, which transforms them into CloudEvents and
forwards to the Ingestion Gateway.

**Cons (rejected):**
- The billing pipeline design document explicitly rejects the OTLP path: "OTLP
  does not structurally enforce the ULID dedup key or the billing entity
  (`subject`). These fields would become opaque string attributes that must
  survive the OTLP round-trip intact."
  (`billing/docs/enhancements/usage-pipeline.md`)
- Attribution of OTLP metric streams to billing accounts is not defined in the
  current pipeline design.

### Connection-seconds handling

For all options, the connection-seconds signal for WebSocket and other
long-lived connections is not captured naturally by per-request telemetry. The
recommended approach is to emit a connection-open and connection-close event from
the network-services-operator's gateway controller (which already watches
`Gateway` objects and manages their lifecycle). The duration between open and
close events, reported as a sum of connection-seconds, gives the meter its
signal. This is a small, localized Go change to the gateway controller.

### Recommendation

**Option A** (access log scraping via Vector) is the most consistent with the
billing pipeline architecture and is recommended as the primary collection
mechanism for request count, egress bytes, and ingress bytes. Connection-seconds
for persistent connections is handled by a lightweight controller-side emitter
(see above). This approach requires:

1. An `EnvoyProxy` CR patch configuring structured JSON access logs.
2. A Vector configuration to parse the log format and construct `UsageEvent`s.
3. A small addition to the gateway controller for connection-lifecycle events.

None of these changes require modifications to the billing pipeline contract.

### Pipeline context enrichment

The pipeline needs every piece of context required to attribute a
`UsageEvent` to the right billing entity (subject project, owning `HTTPRoute`,
gateway, gateway class, region) — and Envoy access logs alone do not carry
all of that. The data plane sees host headers, route names, and listener
metadata; the billing pipeline needs Kubernetes-side identifiers (namespaces,
project refs, owner references).

A sidecar that watches the relevant control-plane resources (`HTTPRoute`,
`Gateway`, `GatewayClass`, project metadata) and performs the translation from
data-plane identifiers to billing-entity identifiers is the right shape for
this. Two placements to evaluate:

- **Per-node, alongside Vector:** Vector calls out to the sidecar (or reads an
  enriched local cache) to attach project / route / gateway-class metadata to
  each event before sending it to the Ingestion Gateway.
- **Central, in front of the Ingestion Gateway:** Vector forwards the raw
  events; the enrichment service joins them against a cached view of the
  relevant control-plane resources and forwards enriched events to ingestion.

The per-node placement keeps the durability story simple (Vector's local disk
buffer continues to be the tier-1 guarantee) but pushes a copy of the
control-plane index to every node. The central placement is easier to scale
and operate but introduces a stateful enrichment hop. Final placement is an
OD for Phase 2 (see OD-8).

---

## Open Decisions

The following decisions are required before work can begin on each phase.

| ID | Question | Owner | Blocking |
|----|----------|-------|---------|
| OD-1 | How does the service catalog's service configuration resource express the monitored resource (existing `Service` spec fields, or a new sub-resource)? Does it exist yet? | Services API owner | Phase 1 |
| OD-2 | ~~Canonical `serviceName`~~ — resolved: `networking.datumapis.com`. | — | — |
| OD-3 | ~~`producerProjectRef.name`~~ — resolved: `datum-cloud`. | — | — |
| OD-4 | Bundle layout: `config/billing/` + `config/services/` (recommended) vs `config/components/`? | Kevin | Phase 1 |
| OD-5 | Is the Vector Agent DaemonSet planned to run on the edge cluster nodes that host Envoy Gateway pods? | Platform / infra | Phase 2 |
| OD-6 | Can the network-services-operator patch the `EnvoyProxy` CR to inject access log configuration? | Kevin | Phase 2 |
| OD-7 | Is the billing SDK published as a consumable Go module? | Billing team | Phase 2 |
| OD-8 | Enrichment-sidecar placement: per-node alongside Vector, or central in front of the Ingestion Gateway? | Billing team / platform | Phase 2 |

Phase 1 can begin once OD-1 and OD-4 are resolved. Phase 2 is additionally
blocked on OD-5 through OD-8.

---

## Acceptance Criteria

From issue [#155](https://github.com/datum-cloud/network-services-operator/issues/155):

- [ ] Network Services appears in the platform-wide service catalog in
  `Published` phase. (Phase 1 delivers `Draft`; promotion to `Published` is a
  separate sign-off step gated on the billing team.)
- [ ] Metering configuration covers request count, egress bytes, ingress bytes,
  and connection seconds, all shipped in `Draft` phase initially.
- [ ] Resources are packaged as a Kustomize bundle under `config/billing/` and
  `config/services/` (or an agreed alternative per OD-4).
- [ ] Investigation is complete with a clear confirmed approach for collecting
  and emitting usage events from the proxy to the billing pipeline.

---

## Implementation Plan

### Phase 1 — Catalog registration (no Go changes, ~1–2 days)

1. Resolve OD-1 and OD-4.
2. Author `config/billing/` YAML bundle:
   - `MeterDefinition` for each of the four signals.
   - `kustomization.yaml` and `README.md`.
   - No `MonitoredResourceType` YAML — produced by the service catalog from
     the `Service` resource.
3. Author `config/services/` YAML bundle:
   - `Service` resource carrying the monitored-resource configuration
     (shape per OD-1).
   - `kustomization.yaml` and `README.md`.
4. Open PR against `network-services-operator`. No Go code changes.
5. Platform/infra team separately wires the bundles into `datum-cloud/infra`
   Flux kustomizations (out of scope for this repo).

### Phase 2 — Emission integration (~1–2 weeks)

1. Resolve OD-5 through OD-8.
2. Add billing SDK to `go.mod`.
3. Patch the `EnvoyProxy` CR to enable structured JSON access logs.
4. Configure Vector to parse access log entries and emit `UsageEvent`s for
   request count, egress bytes, and ingress bytes.
5. Build (or wire in) the enrichment sidecar that translates data-plane
   identifiers to billing-entity identifiers, per OD-8.
6. Add connection-lifecycle emission to the gateway controller for the
   connection-seconds signal.
7. Write unit and integration tests covering the emission logic.

---

## References

- GitHub issue #155: https://github.com/datum-cloud/network-services-operator/issues/155
- Billing usage pipeline design: `datum-cloud/billing/docs/enhancements/usage-pipeline.md`
- `MeterDefinition` API type: `datum-cloud/billing/api/v1alpha1/meterdefinition_types.go`
- `MonitoredResourceType` API type: `datum-cloud/billing/api/v1alpha1/monitoredresourcetype_types.go`
- Cloud-portal billing bundle (reference pattern): `datum-cloud/cloud-portal/config/billing/`
- Cloud-portal service registration (reference pattern): `datum-cloud/cloud-portal/config/services/`
- Billing `MeterDefinition` sample: `datum-cloud/billing/config/samples/billing_v1alpha1_meterdefinition.yaml`
