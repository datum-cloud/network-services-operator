# `datumctl alb` plugin

| | |
|---|---|
| **Status** | Proposed |
| **Author** | Engineering |
| **Created** | 2026-09-09 |

> [!NOTE]
> Product design, not shipped code. Decisions are proposed unless marked
> **open**. Later work is **not in v1**. After this merges we reshape the
> plugin build against it. Plumbing, exit codes, and output conventions copy
> `datumctl dns` unless this document says otherwise.

## Summary

`datumctl-alb` is a first-party `datumctl` plugin. It manages Application Load
Balancers without exposing that an ALB is an `HTTPProxy`, a
`TrafficProtectionPolicy`, an Envoy `SecurityPolicy`, and an htpasswd `Secret`.

Create, print the generated hostname, then attach custom hostnames, routes,
traffic protection, request headers, and basic auth using the same payloads
the portal writes, so a CLI-created ALB stays editable in the UI.

A NetworkService is a separate noun in this plugin: create it, then point a
route at it. The UI will grow the same create flow later; the CLI writes the
payload first. `alb create` never invents a NetworkService on the side.

## Motivation

Outside the portal, ALBs are raw YAML, and that YAML is the wrong unit of work.

- Users think in load balancers. The API is several objects glued together by
  naming convention, plus a Gateway the user never sees.
- Create is not done at HTTP 201. It is done when
  `status.canonicalHostname` exists, so the user can CNAME at
  `<uid>.datumproxy.net`.
- Routes and backends are about to be first-class in the UI. A CLI that
  pretends the ALB has one origin will fight that the same way extra headers
  do today.
- The portal already sends "advanced" header work to the CLI, then locks the
  form if the CLI writes filters it does not understand — or clobbers them on
  the next origin update.
- `Accepted` / `Programmed` hide hostname conflicts, domain verification, DNS
  authority, certificate challenges, WAF `PartialFailure`, and a missing
  NetworkService.

## Goals

- Present **Application Load Balancers**, not HTTPProxies.
- Common path with no YAML: create, print hostname, attach a custom hostname.
- Create and manage NetworkServices (selector + named ports), then point a
  route at one by name and port. Do not auto-create one from `alb create`.
- Add and remove routes, and give a route one or more backends (URL and/or
  NetworkService), matching the portal multi-backend UI.
- Match portal create defaults and encoding so CLI-created ALBs stay
  form-editable.
- Merge-safe updates: preserve connectors, sibling routes, and filters the
  CLI did not create.
- Surface hostname, cert, DNS, protection, and backend kind in product words.

## Non-goals

- Replacing `datumctl apply -f`.
- Domain / DNS zone CRUD (`datumctl dns`).
- Picking members by address, location weights, or failover order — the
  service selector plus platform nearest-location ranking own that.
- Instance (VPC EndpointSlice) backends as a user flag. NetworkService is the
  product wrapper.
- Connector assignment, URL rewrite, response headers, WAF sampling /
  thresholds / exclusions.
- Metrics, logs, activity, PoP maps, caching, branded error pages.

## Product model

| Product | Stored as |
|---|---|
| Load balancer | `HTTPProxy` in `default` |
| Display name | `app.kubernetes.io/name` |
| Generated hostname | `status.canonicalHostname` |
| Custom hostname | `spec.hostnames[]` + `status.hostnameStatuses[]` |
| Default route | backend rule matching `/` |
| Extra route | another `spec.rules[]` entry (path match + backends) |
| Backend pool | `spec.rules[].backends[]` — one or more per route |
| NetworkService | `NetworkService` — label selector on interfaces + named ports |
| URL origin | `backends[].endpoint` (+ optional `tls.hostname`) |
| NetworkService origin | `backends[].networkService.{name,port}` — port is a **name** |
| Connector | `backends[].connector` (show in v1, do not assign) |
| Force HTTPS | extra rule: `x-forwarded-proto: http` → HTTPS 301, no backends |
| Host override | rule-level `RequestHeaderModifier` set `Host` |
| Other request headers | same filter; portal may treat extra names as `advanced` |
| Traffic protection | TPP targeting `Gateway/{proxy name}` |
| Basic auth | Envoy `SecurityPolicy` + Secret `{name}-basic-auth` (`{SHA}` htpasswd) |

