---
name: client-traffic
description: 'Tune how Datum Cloud edge gateways accept client connections with a ClientTrafficPolicy: TLS versions and ciphers, mutual TLS, HTTP/2 and HTTP/3, timeouts, connection limits, and real client IP detection.'
---

# Skill: Client Traffic Policy

## Description

Manage how Datum Cloud edge gateways accept and handle incoming client connections — configure TLS termination, HTTP protocol versions, connection limits, timeouts, and client IP detection by attaching a `ClientTrafficPolicy` to a Gateway listener.

## Capabilities

- Configure TLS termination: minimum/maximum TLS version, cipher suites, ALPN protocols
- Require and validate client certificates (mutual TLS / mTLS)
- Enable HTTP/2 and HTTP/3 (QUIC) on gateway listeners
- Set client-facing connection timeouts and idle timeouts
- Enforce maximum concurrent connections and per-connection request limits
- Detect real client IP from X-Forwarded-For headers or proxy protocol
- Scope policies to a specific listener via `sectionName`
- List, inspect, and update policies across a project
- Preview changes before applying
- Delete policies safely
- Check permissions before acting

## Key Commands

```bash
datumctl get clienttrafficpolicies --project <project-id>
datumctl get ctp --project <project-id>
datumctl describe clienttrafficpolicy <name> --project <project-id>
datumctl apply -f ctp.yaml --project <project-id>
datumctl diff -f ctp.yaml --project <project-id>
datumctl delete clienttrafficpolicy <name> --project <project-id>
datumctl auth can-i create clienttrafficpolicies --project <project-id>
```

**Gateway reference (required before attaching a policy):**
```bash
datumctl get gateways --project <project-id>
datumctl describe gateway <name> --project <project-id>
```

## API Reference

- **API group:** `gateway.envoyproxy.io` ⚠️ **alpha — field names may change**
- **Version:** `v1alpha1`
- **Kind:** `ClientTrafficPolicy` (plural: `clienttrafficpolicies`, short: `ctp`)
- **Namespaced:** yes (default namespace: `default`)
- **Scope:** project-level (`--project`)
- **Key spec fields:**
  - `spec.targetRefs[]` — policy attachment targets (required):
    - `group` — `gateway.networking.k8s.io`
    - `kind` — `Gateway`
    - `name` — target Gateway name
    - `sectionName` — optional; name of a specific listener within the Gateway (use when a Gateway has multiple listeners on different ports/protocols)
  - `spec.tls` — downstream TLS configuration:
    - `minVersion` — minimum TLS version: `"1.0"`, `"1.1"`, `"1.2"`, `"1.3"`
    - `maxVersion` — maximum TLS version: same values as `minVersion`
    - `ciphers[]` — list of TLS cipher suite names; defaults to a secure built-in set
    - `ecdhCurves[]` — list of ECDH curve names
    - `alpnProtocols[]` — ALPN negotiation list (e.g. `["h2", "http/1.1"]`)
    - `clientValidation` — mTLS client certificate validation:
      - `caCertificateRefs[]` — Secret or ConfigMap references containing the CA bundle
      - `optional` — `true` to request but not require a client certificate
  - `spec.http1` — HTTP/1.1-specific settings (e.g. `enableTrailers`, `preserveHeaderCase`)
  - `spec.http2` — HTTP/2-specific settings:
    - `initialStreamWindowSize` — initial HTTP/2 stream flow-control window
    - `initialConnectionWindowSize` — initial connection-level flow-control window
    - `maxConcurrentStreams` — maximum concurrent streams per connection
  - `spec.http3` — enable HTTP/3 (QUIC); set to `{}` to enable with defaults
  - `spec.timeout` — client connection timeouts:
    - `http.requestReceivedTimeout` — maximum time to receive a complete request (e.g. `"10s"`)
    - `http.idleTimeout` — maximum idle time before closing a connection (e.g. `"90s"`)
  - `spec.connection` — connection-level limits:
    - `connectionLimit.value` — maximum simultaneous connections accepted by the listener
    - `bufferLimit` — per-connection read buffer size (e.g. `"32Ki"`)
  - `spec.clientIPDetection` — real client IP extraction:
    - `xForwardedFor.numTrustedHops` — number of trusted proxy hops in the XFF chain
    - `customHeader.name` — alternative header name for client IP (e.g. `"X-Real-Ip"`)
  - `spec.enableProxyProtocol` — `true` to parse HAProxy PROXY protocol from upstream load balancers
- **Status fields:**
  - `status.conditions` — includes `Accepted` and `Programmed` readiness conditions

## Examples

List existing gateways before attaching a policy:

```bash
datumctl get gateways --project my-project
datumctl describe gateway my-gateway --project my-project
```

### Enforce TLS 1.2+ with strong cipher suites

Restrict clients to TLS 1.2 or higher and limit the accepted cipher suites:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: ClientTrafficPolicy
metadata:
  name: tls-hardening
  namespace: default
