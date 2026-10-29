---
name: httproute
description: 'Write and change HTTPRoute rules on a Datum Cloud Gateway with datumctl: match on path, method, header or query, split traffic by weight, redirect, rewrite, mirror, and set timeouts.'
---

# Skill: HTTPRoute

## Description

Configure HTTP routing rules in Datum Cloud — define how incoming requests are matched against paths, headers, and methods, then forwarded, split across backends, rewritten, redirected, or mirrored using `HTTPRoute` resources attached to a Gateway.

## Capabilities

- Route traffic by path (exact, prefix, regex), HTTP method, headers, and query parameters
- Split traffic across multiple backends by weight (canary, A/B, blue-green deployments)
- Redirect HTTP to HTTPS and issue permanent or temporary redirects
- Rewrite URL paths and hostnames before forwarding to backends
- Add, set, or remove request and response headers per route rule
- Mirror (shadow) a percentage of traffic to a secondary backend without affecting responses
- Configure per-route request and backend timeouts
- List, inspect, and update routes across a project
- Preview changes before applying
- Delete routes safely
- Check permissions before acting

## Key Commands

```bash
datumctl get httproutes --project <project-id>
datumctl describe httproute <name> --project <project-id>
datumctl apply -f httproute.yaml --project <project-id>
datumctl diff -f httproute.yaml --project <project-id>
datumctl delete httproute <name> --project <project-id>
datumctl auth can-i create httproutes --project <project-id>
```

**Gateway reference (required before creating routes):**
```bash
datumctl get gateways --project <project-id>
datumctl describe gateway <name> --project <project-id>
```

## API Reference

- **API group:** `gateway.networking.k8s.io`
- **Version:** `v1` (stable GA)
- **Kind:** `HTTPRoute` (plural: `httproutes`)
- **Namespaced:** yes (default namespace: `default`)
- **Scope:** project-level (`--project`)
- **Key spec fields:**
  - `spec.parentRefs[]` — the Gateway this route binds to (required):
    - `name` — Gateway resource name
    - `sectionName` — optional; bind to a specific listener (e.g. the `https` listener only)
  - `spec.hostnames[]` — list of virtual hostnames this route handles (e.g. `["api.example.com"]`); if omitted, the route matches all hostnames on the parent Gateway
  - `spec.rules[]` — ordered list of routing rules; evaluated top-to-bottom, first match wins:
    - `matches[]` — conditions that trigger this rule (all fields within one match entry are ANDed):
      - `path` — path match: `type` (`Exact`, `PathPrefix`, `RegularExpression`) and `value`
      - `method` — HTTP method (`GET`, `POST`, `DELETE`, etc.)
      - `headers[]` — header match: `name`, `value`, optional `type` (`Exact` or `RegularExpression`)
      - `queryParams[]` — query parameter match: `name`, `value`, optional `type`
    - `backendRefs[]` — upstream Service backends:
      - `name` — Service name
      - `port` — Service port number
      - `weight` — relative weight for traffic splitting (default `1`); weights across all backends in a rule are summed to determine percentages
    - `filters[]` — transformations applied to matching requests:
      - `type: RequestRedirect` — redirect the client; configure `redirect.scheme`, `redirect.hostname`, `redirect.path`, `redirect.statusCode` (`301` or `302`)
      - `type: URLRewrite` — rewrite path or hostname before forwarding; configure `urlRewrite.path` (`ReplacePrefixMatch` or `ReplaceFullPath`) and `urlRewrite.hostname`
      - `type: RequestHeaderModifier` — modify request headers before forwarding; configure `requestHeaderModifier.add[]`, `.set[]`, `.remove[]`
      - `type: ResponseHeaderModifier` — modify response headers before returning to client; configure `responseHeaderModifier.add[]`, `.set[]`, `.remove[]`
      - `type: RequestMirror` — shadow traffic to a second backend (fire-and-forget; does not affect the primary response); configure `requestMirror.backendRef`
    - `timeouts` — per-rule timeout overrides:
      - `request` — maximum duration for a complete request/response cycle (e.g. `"30s"`)
      - `backendRequest` — maximum time to wait for the upstream backend to respond (e.g. `"25s"`)
- **Status fields:**
  - `status.parents[]` — per-Gateway attachment status
  - `status.parents[].conditions` — includes `Accepted` and `ResolvedRefs` conditions

## Examples

List existing gateways before creating routes:

```bash
datumctl get gateways --project my-project
datumctl describe gateway my-gateway --project my-project
```

### Route traffic by path prefix

Send `/api/` requests to an API backend and all other traffic to a frontend:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: path-routing
  namespace: default
spec:
  parentRefs:
    - name: my-gateway
  hostnames:
    - app.example.com
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /api/
      backendRefs:
        - name: api-service
          port: 8080
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: frontend-service
          port: 3000
```

```bash
datumctl diff -f path-routing.yaml --project my-project
datumctl apply -f path-routing.yaml --project my-project
datumctl describe httproute path-routing --project my-project
# Look for status.parents[].conditions: Accepted=True, ResolvedRefs=True
```

### Canary deployment — split traffic by weight

Route 90% of traffic to the stable model and 10% to a new version. Adjust weights incrementally as confidence grows:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: canary-inference
  namespace: default
spec:
  parentRefs:
    - name: inference-gateway
  hostnames:
    - inference.example.com
  rules:
    - backendRefs:
        - name: model-stable
          port: 8080
          weight: 9
        - name: model-v2
          port: 8080
          weight: 1
```

