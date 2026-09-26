# Weighted load balancing across origins

| | |
|---|---|
| **Status** | Proposed |
| **Author** | Engineering |
| **Created** | 2026-09-21 |

## Summary

An Application Load Balancer can be given several backends with weights, but
only if every backend lives on the same hostname. Splitting traffic between two
different origins — the reason the weight field exists — cannot be programmed,
and attempting it leaves the load balancer stuck serving its previous
configuration.

## Motivation

Sending a share of live traffic to a second origin is the ordinary way teams de-risk
a change:

- **Canary a release.** Send 5% of requests to the new origin, watch error rates
  and latency, then widen or roll back.
- **Blue/green a cutover.** Shift from 100/0 to 0/100 between two hosted
  environments, with the old one still warm if the new one misbehaves.
- **Migrate provider.** Move from one platform to another over days rather than
  in a single DNS change, with a rollback that takes effect in seconds.
- **Compare under real traffic.** Run two origins side by side to measure cost
  or latency differences that synthetic testing does not show.

Each of these means two origins with different hostnames, which is what this
API refuses. It is also what motivated the work. From
[datum-cloud/enhancements#744](https://github.com/datum-cloud/enhancements/issues/744):

> **Scope update:** LB chaos testing (bm-lb-test-server-a/b on Fly.io) surfaced a
> second, independent need — weighted load-balancing across more than one
> backend

Two Fly.io apps are two hostnames: the scenario that prompted raising the
backend cap is the one the cap now permits but the controller cannot program.

### What a user sees today

The API accepts the configuration, the controller refuses it, and the load
balancer goes on serving what it already served while reporting `Pending`. A
change is accepted, not applied, and nothing obvious says so, which is worse
than the feature being absent: that at least fails at the point of writing.
Shown in full under [What it looks like to use](#what-happens-today), and
reproduced on staging, where a proxy split across two Vercel apps sat in
`Pending` for hours, still sending every request to the first origin.

### Why the obvious workaround does not help

A Host header override makes every backend agree, which satisfies the
controller. It also sends the same Host to every origin.

Platforms that host by hostname — Vercel, Fly.io, Netlify, Cloudflare Pages,
and most SaaS origins — route on that header, so the second origin receives
requests addressed to the first and answers 404. It programs successfully and
serves wrong results.

## What it looks like to use

### Canary a new origin

Today's load balancer, serving one origin:

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: HTTPProxy
metadata:
  name: storefront
spec:
  hostnames:
    - shop.example.com
  rules:
    - backends:
        - endpoint: https://storefront-blue.fly.dev
```

Send 5% to a new origin by adding a backend and weighting the pair:

```yaml
  rules:
    - backends:
        - endpoint: https://storefront-blue.fly.dev
          weight: 95
        - endpoint: https://storefront-green.fly.dev
          weight: 5
```

Widen by editing two numbers — `50`/`50`, then `0`/`100` — and roll back the
same way. A weight of `0` drains an origin without removing it, so the rollback
path stays configured while carrying no traffic.

Each origin receives requests addressed to itself: blue gets
`Host: storefront-blue.fly.dev`, green its own. That is what makes a split work
on a platform that routes by hostname, and it is derived from each endpoint
rather than written by the user.

### What happens today

The API accepts that spec — `weight` and `MaxItems: 16` shipped in #453. The
controller then cannot program it:

```console
$ kubectl get httpproxy storefront -o jsonpath='{.status.conditions[?(@.type=="Programmed")]}'
{
  "type": "Programmed",
  "status": "False",
  "reason": "Pending",
  "message": "The HTTPProxy cannot be programmed: failed to collect desired
    resources: backend 1 in rule 0 needs Host header rewritten to
    \"storefront-green.fly.dev\", which conflicts with another backend in the
    same rule that needs \"storefront-blue.fly.dev\"; backends sharing a rule
    must resolve to the same Host rewrite target. Set a Host header override on
    the rule so every backend agrees, or give each backend its own rule"
}
```

Blue keeps serving 100%. Nothing stops working; it stops changing, which is
harder to notice. That message is #469 — before it, the condition read only
`The HTTPProxy has not been programmed`.

### After this change

The same spec programs, and the status says so:

```console
$ kubectl get httpproxy storefront
NAME         HOSTNAME           PROGRAMMED   CERTIFICATES   AGE
storefront   shop.example.com   True         True           4m
```

Nothing in the user-facing API changes. `weight` already exists and already
means this; what changes is the controller carrying it through to the data
plane when origins differ.

### Splitting across a Datum compute workload and an external origin

Mixed pools work the same way, and this one already programs today, since a
`networkService` backend takes no Host rewrite:

```yaml
  rules:
    - backends:
        - networkService:
            name: storefront
            port: http
          weight: 90
        - endpoint: https://storefront-legacy.fly.dev
          weight: 10
```

### Through `datumctl`

The same journey on the CLI, following the surface proposed in
[#449](https://github.com/datum-cloud/network-services-operator/pull/449):

```console
$ datumctl alb route backend add storefront --path / \
    --endpoint https://storefront-green.fly.dev --weight 5
$ datumctl alb route backend list storefront --path /
ORIGIN                                  WEIGHT   SHARE
https://storefront-blue.fly.dev         95       95%
https://storefront-green.fly.dev        5        5%

$ datumctl alb route update storefront --path / \
    --endpoint https://storefront-blue.fly.dev --weight 50 \
    --endpoint https://storefront-green.fly.dev --weight 50
```

This settles an open question there:

> Equal split across a pool unless the API grows a weight field; `--weight` is
> then a flag on each backend group. **Open** until that field exists.

The field exists. `--weight` belongs on each backend group, defaulting to `1`
as the API does, so omitting it everywhere still gives an equal split.

## Goals

- One load balancer can send weighted shares of traffic to origins on different
  hostnames
- Each origin receives requests addressed to itself
- Weights can be changed at runtime without recreating the load balancer, so a
  canary can be widened or rolled back quickly
- Existing load balancers are unaffected

## Non-goals

- **Priority-based failover** (use B only when A is unhealthy). Tracked
  separately in datum-cloud/enhancements#744 phase 1 and
  datum-cloud/enhancements#573; it shares machinery with this but answers a
  different question
- **Active health checks.** Passive outlier detection landed separately
- **Session affinity beyond the existing consistent-hash algorithm**
- **Connector backends.** A connector must still be the only backend in its
  rule

## Who this affects

| Backend kind | Weighted LB today | After this change |
|---|---|---|
| `endpoint`, origins sharing a hostname | Works | Works |
| `endpoint`, origins on different hostnames | **Refused** | Works |
| `networkService` (compute workloads) | Works | Works |
| `instance` | Works | Works |
| `connector` | Single backend only | Unchanged |

Only external origins on distinct hostnames are blocked. A `networkService`
backend takes no Host rewrite, so customers on Datum compute are unaffected.

## Why it does not work today

Each origin needs the upstream `Host` header rewritten to its own hostname, or
the origin cannot tell which site is being asked for. The controller expresses
that as a `URLRewrite` filter on the `HTTPRouteRule`, which applies to every
backend in the rule alike. Two backends wanting different values cannot both be
satisfied, so the controller refuses the pair rather than silently applying one
origin's hostname to both.

Nothing below the controller shares that limitation. Envoy gives each weighted
cluster its own `host_rewrite_literal`, and Envoy Gateway has accepted a
hostname `URLRewrite` on an individual backend reference since v1.7.0,
translating it onto that weighted cluster. The edge runs v1.7.4.

What stops the controller from using it is this repository's own HTTPRoute
admission webhook, which permits only `RequestHeaderModifier`,
`ResponseHeaderModifier` and `ExtensionRef` on a backend reference. It
validates every HTTPRoute in the project control plane, including the ones the
HTTPProxy controller generates.

## Proposal

Move the Host rewrite onto each backend reference when a rule's backends need
different ones, and let the webhook admit it.

- When every backend that needs a rewrite agrees, the rule keeps its single
  rule-scoped rewrite exactly as today, so existing load balancers are
  untouched.
- When they differ, each such backend reference carries its own
  `URLRewrite{hostname}`, and any rule-level hostname rewrite is dropped so the
  two never coexist. A rule-level path rewrite is kept.
- The HTTPRoute webhook admits `URLRewrite` on a backend reference, hostname
  only, matching what Envoy Gateway supports there. The filters a user may set
  on an HTTPProxy backend are unchanged, so the generated rewrite cannot be
  contradicted by a user-supplied one.

No upstream change is needed. Translating the storefront split with
`egctl x translate` against Envoy Gateway v1.7.4 produces the intended shape:

```yaml
weightedClusters:
  clusters:
  - hostRewriteLiteral: storefront-blue.fly.dev
    name: httproute/default/storefront/rule/0/backend/0
    weight: 95
  - hostRewriteLiteral: storefront-green.fly.dev
    name: httproute/default/storefront/rule/0/backend/1
    weight: 5
```

An earlier revision of this document proposed adding a literal hostname
modifier to Envoy Gateway's `HTTPRouteFilter`, on the premise that Gateway API's
webhook refused `URLRewrite` on a backend reference. Gateway API v1.5 ships no
such webhook; the refusal was ours.

### Why not `type: Backend`

Envoy Gateway's `HTTPRouteFilter` can already rewrite Host to the selected
upstream's DNS name, via Envoy's `auto_host_rewrite`. Applied at the rule, it
would give each FQDN origin its own Host without any per-backend filter. It
cannot express a user's Host override on one backend, or the `tls.hostname` an
HTTPS origin addressed by IP needs, so it covers only part of the table above.
The per-backend rewrite covers all of it.

### Why not patch the generated configuration

The obvious shortcut is an `EnvoyPatchPolicy` setting `host_rewrite_literal`
per weighted cluster, reusing the escape hatch this repository already relies
on for connector routing. It does not work.

A weighted route with no backend-level filters produces no weighted clusters.
Envoy Gateway emits one merged cluster and carries the weights as locality
weights inside it, and `host_rewrite_literal` has no per-locality form — so in
the shape we generate there is nowhere to attach a per-backend rewrite, and a
patch aimed at one matches nothing.

Evidence and the fixture cross-reference behind this are in
[network-services-operator#473](https://github.com/datum-cloud/network-services-operator/issues/473).

## Alternatives considered

**Add a literal hostname modifier upstream.** The earlier proposal. It would
work, but it waits on an Envoy Gateway release for something the version we
run already supports.

**Document the limitation and reject the configuration clearly.** The cheapest
option — network-services-operator#469 makes the refusal visible on the
resource instead of only in controller logs. It does not give anyone the
feature.

**Require one rule per origin.** Backends in separate rules are matched, not
weighted, so this cannot express "5% of the same traffic". It is a different
feature.

## Consequences

### Rules whose origins differ get a cluster per backend

Any backend-level filter makes Envoy Gateway emit a weighted cluster per
backend instead of one merged cluster. That is what carries the per-backend
rewrite, and it only happens for rules that could not be programmed before.

It changes one behaviour on the version the edge runs. Envoy picks among
weighted clusters per request, and Envoy Gateway v1.7.4 does not hash that
choice, so a `ConsistentHash` load balancer keeps a client on one endpoint
within an origin but not on the same origin. A canary sees a client's requests
split by weight rather than pinned to one side.

### Two paths

Rules whose backends agree keep the rule-scoped rewrite; rules whose backends
differ use per-backend rewrites. The cost is two shapes of generated route to
reason about. The benefit is that no existing load balancer's configuration
changes.

## Open questions

### Session affinity across origins

Whether a canary needs a client pinned to one origin before this ships. Envoy
Gateway v1.8.4 and v1.9.0 set `use_hash_policy` on weighted clusters whenever
the route has a hash policy, which pins it. The edge was rolled back to v1.7.4
over an OIDC regression in v1.8, so affinity across origins arrives with the
next Envoy Gateway upgrade rather than with this change.

## Known issues

A rule-level Host override takes precedence over a backend-level one, so the
less specific wins. With per-backend rewrites in place, "this Host for the
pool, except the canary" is now expressible on the data plane; what stops it is
that precedence in the controller. Reversing it would change the Host sent by
any existing proxy that sets both, so it is left as a separate decision.