The user never names the Gateway. Endpoint, connector, instance, and
networkService are mutually exclusive **on one backend entry**. A route may
list several entries (URL next to NetworkService is fine). NetworkService
backends have no TLS — members are reached over plaintext HTTP.

`-o json|yaml` emits the raw API objects. There is no fictional ALB CRD.

## Command surface

```
datumctl alb version

datumctl alb list     [--status active|pending|error] [-o table|wide|json|yaml|name]
datumctl alb create   <name>
                      [--endpoint URL]...
                      [--network-service NAME --port PORTNAME]...
                      [--hostname FQDN]... [--display-name TEXT]
                      [--host-header HOST] [--tls-hostname HOST]
                      [--force-https|--no-force-https]
                      [--waf-mode Enforce|Observe|Disabled]
                      [--paranoia N] [--no-waf]
                      [--wait|--no-wait] [--timeout D] [--dry-run]
datumctl alb describe <name>
datumctl alb update   <name> [--display-name TEXT]
                      [--force-https|--no-force-https] [--dry-run]
datumctl alb delete   <name> [--yes] [--dry-run]

datumctl alb network-service list
datumctl alb network-service create   <name> --workload NAME | --selector K=V...
                                      --port NAME=NUMBER [--port NAME=NUMBER]...
                                      [--dry-run]
datumctl alb network-service describe <name>
datumctl alb network-service delete   <name> [--yes] [--dry-run]
datumctl alb hostname add|remove|list <name> [<fqdn>]
datumctl alb route    add|remove|list|update <name>
                      [--path PREFIX]
                      [--endpoint URL]...
                      [--network-service NAME --port PORTNAME]...
                      [--tls-hostname HOST]
datumctl alb route backend add|remove|list <name> --path PREFIX
                      [--endpoint URL | --network-service NAME --port PORTNAME]
datumctl alb waf      set|disable|describe <name> [--mode] [--paranoia]
datumctl alb header   set|unset|list <name> [Name=value|Name]
datumctl alb auth     set|unset|list <name> [--user] [--password-stdin]
```

Aliases: `ls`, `show`/`get`, `rm`. `protection` aliases `waf`.
`network-service` aliases `nsvc`.

**`alb` not `load-balancer` or `httpproxy`.** Portal routes are `/alb`; help
text always says Application Load Balancer.

**Nested verbs, not one `update` flag set.** NetworkService is its own noun
(like `dns zone` vs `dns record`): create the service, then point the ALB at
it. Backends belong to a route's rule, so they nest under `route`, not under
the load balancer. `alb create` owns the default `/` route, optional
hostnames, Force HTTPS, and WAF defaults. Extra routes, backends on a route,
hostnames, protection, headers, and auth are later dialogs. `update` is
ALB-wide only: display name and Force HTTPS. Changing a pool is
`route update --path` (replace) or `route backend add` / `remove` (one entry).

**`<name>` is `metadata.name`.** `--display-name` writes `app.kubernetes.io/name`
(max 50). Lookup by display name is **not in v1**.

**`version` is offline.** No credentials, no project, no entitlement.

## Create

Defaults copy the portal: Force HTTPS on, WAF Enforce at paranoia 1 (Relaxed),
no custom hostnames, no Host override. At least one backend is required.
`--endpoint` and `--network-service` may both appear: each flag group is one
entry in the default `/` pool. `--endpoint` is repeatable. Each
`--network-service` is paired with the `--port` that follows it.

`--endpoint` is a URL. Missing scheme is a usage error. `https://<ip>`
requires `--tls-hostname`. `--tls-hostname` applies to the URL backends on
that command, never to a NetworkService entry.

`--network-service` names an object in the same namespace. `--port` is the
port **name** on that service (`http`, not `8080`). If it is missing, fail
not-found with a fix that names `datumctl alb network-service create` — do
not create it as a side effect of `alb create`.

`--wait` is on for create, timeout 2m, until `status.canonicalHostname` is
set. Do not wait for `Programmed`, `CertificatesReady`, or NetworkService
`Ready` — the hostname is the product of create; an empty membership is an
ordinary state of a service written before its workload.

## Hostnames

| Kind | Source | User action |
|---|---|---|
| Generated | `status.canonicalHostname` | Copy / CNAME. Never put it in `spec.hostnames`. |
| Custom | `spec.hostnames[]` | Unique on the platform. Domain auto-created if missing. No wildcards. |

