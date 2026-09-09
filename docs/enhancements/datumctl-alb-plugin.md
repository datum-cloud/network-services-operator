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

The plugin **consumes** a `NetworkService` as a backend. It does not create,
select, or manage membership of that object.

## Motivation

Outside the portal, ALBs are raw YAML, and that YAML is the wrong unit of work.

- Users think in load balancers. The API is several objects glued together by
  naming convention, plus a Gateway the user never sees.
- Create is not done at HTTP 201. It is done when
  `status.canonicalHostname` exists, so the user can CNAME at
  `<uid>.datumproxy.net`.
- Routes and backends are about to be first-class in the UI. A CLI that only
  has `update --endpoint` will fight that the same way extra headers do today.
- The portal already sends "advanced" header work to the CLI, then locks the
  form if the CLI writes filters it does not understand — or clobbers them on
  the next origin update.
- `Accepted` / `Programmed` hide hostname conflicts, domain verification, DNS
  authority, certificate challenges, WAF `PartialFailure`, and a missing
  NetworkService.

## Goals

- Present **Application Load Balancers**, not HTTPProxies.
- Common path with no YAML: create, print hostname, attach a custom hostname.
- Point a route at a URL **or** at an existing NetworkService (name + named
  port). Never create the NetworkService.
- Add and remove routes that have different backends, matching the upcoming
  portal multi-backend UI.
- Match portal create defaults and encoding so CLI-created ALBs stay
  form-editable.
- Merge-safe updates: preserve connectors, sibling routes, and filters the
  CLI did not create.
- Surface hostname, cert, DNS, protection, and backend kind in product words.

## Non-goals

- Replacing `datumctl apply -f`.
- Domain / DNS zone CRUD (`datumctl dns`).
- **NetworkService CRUD** — membership, selectors, ports, and readiness live
  on that object. This plugin only names one that already exists.
- Instance (VPC EndpointSlice) backends as a user flag. NetworkService is the
  product wrapper.
- Backend pools (several origins on one path). Still `MaxItems=1` per rule.
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
| Extra route | another `spec.rules[]` entry (path match + one backend) |
| URL origin | `backends[].endpoint` (+ optional `tls.hostname`) |
| NetworkService origin | `backends[].networkService.{name,port}` — port is a **name** |
| Connector | `backends[].connector` (show in v1, do not assign) |
| Force HTTPS | extra rule: `x-forwarded-proto: http` → HTTPS 301, no backends |
| Host override | rule-level `RequestHeaderModifier` set `Host` |
| Other request headers | same filter; portal may treat extra names as `advanced` |
| Traffic protection | TPP targeting `Gateway/{proxy name}` |
| Basic auth | Envoy `SecurityPolicy` + Secret `{name}-basic-auth` (`{SHA}` htpasswd) |

The user never names the Gateway. Endpoint, connector, instance, and
networkService are mutually exclusive on one backend. NetworkService backends
have no TLS — members are reached over plaintext HTTP.

`-o json|yaml` emits the raw API objects. There is no fictional ALB CRD.

## Command surface

```
datumctl alb version

datumctl alb list     [--status active|pending|error] [-o table|wide|json|yaml|name]
datumctl alb create   <name>
                      (--endpoint URL | --network-service NAME --port PORTNAME)
                      [--hostname FQDN]... [--display-name TEXT]
                      [--host-header HOST] [--tls-hostname HOST]
                      [--force-https|--no-force-https]
                      [--waf-mode Enforce|Observe|Disabled]
                      [--paranoia N] [--no-waf]
                      [--wait|--no-wait] [--timeout D] [--dry-run]
datumctl alb describe <name>
datumctl alb update   <name> [--endpoint URL] [--tls-hostname HOST]
                      [--network-service NAME --port PORTNAME]
                      [--display-name TEXT]
                      [--force-https|--no-force-https] [--dry-run]
datumctl alb delete   <name> [--yes] [--dry-run]

datumctl alb hostname add|remove|list <name> [<fqdn>]
datumctl alb route    add|remove|list <name>
                      [--path PREFIX]
                      (--endpoint URL | --network-service NAME --port PORTNAME)
                      [--tls-hostname HOST]
datumctl alb waf      set|disable|describe <name> [--mode] [--paranoia]
datumctl alb header   set|unset|list <name> [Name=value|Name]
datumctl alb auth     set|unset|list <name> [--user] [--password-stdin]
```

Aliases: `ls`, `show`/`get`, `rm`. `protection` aliases `waf`.

**`alb` not `load-balancer` or `httpproxy`.** Portal routes are `/alb`; help
text always says Application Load Balancer.

**Nested verbs, not one `update` flag set.** Create owns the default `/`
route, optional hostnames, Force HTTPS, and WAF defaults. Extra routes,
hostnames, protection, headers, and auth are later dialogs. `update` only
changes the default `/` route (and display name / Force HTTPS).

**`<name>` is `metadata.name`.** `--display-name` writes `app.kubernetes.io/name`
(max 50). Lookup by display name is **not in v1**.

