# HTTPRoute Attachment Contract

`HTTPRoute` is an upstream [Gateway API](https://gateway-api.sigs.k8s.io/) type,
not an NSO CRD. When you attach one to an NSO-managed `Gateway`, a validating
admission webhook enforces a narrower contract than upstream Gateway API allows.
This page documents that contract.

The webhook rejects any create or update that violates a rule below. Enforcement
lives in
[`internal/validation/httproute_validation.go`](../../internal/validation/httproute_validation.go),
wired through
[`internal/webhook/v1/httproute_webhook.go`](../../internal/webhook/v1/httproute_webhook.go).
Treat the code as the source of truth; this page tracks it.

## parentRefs

Each `parentRef` must target an NSO-managed `Gateway` in the route's own
namespace:

- `group` — unset or `gateway.networking.k8s.io`.
- `kind` — unset or `Gateway`.
- `namespace` — unset or equal to the route's namespace. Cross-namespace
  attachment is rejected.

## hostnames

`spec.hostnames` is **forbidden**. Setting it fails with:

```
spec.hostnames: Forbidden: hostnames are not permitted
```

The route inherits its hostname from the `Gateway` listener it attaches to; do
not set it on the route.

This is a **temporary** restriction — the validator notes hostnames will be
allowed in the future. Until then, this rejection is expected behavior, not a
bug. See #288.

## Rule filters

`spec.rules[].filters[].type` must be one of:

- `RequestHeaderModifier`
- `ResponseHeaderModifier`
- `RequestRedirect`
- `URLRewrite`
- `CORS`
- `ExtensionRef`

`backendRefs[].filters[]` are narrower — only:

- `RequestHeaderModifier`
- `ResponseHeaderModifier`
- `ExtensionRef`

An `ExtensionRef` filter must point at an
[Envoy Gateway](https://gateway.envoyproxy.io/) `HTTPRouteFilter`:

- `group` — `gateway.envoyproxy.io`.
- `kind` — `HTTPRouteFilter`.

## backendRefs

Each backend reference must be one of these group/kind pairs, in the route's own
namespace:

| Group | Kind | Notes |
| --- | --- | --- |
| `discovery.k8s.io` | `EndpointSlice` | `port` is required. |
| `gateway.envoyproxy.io` | `Backend` | Envoy Gateway backend. |
| `""` (core) | `Service` | Only when the operator runs with `allowServiceBackends` enabled. |

`Service` backends are disabled by default: NSO typically runs against a Datum
control plane where the `Service` type is not registered. The
`allowServiceBackends` option exists mainly for conformance-style testing. See
[`HTTPRouteValidationOptions`](../../internal/config/config.go).

Additional constraints:

- `group` and `kind` are required (no defaulting to core `Service`).
- `namespace` — unset or equal to the route's namespace. Cross-namespace
  backends are rejected.

## Rejected examples

- `spec.hostnames` set to any value.
- A `parentRef` with `kind: HTTPRoute`, or in a different namespace.
- A rule filter of type `RequestMirror`.
- A `backendRef` filter of type `RequestRedirect` (allowed at rule level, not on
  a backend).
- An `ExtensionRef` filter targeting a group other than
  `gateway.envoyproxy.io`.
- A `backendRef` to a core `Service` when `allowServiceBackends` is off.
- A `backendRef` to an `EndpointSlice` without a `port`.
- Any backend in a namespace other than the route's.