Both onboarding paths are first-class: CNAME at the generated hostname, or
`hostname add` plus domain proof / Datum DNS. `hostname add` does **not** wait
(leaning no `--wait` in v1). Remove warns that Datum-managed DNS records go
with it. Unverified / `DNS not delegated` next steps point at `datumctl dns`.

## NetworkService

A NetworkService is the application: interfaces selected by label, named
ports, nearest-location ranking. An ALB is the edge that points at it. The
CLI (and later the UI) creates both; they stay two objects so several ALBs
can share one service and deleting a load balancer does not delete membership.

```
datumctl alb network-service create storefront \
  --workload storefront --port http=8080
datumctl alb create my-app --network-service storefront --port http
```

`--workload NAME` is sugar for
`compute.datumapis.com/workload-name=NAME`. `--selector K=V` (repeatable) is
the raw matchLabels form. At least one of `--workload` or `--selector` is
required; an empty selector is a usage error. `--port name=number` is
repeatable (unique names and numbers). Protocol defaults to TCP.
`trafficDistribution` is omitted so the API default (Nearest) applies — no
flag in v1.

Do not wait on create by default. Matching nothing (`NoMatchingInterfaces`)
is the ordinary state of a service written before its workload. `describe`
shows Ready / members / healthy / per-location serving. `--wait` on create,
if added, waits for `Ready` with an explicit timeout, not for a member count.

`delete` types the service name and refuses non-interactively without
`--yes`. If any HTTPProxy in the namespace still names it as a backend, the
prompt says so. It does not cascade-delete those ALBs.

The UI create flow should write this same object. The CLI is not a private
shape.

## Routes and backends

A route is a path match plus a **pool** of backends. Several routes and
several backends on one route are both first-class — write the design as if
the old single-backend cap is gone.

```
datumctl alb route list   my-app
datumctl alb route add    my-app --path /api --endpoint https://api.example.com
datumctl alb route add    my-app --path /checkout \
  --endpoint https://a.example.com --endpoint https://b.example.com
datumctl alb route add    my-app --path / \
  --network-service storefront --port http \
  --endpoint https://fallback.example.com
datumctl alb route backend add    my-app --path /api --endpoint https://api-2.example.com
datumctl alb route backend remove my-app --path /api --endpoint https://api.example.com
datumctl alb route update my-app --path / \
  --network-service storefront --port http
datumctl alb route update my-app --path /api --endpoint https://api-new.example.com
datumctl alb route remove my-app --path /api
```

- `--path` defaults to `/` on `route add` only when the ALB has no default
  route yet. A second `/` is a conflict.
- Path is prefix match in v1. Methods, headers, and exact-path are **not in
  v1**.
- Force HTTPS is a system rule (no backend). `route list` marks it; `remove`
  cannot delete it (`update --no-force-https` does).
- `alb update` does not take backends. With more than one route there is no
  single origin to replace; guessing `/` would silently miss `/api`.
- `route update --path` **replaces that path's backend list** with the
  flags given. `--path` is required. At least one backend is required. Other
  routes are left alone.
- `route backend add` / `remove` change one entry in that path's pool.
  `--path` is required. `route backend list` without `--path` lists every
  route. Removing the last backend on a route is a usage error —
  `route remove` deletes the route.
- `route remove` of `/` is refused while other user routes exist. Leaning
  refuse without `--force`.

Equal split across a pool unless the API grows a weight field; `--weight` is
then a flag on each backend group. **Open** until that field exists.

`describe`, `route list`, and `route backend list` print URL origins as URLs
and NetworkService origins as `storefront:http`, never a synthesized address.
Membership counts stay on the NetworkService object.

`--instance` is not a flag. People who need a raw EndpointSlice keep
`apply -f`.

## Headers, protection, auth

**Force HTTPS** is the portal's exact redirect rule (`x-forwarded-proto: http`,
301, no backends). Basic auth without it warns: credentials would be plaintext.

**Headers.** `--host-header` / `header set Host=...` is the portal override
(literal hostname, no wildcard, no IP). Non-Host `header set` is allowed and
**warns** if the portal still treats that as `advanced`. Unsetting the last
non-Host header must return the object to `host-only`.

**WAF.** `set` creates a same-named TPP targeting `Gateway/{name}`. `disable`
deletes the policy. Paranoia is blocking 1–4 (Relaxed / Balanced / Strict /
Maximum). Readiness: Disabled, Pending, Monitoring, Protected, Error.
Sampling, thresholds, exclusions, detection paranoia: **not in v1**.