**`version` is offline.** No credentials, no project, no entitlement.

## Create

Defaults copy the portal: Force HTTPS on, WAF Enforce at paranoia 1 (Relaxed),
no custom hostnames, no Host override. Backend is required and exclusive:
`--endpoint` **or** `--network-service` plus `--port`. Mixing them is a usage
error.

`--endpoint` is a URL. Missing scheme is a usage error. `https://<ip>`
requires `--tls-hostname`. `--tls-hostname` is a usage error with
`--network-service`.

`--network-service` names an existing object in the same namespace. `--port`
is the port **name** on that service (`http`, not `8080`). If the service is
missing, admission/controller will fail; the CLI should fail faster with a
not-found and a fix that does **not** offer to create it.

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

## Routes and backends

An HTTPProxy already allows up to 16 rules. One backend per rule
(`MaxItems=1`). Multi-backend in the product is **multiple routes**, each
with a different origin — the UI is gaining that; the CLI should match it,
not treat extra rules as a form-locking escape hatch.

```
datumctl alb route list   my-app
datumctl alb route add    my-app --path /api --endpoint https://api.example.com
datumctl alb route add    my-app --path /    --network-service storefront --port http
datumctl alb route remove my-app --path /api
```

- `--path` defaults to `/` on `route add` only when the ALB has no default
  route yet. A second `/` is a conflict.
- Path is prefix match in v1. Methods, headers, and exact-path are **not in
  v1**.
- Force HTTPS is a system rule (no backend). `route list` marks it; `remove`
  cannot delete it (`update --no-force-https` does).
- `update --endpoint` / `--network-service` retargets the default `/` route
  only. It must not wipe sibling routes, connectors, or unowned filters.
- `route remove` of `/` is refused while other user routes exist, unless we
  later add an explicit `--force`. Leaning refuse.

`describe` and `route list` print URL origins as URLs and NetworkService
origins as `storefront:http`, never a synthesized address. Membership counts
and nearest-location behaviour live on the NetworkService; the ALB does not
re-explain them beyond a not-found / not-ready condition message.

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

Until the portal ships multi-route editing, extra routes may still classify
as `advanced`. Once it does, extra routes and NetworkService backends must
stay form-editable — the CLI writes the same shapes. `describe` can print
class while it is still a useful warning.

Merge-unsafe rule rebuilds (wiping sibling routes) error. Hostname / route
add / waf / auth stay allowed. `--force` on unsafe merges is **open**,
leaning refuse.

Tests must include: URL default route, NetworkService default route, a
second path route, a Connector-backed proxy left untouched by `update`, and
the Force HTTPS rule surviving `route add`.

## Status and output

List columns: name, display name, hostname, origin (URL or `name:port`),
protection, status, age. `Active` means `Programmed=True`. Hostnames show
claimed / in use / unverified / DNS not delegated / external DNS / cert
state. A missing NetworkService is `Error` with
`NetworkServiceBackendNotFound`, not a generic pending.

`describe` is the CLI overview: status, generated hostname, routes,
protection, auth, custom hostnames, and a copyable `curl` against the
generated hostname.

Delete types the **object name**, refuses non-interactively without `--yes`,
and states the cascade (TPP, basic auth, Datum DNS for custom hostnames). It
does **not** delete referenced NetworkServices. Hostname remove / route
remove / waf disable / auth unset are `y/N` and proceed when non-interactive.
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
- NetworkService backend: `{name, port}` only — no `endpoint`, `connector`,
  `instance`, or `tls`
- TPP `targetRefs` Gateway `{proxy name}`, group `gateway.networking.k8s.io`
- Auth Secret `{name}-basic-auth`, `{SHA}` htpasswd
- Create WAF: Enforce, blocking paranoia 1; disable = delete the policy
- Merge updates preserve sibling routes, `connector`, and unowned filters
- Client-side: exclusive backend flags; URL scheme; FQDN-or-IP origin; TLS
  hostname for HTTPS IPs only; NetworkService port is a DNS label; hostname /
  header / paranoia / auth rules above

## Phasing

1. **Everyday loop** — CRUD + wait-on-create, hostname / route / waf /
   header / auth, URL and NetworkService consume, version, safety, user
   guide. NetworkService flags no-op-error with a clear message if the CRD
   is not on the cluster yet.
2. **Catalog** — tagged plugin archives, `datumctl plugin install alb`.
3. **Later, as APIs and portal exist** — connector assign, backend pools if
   `MaxItems` lifts, WAF exclusions, multi-user auth, display-name lookup.

## Open questions

- **`hostname add --wait` in v1?** Leaning no; verification is human-paced.
- **Refuse wiping sibling routes without `--force`?** Leaning yes.
- **Quota pre-flight vs admission error?** Skip unless the admission message
  is opaque.
- **Password prompt on TTY?** `--password-stdin` is enough for v1 if tests
  prove the secret never hits argv.
)
