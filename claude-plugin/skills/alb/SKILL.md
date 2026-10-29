---
name: alb
description: 'Drive Datum Cloud Application Load Balancers yourself with the datumctl alb plugin: create one, attach hostnames, routes, origins, headers, traffic protection and basic auth, read access logs, and attach advanced Envoy Gateway policies. Use when the person wants datumctl commands or manifests rather than the datum-mcp tools.'
---

# Skill: Application Load Balancer

> **Datum's own assistant** can answer questions about a load balancer directly, using read-only tools of its own. This skill is a different surface: it teaches you to drive `datumctl` yourself.

## Description

Create and manage Application Load Balancers on Datum Cloud — take internet traffic on one or more hostnames and send it to origins, with HTTPS, a web application firewall, and optional basic authentication.

An Application Load Balancer is a **product built from several objects**, not a single kind. Prefer `datumctl alb`, which presents it as one thing; drop to raw manifests only for the advanced policies at the end of this document.

## Capabilities

- Create a load balancer and get the hostname to point DNS at
- Attach custom hostnames and follow their verification, DNS and certificate progress
- Add routes and the origins behind them, by URL or by network service
- Split a route's traffic across several origins by weight, choose the load balancing algorithm, and turn on passive health checks
- Configure traffic protection (WAF), request headers and basic authentication
- Read access logs for one load balancer
- Preview every change before applying it
- Attach advanced Envoy Gateway policies the plugin does not cover

## Before you run anything: install the plugin

`datumctl alb` is a plugin, not built in. Without it every command below fails
with `unknown command "alb"`.

```bash
datumctl plugin install alb
datumctl alb version
```

That installs it from the official Datum catalog as a managed plugin, so
datumctl tracks it, verifies the download, and upgrades it with
`datumctl plugin upgrade alb`. `alb version` needs no login, no project and no
network, so run it first whenever anything else fails.

## What an ALB is made of

| What you think about | What is stored |
|---|---|
| The load balancer | An `HTTPProxy` you create |
| Its generated hostname | `status.canonicalHostname` |
| A custom hostname | `spec.hostnames[]`, progress in `status.hostnameStatuses[]` |
| A route | One `spec.rules[]` entry: a path prefix and its origins |
| An origin | A URL, or a `NetworkService` and one of its declared **port names** |
| An origin's share of a route | `weight` on that origin, relative to the route's others; 1 when unset |
| The load balancing algorithm | `spec.loadBalancer`, one setting for every route |
| Health checks | `spec.healthCheck.passive`, one setting for every route |
| Force HTTPS | An extra redirect rule with no origins |
| Traffic protection | A `TrafficProtectionPolicy` attached to the load balancer |
| Basic auth | A `SecurityPolicy` and a `<name>-basic-auth` Secret |

You never name or edit the routing objects Datum derives from the `HTTPProxy`.

## The everyday commands

```bash
datumctl alb create my-app --endpoint https://origin.example.com
datumctl alb list
datumctl alb describe my-app
datumctl alb update my-app --display-name "Production API"
datumctl alb delete my-app --yes
```

`create` waits for the generated hostname (`<id>.datumproxy.net`) — that is what you CNAME at. Defaults match the portal: Force HTTPS on, traffic protection in `Enforce` at paranoia 1.

Omit the name and pass `--display-name` to derive a DNS-safe name the way the portal does. The printed message shows the name that was used; **carry it forward**, because it includes a random suffix and re-deriving gives a different one.

### Hostnames

```bash
datumctl alb hostname add my-app app.example.com
datumctl alb hostname list my-app
datumctl alb hostname remove my-app app.example.com
```

Two ways a hostname reaches a load balancer, and they fail differently:

- **Your own DNS** — create a CNAME to the generated hostname. Datum writes nothing. At a zone apex a CNAME is not legal; use ALIAS/ANAME if your provider offers it, or a subdomain.
- **A custom hostname** — prove you own the domain, then it goes through claim → ownership → DNS record → certificate, each gating the next.

`describe` prints each custom hostname with its available / DNS / certificate status.

### Routes and origins

```bash
datumctl alb route list my-app
datumctl alb route add my-app --path /api --endpoint https://api.example.com
datumctl alb route update my-app --path / --network-service storefront --port http
datumctl alb route remove my-app --path /api
datumctl alb route backend add my-app --path /api --endpoint https://api-2.example.com
datumctl alb route backend add my-app --path /api --endpoint https://api-3.example.com --weight 3
datumctl alb route backend update my-app --path /api --endpoint https://api-2.example.com --weight 0
datumctl alb route backend list my-app
```

`--port` is the port **name** the service declares (`http`), never a number. The load balancer never creates a `NetworkService`; naming one that does not exist fails before anything is written. Create it first with `datumctl network service create` — see the [networks skill](../networks/SKILL.md).

`route update` replaces every origin on that path and leaves other routes alone. Origins that stay in the list keep their weight, TLS hostname and filters.