spec:
  tls:
    minVersion: "1.2"
    maxVersion: "1.3"
    ciphers:
      - ECDHE-ECDSA-AES128-GCM-SHA256
      - ECDHE-RSA-AES128-GCM-SHA256
      - ECDHE-ECDSA-AES256-GCM-SHA384
      - ECDHE-RSA-AES256-GCM-SHA384
    alpnProtocols:
      - h2
      - http/1.1
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: my-gateway
```

```bash
datumctl diff -f tls-hardening.yaml --project my-project
datumctl apply -f tls-hardening.yaml --project my-project
datumctl describe clienttrafficpolicy tls-hardening --project my-project
# Look for status.conditions: Programmed=True
```

### Require mutual TLS (mTLS) from clients

Use `clientValidation` to require clients to present a certificate signed by your CA. The CA must be stored in a Kubernetes Secret in the same namespace:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: ClientTrafficPolicy
metadata:
  name: mtls-required
  namespace: default
spec:
  tls:
    minVersion: "1.2"
    clientValidation:
      caCertificateRefs:
        - kind: Secret
          name: internal-ca-bundle
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: internal-gateway
```

```bash
datumctl diff -f mtls-required.yaml --project my-project
datumctl apply -f mtls-required.yaml --project my-project
```

### Enable HTTP/3 (QUIC) on a listener

Enable HTTP/3 alongside HTTP/2 fallback. Use `sectionName` to target only the HTTPS listener when the Gateway has both HTTP and HTTPS listeners:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: ClientTrafficPolicy
metadata:
  name: http3-enabled
  namespace: default
spec:
  http3: {}
  http2:
    maxConcurrentStreams: 100
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: my-gateway
      sectionName: https
```

```bash
datumctl diff -f http3-enabled.yaml --project my-project
datumctl apply -f http3-enabled.yaml --project my-project
```

### Configure client IP detection behind a load balancer

When Datum Cloud sits behind an upstream load balancer that adds an `X-Forwarded-For` header, set `numTrustedHops` to the number of trusted proxy hops so the real client IP is extracted correctly:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: ClientTrafficPolicy
metadata:
  name: client-ip-detection
  namespace: default
spec:
  clientIPDetection:
    xForwardedFor:
      numTrustedHops: 1
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: my-gateway
```

```bash
datumctl diff -f client-ip-detection.yaml --project my-project
datumctl apply -f client-ip-detection.yaml --project my-project
```

### Enforce connection limits for high-traffic AI inference endpoints

Protect inference backends from connection overload by capping simultaneous connections and setting aggressive idle timeouts to recycle connections quickly:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: ClientTrafficPolicy
metadata:
  name: inference-connection-limits
  namespace: default
spec:
  connection:
    connectionLimit:
      value: 2000
    bufferLimit: "32Ki"
  timeout:
    http:
      requestReceivedTimeout: "30s"
      idleTimeout: "60s"
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: inference-gateway
```

```bash
datumctl diff -f inference-connection-limits.yaml --project my-project
datumctl apply -f inference-connection-limits.yaml --project my-project
```

## Constraints & Guardrails

- Always use `datumctl` — never `kubectl`
- `--project` is required for all operations
- `gateway.envoyproxy.io/v1alpha1` is unstable; field names may change between releases
- `ClientTrafficPolicy` attaches to `Gateway` only — it cannot attach to `HTTPRoute`; use `SecurityPolicy` or `BackendTrafficPolicy` from the alb skill for route-level policies
- Use `sectionName` in `targetRefs` when a Gateway has multiple listeners on different ports or protocols — omitting it applies the policy to all listeners on that Gateway
- TLS certificates (server certs and CA bundles for mTLS) must be stored as Kubernetes Secrets and referenced by the Gateway listener or by `spec.tls.clientValidation.caCertificateRefs` — `ClientTrafficPolicy` configures *how* TLS works, not *which* server certificate to serve
- Increasing TLS strictness (`minVersion: "1.3"`, mTLS) can break existing clients — run `datumctl diff` and test against a non-production gateway first
- `numTrustedHops` must match the actual number of trusted upstream proxies — setting it too high allows clients to spoof their IP via XFF; setting it too low causes rate-limiting and logging to target the wrong IP
- Run `datumctl diff -f` before `apply` for any changes
- `--dry-run=server` validates the manifest against the API before committing
- `delete` has no confirmation prompt — always verify the resource name first

## See Also

- [Datum Cloud Application Load Balancer documentation](https://www.datum.net/docs/alb/overview.md)
- [Envoy Gateway ClientTrafficPolicy API reference](https://gateway.envoyproxy.io/docs/api/extension_types/#clienttrafficpolicy)
- [Envoy Gateway TLS termination task](https://gateway.envoyproxy.io/docs/tasks/security/tls-termination/)
- [Envoy Gateway mTLS task](https://gateway.envoyproxy.io/docs/tasks/security/mutual-tls/)