```bash
datumctl diff -f canary-inference.yaml --project my-project
datumctl apply -f canary-inference.yaml --project my-project
```

### HTTP to HTTPS redirect

Attach a redirect route to the HTTP (port 80) listener and the main route to the HTTPS (port 443) listener using `sectionName`:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: http-redirect
  namespace: default
spec:
  parentRefs:
    - name: my-gateway
      sectionName: http
  hostnames:
    - app.example.com
  rules:
    - filters:
        - type: RequestRedirect
          requestRedirect:
            scheme: https
            statusCode: 301
```

```bash
datumctl diff -f http-redirect.yaml --project my-project
datumctl apply -f http-redirect.yaml --project my-project
```

### Header-based routing

Route requests to different model backends based on an `x-model` header:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: header-routing
  namespace: default
spec:
  parentRefs:
    - name: inference-gateway
  hostnames:
    - inference.example.com
  rules:
    - matches:
        - headers:
            - name: x-model
              value: gpt4
      backendRefs:
        - name: gpt4-service
          port: 8080
    - matches:
        - headers:
            - name: x-model
              value: claude
      backendRefs:
        - name: claude-service
          port: 8080
    - backendRefs:
        - name: default-model-service
          port: 8080
```

```bash
datumctl diff -f header-routing.yaml --project my-project
datumctl apply -f header-routing.yaml --project my-project
```

### Strip a path prefix before forwarding

Expose the API at `/v1/` externally but forward requests to the backend with the prefix removed:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: prefix-rewrite
  namespace: default
spec:
  parentRefs:
    - name: my-gateway
  hostnames:
    - api.example.com
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /v1/
      filters:
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: /
      backendRefs:
        - name: api-service
          port: 8080
```

```bash
datumctl diff -f prefix-rewrite.yaml --project my-project
datumctl apply -f prefix-rewrite.yaml --project my-project
```

### Mirror traffic to a shadow backend

Shadow a copy of all inference requests to a second service for logging or evaluation — the mirrored request is fire-and-forget and does not affect the client response:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: inference-with-mirror
  namespace: default
spec:
  parentRefs:
    - name: inference-gateway
  hostnames:
    - inference.example.com
  rules:
    - filters:
        - type: RequestMirror
          requestMirror:
            backendRef:
              name: shadow-logger
              port: 9090
      backendRefs:
        - name: inference-service
          port: 8080
```

```bash
datumctl diff -f inference-with-mirror.yaml --project my-project
datumctl apply -f inference-with-mirror.yaml --project my-project
```

### Inject a request header before forwarding

Add an `x-internal: true` header to all requests forwarded to a backend, useful for downstream services that need to identify internal traffic:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: header-injection
  namespace: default
spec:
  parentRefs:
    - name: my-gateway
  hostnames:
    - app.example.com
  rules:
    - filters:
        - type: RequestHeaderModifier
          requestHeaderModifier:
            add:
              - name: x-internal
                value: "true"
      backendRefs:
        - name: app-service
          port: 8080
```

```bash
datumctl diff -f header-injection.yaml --project my-project
datumctl apply -f header-injection.yaml --project my-project
```

## Constraints & Guardrails

- Always use `datumctl` — never `kubectl`
- `--project` is required for all operations
- `spec.parentRefs[].name` must reference an existing `Gateway` in the same namespace and project — the route will not become active until the Gateway accepts it (`status.parents[].conditions: Accepted=True`)
- Route rules are evaluated in order — place more-specific matches (exact path, method + header combinations) before catch-all prefix matches; a catch-all prefix early in the list will shadow later rules
- `weight` values in `backendRefs[]` are relative, not percentages — weights of `9` and `1` produce a 90%/10% split; weights of `90` and `10` produce the same split
- `RequestMirror` sends a fire-and-forget copy to the mirror backend — errors from the mirror do not affect the primary response or the client, but mirror backends do consume capacity
- A `RequestRedirect` filter returns a redirect response directly to the client — combine it with an empty `backendRefs` list (no backend needed for redirect-only rules)
- Use `sectionName` in `spec.parentRefs[]` to bind a route to a specific listener when a Gateway has both HTTP and HTTPS listeners; binding only to `http` is the standard pattern for redirect routes
- `gateway.networking.k8s.io/v1` is stable GA — unlike the `gateway.envoyproxy.io` policy CRDs it is not subject to breaking alpha changes
- Policy attachments from the alb skill (`SecurityPolicy`, `BackendTrafficPolicy`, `TrafficProtectionPolicy`) reference `HTTPRoute` resources by name via `targetRefs` — create the HTTPRoute first before attaching policies
- Run `datumctl diff -f` before `apply` for any changes
- `--dry-run=server` validates the manifest against the API before committing
- `delete` has no confirmation prompt — always verify the resource name first

## See Also

- [Datum Cloud Application Load Balancer documentation](https://www.datum.net/docs/alb/overview.md)
- [Kubernetes Gateway API HTTPRoute reference](https://gateway-api.sigs.k8s.io/reference/spec/#gateway.networking.k8s.io/v1.HTTPRoute)
- [Gateway API traffic management tasks](https://gateway-api.sigs.k8s.io/guides/traffic-splitting/)
- [alb skill](../alb/SKILL.md) — WAF, JWT auth, rate limiting, and circuit breaker policies that attach to HTTPRoute via `targetRefs`
- [client-traffic skill](../client-traffic/SKILL.md) — TLS termination, HTTP/3, and connection settings that attach to the parent Gateway