A route takes up to 16 origins and splits requests across them by weight. Weights are relative: origins at 1 and 3 get 25% and 75%, and an origin with no weight counts as 1. `route backend list` shows each origin's `WEIGHT` and `SHARE`.

- `route backend add` without `--weight` gives the new origin the average weight of the pool, so it joins with an even share rather than a sliver.
- `--weight 0` drains an origin: it stays in the pool and gets no requests. A change that would leave every origin on a route at 0 is refused.

Each URL origin is sent its own hostname as the Host header, so origins on different hostnames can share a route. A Host override on the route (`alb header set my-app Host=...`) is the trap: it sends the same Host to every origin, so in a pool spanning several hostnames any origin that serves by hostname (Vercel, Netlify, Fly.io, Cloudflare Pages) answers the wrong site or a 404. It looks like it worked.

A connector origin must be the only origin in its route.

### Load balancing and health checks

Both apply to every route on the load balancer and are set with `alb update`. `describe` shows them.

```bash
datumctl alb update my-app --algorithm round-robin
datumctl alb update my-app --hash-header X-User-ID
datumctl alb update my-app --algorithm default
datumctl alb update my-app --health-checks
datumctl alb update my-app --consecutive-5xx 3 --base-ejection-time 1m --max-ejection-percent 50
datumctl alb update my-app --no-health-checks
```

`--algorithm` takes `round-robin`, `random`, `least-request` or `consistent-hash`. Least request applies when nothing is set, and `default` returns to that. `consistent-hash` keeps a client on one origin by its source IP, or by the header `--hash-header` names. A test run from one machine has one IP, so under consistent hash it lands on one origin every time; that is not a broken split.

Passive health checks are off unless turned on. An endpoint that returns a run of 5xx responses (5 by default) gets no requests for a while (30s at first, longer each time it repeats), then gets them again. At most 50% of an origin's endpoints are ejected at once by default. There are no active probes. Any tuning flag turns the checks on and keeps the values already set.

Know what the checks do not do before promising resilience:

- **They act inside one origin.** They move requests between that origin's endpoints: the addresses its hostname resolves to, or a network service's members. **They never move an origin's share to another origin.** The weights decide that, so a broken origin keeps failing its share until you drain or remove it.
- **An origin with one endpoint gains little.** Once fewer than half of an origin's endpoints are left, a route whose origins are all URLs spreads requests over every endpoint again, failing ones included. A route with a network service origin fails those requests fast instead.

### Traffic protection, headers, auth, logs

```bash
datumctl alb waf set my-app --mode Observe --paranoia 1
datumctl alb waf describe my-app
datumctl alb header set my-app Host=origin.example.com
echo 'secret' | datumctl alb auth set my-app --user admin --password-stdin
datumctl alb logs my-app --since 1h --code 502
```

Passwords are never printed and must come from stdin — never put one in a command line or a manifest. Every `--user` in one command gets the same password, and `auth set` replaces the whole user list rather than merging, so pass every user you want to keep.

`tpp` is an alias for `waf`. `--paranoia` sets the blocking and detection levels together and takes 1 to 4, but the portal offers only 1 and 2.

## What you cannot get here

**Metrics.** There is no command for request rates, latency percentiles, error
rates over time, or the traffic protection breakdown, and no API to build one
on yet — the platform's metric routes are defined but return 501. Do not infer
a trend from `alb logs`, which returns at most 500 lines from one window. Send
people to the Metrics tab for that load balancer in the cloud portal.

**Activity and audit history.** `alb logs` is request traffic only. Control-plane
changes are `datumctl activity`.

## Reading status without getting it wrong

Three properties of this API produce confident wrong answers. They matter whether you use the plugin or raw manifests:

- **Top-level conditions aggregate and name nothing.** `DNSRecordsProgrammed=PartialFailure` and `CertificatesReady=CertificatesPending` mean "one or more hostnames". Which one is in `status.hostnameStatuses[]`. Always resolve to the hostname before reporting.
- **`HostnamesInUse` is true when something is wrong.** Every other condition here is true when healthy, so a general "not True means broken" check silently treats a hostname collision as fine.
- **`DNSZoneNotFound` and `NotApplicable` are set True on purpose.** They mean Datum does not run that domain's DNS — a normal arrangement. Not a fault, and not something to wait for.

Also: the same reason word means different things on different conditions. `Pending` on `Accepted` means nothing has reconciled; on a hostname's `CertificateReady` it means the certificate is not issued. Always read the condition type with the reason.

Two things status does not report at all: whether Datum's edge can actually serve the load balancer (conditions go true while the edge is still converging, and no window is published — confirm with a request), and a generated hostname's DNS record (no condition exists, so its absence means nothing).

## Advanced: policies the plugin does not cover

Attach these by manifest when you need rate limiting, OIDC, or WAF rule tuning. All attach via `targetRefs` to the Gateway derived from the load balancer, which carries the same name.