**Auth.** `--password-stdin` required; `list` prints usernames never hashes.
`{SHA}` htpasswd. Portal validation: one user minimum, username ≤64 no
spaces/colons, password ≥4, unique names. `set` replaces the whole list.
`unset` deletes policy + secret.

## Complexity

Until the portal ships multi-route / pool editing, extra routes may still
classify as `advanced`. Once it does, extra routes, pools, and NetworkService
backends must stay form-editable — the CLI writes the same shapes. `describe`
can print class while it is still a useful warning.

Merge-unsafe rule rebuilds (wiping sibling routes or a pool the command did
not name) error. Hostname / route add / route backend add / waf / auth stay
allowed. `--force` on unsafe merges is **open**, leaning refuse.

Tests must include: NetworkService create from `--workload` and from
`--selector`, URL default route, NetworkService default route, a second
path route, a route with two URL backends plus one NetworkService, a
Connector-backed proxy left untouched by `update --display-name`, Force
HTTPS surviving `route add` / `route backend add`, `alb update` refusing
backend flags, `route update --path /api` leaving `/` alone, and
`alb create` refusing to invent a missing NetworkService.

## Status and output

List columns: name, display name, hostname, origin (first backend, `+N`
if the default route has a pool),
protection, status, age. `Active` means `Programmed=True`. Hostnames show
claimed / in use / unverified / DNS not delegated / external DNS / cert
state. A missing NetworkService is `Error` with
`NetworkServiceBackendNotFound`, not a generic pending.

`describe` is the CLI overview: status, generated hostname, routes,
protection, auth, custom hostnames, and a copyable `curl` against the
generated hostname.

ALB delete types the **object name**, refuses non-interactively without
`--yes`, and states the cascade (TPP, basic auth, Datum DNS for custom
hostnames). It does **not** delete referenced NetworkServices. Hostname
remove / route remove / route backend remove / waf disable / auth unset are
`y/N` and proceed when non-interactive.

Every mutation has server-side `--dry-run`. Patches send `resourceVersion`
and retry once on conflict.

Exit codes, `Error:` / `Fix:`, entitlement (`networking.datumapis.com`), and
name completion match dns (`ALB_` symbols). Complete `--network-service`
from NetworkService names in the namespace; complete `--port` from that
object's `spec.ports[].name`. Catalog install is phase 2.

## Payload contract

- Namespace `default`; display name `app.kubernetes.io/name`
- Force HTTPS and Host override encoded as the portal adapter does
- URL backend: `endpoint` + optional `tls.hostname`
- NetworkService object: `spec.networkInterfaces.selector` + `spec.ports`;
  omit `trafficDistribution`
- NetworkService backend: `{name, port}` only — no `endpoint`, `connector`,
  `instance`, or `tls`
- TPP `targetRefs` Gateway `{proxy name}`, group `gateway.networking.k8s.io`
- Auth Secret `{name}-basic-auth`, `{SHA}` htpasswd
- Create WAF: Enforce, blocking paranoia 1; disable = delete the policy
- Merge updates preserve sibling routes, sibling backends, `connector`, and
  unowned filters
- Client-side: at least one backend on create / route add / route update;
  URL scheme;
  FQDN-or-IP origin; TLS hostname for HTTPS IPs only; NetworkService port is
  a DNS label; hostname / header / paranoia / auth rules above

## Phasing

1. **Everyday loop** — ALB CRUD + wait-on-create, NetworkService CRUD,
   hostname / route / route backend / waf / header / auth, URL and
   NetworkService pools, version, safety, user guide. NetworkService commands
   error clearly if the CRD is not on the cluster yet.
2. **Catalog** — tagged plugin archives, `datumctl plugin install alb`.
3. **Later, as APIs and portal exist** — connector assign, backend weights
   if the field lands, WAF exclusions, multi-user auth, display-name lookup.

## Open questions

- **`hostname add --wait` in v1?** Leaning no; verification is human-paced.
- **Refuse wiping sibling routes or an unnamed pool without `--force`?**
  Leaning yes.
- **Backend weights?** Equal split until the API has a weight field.
- **Quota pre-flight vs admission error?** Skip unless the admission message
  is opaque.
- **Password prompt on TTY?** `--password-stdin` is enough for v1 if tests
  prove the secret never hits argv.
)
