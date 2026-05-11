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
- [MonitoredResourceType](#monitoredresourcetype)
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

- **Phase 1 (catalog):** Declare the `Service`, `MonitoredResourceType`, and
  `MeterDefinition` resources. No Go code changes — pure YAML, packaged in
  `config/billing/` and `config/services/`. Deployable as soon as the billing
  CRDs are present in the control plane.
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
| Request count | `networking.miloapis.com/gateway/requests` | `{request}` | Sum |
| Egress bytes | `networking.miloapis.com/gateway/egress-bytes` | `By` | Sum |
| Ingress bytes | `networking.miloapis.com/gateway/ingress-bytes` | `By` | Sum |
| Active connection seconds | `networking.miloapis.com/gateway/connection-seconds` | `s` | Sum |

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

Each meter carries two optional dimensions for cost attribution and future
pricing tiers:

- `region` — Datum deployment region serving the request.
- `gateway` — The `Gateway` resource name, enabling per-gateway cost drill-down.

Dimensions are declared on the `MonitoredResourceType` as optional (not
required) so events from proxies that cannot populate them are not rejected at
the Ingestion Gateway.

---

## MonitoredResourceType

**Open decision OD-1** (blocking Phase 1): Should the billable kind be
`HTTPProxy` (`networking.datumapis.com/HTTPProxy`) or the underlying Gateway API
`Gateway` (`gateway.networking.k8s.io/Gateway`)?

The `HTTPProxy` is the customer-facing resource — the object customers create
and reason about in the portal. The `Gateway` is an internal implementation
detail managed by the operator. Billing at the `Gateway` level could confuse
customers who only interact with `HTTPProxy`.

**Recommendation:** Use `HTTPProxy` as the primary billable resource. A second
`MonitoredResourceType` for bare `Gateway` (for users bypassing the HTTPProxy
abstraction) can be added later as a purely additive change.

Proposed `MonitoredResourceType` assuming `HTTPProxy`:

```yaml
apiVersion: billing.miloapis.com/v1alpha1
kind: MonitoredResourceType
metadata:
  name: networking-datumapis-com-httpproxy
  labels:
    services.miloapis.com/owner-service: networking.miloapis.com
spec:
  resourceTypeName: networking.datumapis.com/HTTPProxy
  phase: Draft
  displayName: HTTP Proxy
  description: |
    A managed HTTP/HTTPS reverse-proxy entry point. One HTTPProxy
    represents a single customer-configured ingress point on the
    Datum Cloud edge. Usage events cover proxied request count,
    egress bytes, ingress bytes, and active connection seconds.
  gvk:
    group: networking.datumapis.com
    kind: HTTPProxy
  labels:
    - name: region
      required: false
      description: Datum deployment region serving the requests.
    - name: gateway
      required: false
      description: Name of the underlying Gateway resource.
```

---

## Service Registration

Proposed `Service` resource:

```yaml
apiVersion: services.miloapis.com/v1alpha1
kind: Service
metadata:
  name: networking-miloapis-com
  labels:
    app.kubernetes.io/name: network-services-operator
    app.kubernetes.io/managed-by: kustomize
spec:
  serviceName: networking.miloapis.com
  phase: Draft
  displayName: Network Services
  description: |
    Managed HTTP/HTTPS edge proxy, routing, and traffic protection
    for Datum Cloud workloads. Provides programmable ingress via
    HTTPProxy, Gateway API, and WAF/rate-limit policies. Billed via
    the networking.miloapis.com/gateway meter family.
  owner:
    producerProjectRef:
      name: network-services-operator
```

**Open decision OD-2** (blocking Phase 1): Confirm the canonical `serviceName`.
`networking.miloapis.com` is proposed here by analogy with
`assistant.miloapis.com`. The alternative is `network-services.datumapis.com` if
the convention uses the Datum product domain rather than the milo API-group
domain. Needs confirmation from the services API owner.

**Open decision OD-3** (blocking Phase 1): Confirm the
`producerProjectRef.name`. Cloud-portal uses `cloud-portal` (the GitHub repo
name). The natural value here is `network-services-operator`. Needs
confirmation.

---

## Kustomize Bundle Layout

Following `datum-cloud/cloud-portal`'s `config/billing/` and `config/services/`
pattern:

```
config/billing/
  kustomization.yaml
  monitoredresourcetype-httpproxy.yaml
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

---

## Open Decisions

The following decisions are required before work can begin on each phase.

| ID | Question | Owner | Blocking |
|----|----------|-------|---------|
| OD-1 | Billable Kind: `HTTPProxy` (recommended) or `Gateway`? | Kevin | Phase 1 |
| OD-2 | Canonical `serviceName` for Network Services? | Services API owner | Phase 1 |
| OD-3 | `producerProjectRef.name` value? | Kevin | Phase 1 |
| OD-4 | Bundle layout: `config/billing/` + `config/services/` (recommended) vs `config/components/`? | Kevin | Phase 1 |
| OD-5 | Is the Vector Agent DaemonSet planned to run on the edge cluster nodes that host Envoy Gateway pods? | Platform / infra | Phase 2 |
| OD-6 | Can the network-services-operator patch the `EnvoyProxy` CR to inject access log configuration? | Kevin | Phase 2 |
| OD-7 | Is the billing SDK published as a consumable Go module? | Billing team | Phase 2 |

Phase 1 can begin once OD-1 through OD-4 are resolved. Phase 2 is additionally
blocked on OD-5 through OD-7.

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

1. Resolve OD-1 through OD-4.
2. Author `config/billing/` YAML bundle:
   - `MonitoredResourceType` for the chosen billable Kind.
   - `MeterDefinition` for each of the four signals.
   - `kustomization.yaml` and `README.md`.
3. Author `config/services/` YAML bundle:
   - `Service` resource.
   - `kustomization.yaml` and `README.md`.
4. Open PR against `network-services-operator`. No Go code changes.
5. Platform/infra team separately wires the bundles into `datum-cloud/infra`
   Flux kustomizations (out of scope for this repo).

### Phase 2 — Emission integration (~1–2 weeks)

1. Resolve OD-5 through OD-7.
2. Add billing SDK to `go.mod`.
3. Patch the `EnvoyProxy` CR to enable structured JSON access logs.
4. Configure Vector to parse access log entries and emit `UsageEvent`s for
   request count, egress bytes, and ingress bytes.
5. Add connection-lifecycle emission to the gateway controller for the
   connection-seconds signal.
6. Write unit and integration tests covering the emission logic.

---

## References

- GitHub issue #155: https://github.com/datum-cloud/network-services-operator/issues/155
- Billing usage pipeline design: `datum-cloud/billing/docs/enhancements/usage-pipeline.md`
- `MeterDefinition` API type: `datum-cloud/billing/api/v1alpha1/meterdefinition_types.go`
- `MonitoredResourceType` API type: `datum-cloud/billing/api/v1alpha1/monitoredresourcetype_types.go`
- Cloud-portal billing bundle (reference pattern): `datum-cloud/cloud-portal/config/billing/`
- Cloud-portal service registration (reference pattern): `datum-cloud/cloud-portal/config/services/`
- Billing `MeterDefinition` sample: `datum-cloud/billing/config/samples/billing_v1alpha1_meterdefinition.yaml`
