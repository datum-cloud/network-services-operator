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
that as a `URLRewrite` filter on the `HTTPRouteRule`, which the Gateway API
applies to every backend in the rule alike. Two backends wanting different
values cannot both be satisfied, so the controller refuses the pair rather than
silently applying one origin's hostname to both.

Envoy itself has no such limitation: a weighted cluster carries its own
`host_rewrite_literal`. The obstacle is reaching it. Gateway API's validating
webhook permits only `ExtensionRef`, `RequestHeaderModifier` and
`ResponseHeaderModifier` on a backend reference, and none can carry a literal
Host — `RequestHeaderModifier` is forbidden from touching `Host`, and Envoy
Gateway's own filter offers no literal hostname option.

## Proposal

Add a literal hostname modifier to Envoy Gateway's `HTTPRouteFilter`, then
reference it per backend so each origin carries its own Host rewrite.

Envoy Gateway supports and tests everything else this needs. Gateway API's
admission webhook permits an `ExtensionRef` filter on a backend reference,
`processExtensionRefHTTPFilter` routes it into `DestinationFilters.URLRewrite`,
and `xds/translator/route.go` maps that onto the weighted cluster's
`HostRewriteLiteral`. Its fixture
`http-route-weighted-backend-with-url-rewrite` shows the shape wanted: two
weighted clusters, each with its own `hostRewriteLiteral`.

The one gap is that `HTTPHostnameModifier` offers `Header` and `Backend` but no
literal, so there is no way to say "rewrite to this hostname" for a single
backend. Closing it is a change to one enum and its translation.

Rules whose backends agree keep the rule-scoped rewrite exactly as today, so
existing load balancers are untouched.

### Why not patch the generated configuration

The obvious shortcut is an `EnvoyPatchPolicy` setting `host_rewrite_literal`
per weighted cluster, reusing the escape hatch this repository already relies
on for connector routing. It does not work.

A weighted route with no backend-level filters produces no weighted clusters.
Envoy Gateway emits one merged cluster and carries the weights as locality
weights inside it, and `host_rewrite_literal` has no per-locality form — so in
the shape we generate there is nowhere to attach a per-backend rewrite, and a
patch aimed at one matches nothing.

The topology can be forced by giving each backend a filter, since any
backend-level filter switches Envoy Gateway to a cluster per backend. That
stacks two mechanisms — one to change the shape of generated configuration, one
to exploit the shape it changed into — on internals no API contract covers,
failing with every origin receiving the wrong Host. Not worth owning.

Evidence and the fixture cross-reference behind this are in
[network-services-operator#473](https://github.com/datum-cloud/network-services-operator/issues/473).

## Alternatives considered

**Wait for Gateway API to drop the webhook restriction.** The CRD's own
validation already permits what the webhook refuses, and the webhook is
deprecated in favour of CEL, so this may resolve on its own. Not something to
plan around.

**Document the limitation and reject the configuration clearly.** The cheapest
option, and worth doing regardless — network-services-operator#469 makes the
refusal visible on the resource instead of only in controller logs. It does not
give anyone the feature.

**Require one rule per origin.** Backends in separate rules are matched, not
weighted, so this cannot express "5% of the same traffic". It is a different
feature.

## Open questions

### What happens when a rewrite does not take effect

Settle this first: the failure mode is a wrong answer rather than an outage.

If the per-backend filter is missing, unresolvable or silently ignored, the
route still splits traffic but without the rewrite, so origins receive requests
addressed to the load balancer's own hostname and answer 404 or serve the wrong
site.

The proposal is to refuse rather than degrade — do not widen traffic to a
backend whose rewrite is not in place, and say so on the resource — but that
needs agreeing rather than assuming.

### One path or two

The proposal keeps the rule-scoped rewrite for backends that agree and uses
per-backend filters only for those that do not, leaving existing load balancers
untouched. The cost is two code paths and two behaviours to maintain
indefinitely.

### What to do until the upstream change lands

This does not ship until an upstream release carries the modifier. Wait, carry
a patched Envoy Gateway, or ship network-services-operator#469's clearer
refusal and treat the capability as known-missing meanwhile. The third is
honest but leaves the gap open for a release cycle or more.

## Known issues

Adjacent, though independent of this proposal: a rule-level Host override takes
precedence over a backend-level one, so the less specific wins and "this Host
for the pool, except the canary" cannot be expressed. Per-backend overrides
work on their own, so it only bites when both are set.