### WAF rule exclusions and score thresholds

```yaml
apiVersion: networking.datumapis.com/v1alpha
kind: TrafficProtectionPolicy
metadata:
  name: my-app
  namespace: default
spec:
  mode: Enforce
  ruleSets:
    - type: OWASPCoreRuleSet
      owaspCoreRuleSet:
        paranoiaLevels:
          detection: 2
          blocking: 1
        ruleExclusions: [942100, 920350]
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: my-app
```

Keep `detection` at or above `blocking`, so you can preview stricter rules in the logs before enforcing them.

### Rate limiting and circuit breaking

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: traffic-control
  namespace: default
spec:
  rateLimit:
    type: Local
    local:
      rules:
        - limit: { requests: 100, unit: Minute }
  circuitBreaker:
    maxConnections: 500
    maxPendingRequests: 100
    maxParallelRequests: 1000
    maxParallelRetries: 3
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: my-app
```

A local limit counts requests on each edge node separately, so the limit a client actually meets depends on how many nodes serve it.

Datum attaches a `BackendTrafficPolicy` of its own to the load balancer's routing when the load balancer has a network service origin, an algorithm or health checks set. It merges into a Gateway-level policy like the one above, and Datum's settings win where both set the same field, so set the algorithm and health checks with `datumctl alb update` rather than in this policy.

### Authentication beyond basic auth

`SecurityPolicy` (`gateway.envoyproxy.io/v1alpha1`) carries JWT, OIDC, API key and external authorization. `datumctl alb auth` covers basic auth only; anything else is a manifest. See the Datum guides for OIDC with Google or Auth0.

```bash
datumctl get securitypolicies --project <project-id>
datumctl describe securitypolicy <name> --project <project-id>
```

## Driving this from a script or an agent

Four behaviours will catch you out if you key off exit status or assume a prompt:

- **`create` can exit non-zero having created the load balancer.** With the default `--wait`, a timeout waiting for the generated hostname is an error, but the object exists. Do not retry `create` — run `describe` first, or pass `--no-wait` and poll.
- **`create` is not atomic.** If the load balancer is created and traffic protection then fails to attach, the command exits non-zero and leaves an unprotected load balancer behind. Check `waf describe` after a failed create.
- **Confirmation prompts assume yes with no terminal.** `route remove` and `route backend remove` proceed in CI. Only `delete` refuses without `--yes`.
- **A change is accepted before it is published.** Validation at write time does not cover everything the platform checks when it publishes, so run `describe` after changing origins and confirm the status is not `Error`.

Every writing command takes `--dry-run`, which validates against the API and discards the change. Use it before anything destructive.

Failures print a named exit code — `exit status 6 # ALB_INVALID`. The names are `ALB_USAGE` (2), `ALB_FORBIDDEN` (3), `ALB_NOT_FOUND` (4), `ALB_CONFLICT` (5), `ALB_INVALID` (6), `ALB_UNAVAILABLE` (8), `ALB_ABORTED` (9).

## Constraints & Guardrails

- Always use `datumctl` — never `kubectl`
- `--project` is required, or select one with `datumctl ctx use`
- `networking.datumapis.com/v1alpha` and `gateway.envoyproxy.io/v1alpha1` are unstable; field names may change between releases
- Prefer `datumctl alb` over raw manifests
- The portal manages one route: the pool of origins on `/`, with weights, the algorithm and health checks. Those are the same fields `datumctl alb` writes, so either can change them. Once a load balancer has a second route, a path match or a per-origin filter, the portal locks its origins, weights, algorithm, health checks, TLS, redirect and Host override and points to `datumctl`; hostnames, protection and auth stay editable there. Tell the user to expect that lock after you add a route
- Traffic protection takes paranoia 1 to 4 here; the portal offers only 1 and 2
- Run `datumctl diff -f` before `apply`, and validate with `--dry-run=server`
- `delete` has no confirmation prompt — verify the name first. Deleting a load balancer also removes its traffic protection policy and basic auth
- Start traffic protection in `mode: Observe` on a live site, watch real traffic, then move to `Enforce`. Raise paranoia one level at a time; above 2 the false-positive rate climbs sharply
- Never put a password in a command line or a manifest — `--password-stdin` only
- The load balancer never creates a `NetworkService` or a `Domain` for you; create a service with `datumctl network service create`
- A certificate cannot be issued until the hostname resolves publicly to the load balancer, so a stuck certificate is usually a DNS problem

## See Also

- [Application Load Balancer documentation](https://www.datum.net/docs/alb/overview.md)
- [`datumctl` overview](https://www.datum.net/docs/datumctl/overview.md)
- [Envoy Gateway SecurityPolicy](https://gateway.envoyproxy.io/docs/api/extension_types/#securitypolicy)
- [OWASP Core Rule Set](https://coreruleset.org/)
