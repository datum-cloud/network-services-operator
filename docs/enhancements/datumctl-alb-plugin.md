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

Create, print the generated hostname, then attach custom hostnames, traffic
protection, request headers, and basic auth using the same payloads the portal
writes, so a CLI-created ALB stays editable in the UI.

## Motivation

Outside the portal, ALBs are raw YAML, and that YAML is the wrong unit of work.

- Users think in load balancers. The API is four objects glued together by
  naming convention, plus a Gateway the user never sees.
- Create is not done at HTTP 201. It is done when
  `status.canonicalHostname` exists, so the user can CNAME at
  `<uid>.datumproxy.net`.
- The portal already sends "advanced" header work to the CLI, then locks the
  form if the CLI writes filters it does not understand — or clobbers them on
  the next origin update.
- `Accepted` / `Programmed` hide hostname conflicts, domain verification, DNS
  authority, certificate challenges, and WAF `PartialFailure`.

## Goals

- Present **Application Load Balancers**, not HTTPProxies.
- Common path with no YAML: create, print hostname, attach a custom hostname.
- Match portal create defaults and encoding so objects stay `simple` /
  `host-only` and form-editable.
- Merge-safe updates: preserve connectors, extra rules, and filters the CLI
  did not create.
- Surface hostname, cert, DNS, and protection state in product words.

## Non-goals

- Replacing `datumctl apply -f`.
- Domain / DNS zone CRUD (`datumctl dns` already exists; this plugin points
  at it).
- Metrics, logs, activity, PoP maps, caching, branded error pages.
- Connector / instance backend assignment, backend pools, path matching, URL
  rewrite, response headers, WAF sampling / thresholds / exclusions.

## Product model

| Product | Stored as |
|---|---|
| Load balancer | `HTTPProxy` in `default` |
| Display name | `app.kubernetes.io/name` |
| Generated hostname | `status.canonicalHostname` |
| Custom hostname | `spec.hostnames[]` + `status.hostnameStatuses[]` |
| Origin | `spec.rules[].backends[].endpoint` (+ optional `tls.hostname`) |
| Connector | `backends[].connector` (show in v1, do not assign) |
| Force HTTPS | extra rule: `x-forwarded-proto: http` → HTTPS 301, no backends |
| Host override | rule-level `RequestHeaderModifier` set `Host` |
| Other request headers | same filter; portal treats the ALB as `advanced` |
| Traffic protection | TPP targeting `Gateway/{proxy name}` |
| Basic auth | Envoy `SecurityPolicy` + Secret `{name}-basic-auth` (`{SHA}` htpasswd) |

The user never names the Gateway. The operator synthesizes it; the policy is
named after the proxy because attachment is by that Gateway name.

`-o json|yaml` emits the raw API objects. There is no fictional ALB CRD.

## Command surface

```
datumctl alb version

datumctl alb list     [--status active|pending|error] [-o table|wide|json|yaml|name]
datumctl alb create   <name> --endpoint URL [--hostname FQDN]...
                      [--display-name TEXT] [--host-header HOST]
                      [--tls-hostname HOST]
                      [--force-https|--no-force-https]
                      [--waf-mode Enforce|Observe|Disabled]
                      [--paranoia N] [--no-waf]
                      [--wait|--no-wait] [--timeout D] [--dry-run]
datumctl alb describe <name>
datumctl alb update   <name> [--endpoint URL] [--tls-hostname HOST]
                      [--display-name TEXT]
                      [--force-https|--no-force-https] [--dry-run]
datumctl alb delete   <name> [--yes] [--dry-run]

datumctl alb hostname add|remove|list <name> [<fqdn>]
datumctl alb waf      set|disable|describe <name> [--mode] [--paranoia]
datumctl alb header   set|unset|list <name> [Name=value|Name]
datumctl alb auth     set|unset|list <name> [--user] [--password-stdin]
```

Aliases: `ls`, `show`/`get`, `rm`. `protection` aliases `waf`.

**`alb` not `load-balancer` or `httpproxy`.** Portal routes are `/alb`; help
text always says Application Load Balancer.

**Nested verbs, not one `update` flag set.** Create owns origin, optional
hostnames, Force HTTPS, and WAF defaults. Hostnames, protection, headers, and
auth are later portal dialogs and independent objects. `update` only changes
the proxy document (origin, TLS hostname, display name, Force HTTPS).

**`<name>` is `metadata.name`.** The portal generates a slug from a display
name; scripts need a name they chose. `--display-name` writes the annotation
(max 50 characters). Lookup by display name is **not in v1** (names are not
unique).

**`version` is offline.** No credentials, no project, no entitlement.

## Create

Defaults copy the portal dialog: Force HTTPS on, WAF Enforce at paranoia 1
(Relaxed), no custom hostnames, no Host override. `--endpoint` is a URL
(paste), not a protocol+host split. Missing scheme is a usage error.
`https://<ip>` requires `--tls-hostname`. `--no-waf` skips the policy;
`--waf-mode Disabled` is the same outcome.

`--wait` is on for create, timeout 2m, until `status.canonicalHostname` is
set. That is the string the user CNAMEs at. Do not wait for `Programmed` or
`CertificatesReady` — those can lag or stay pending on an unverified custom
hostname.

`hostname add` does **not** wait. Verification can take hours; print
conditions and a next step instead. `--wait` there is **open**, leaning no
for v1.

## Hostnames

| Kind | Source | User action |
|---|---|---|
| Generated | `status.canonicalHostname` | Copy / CNAME. Never put it in `spec.hostnames`. |
| Custom | `spec.hostnames[]` | Unique on the platform. Domain auto-created if missing. No wildcards. |

Both onboarding paths are first-class: CNAME at the generated hostname (no
`hostname add`), or `hostname add` plus domain proof / Datum DNS for a cert
on `app.example.com`. Create success should offer both, not push custom
hostnames as mandatory.

`hostname remove` warns that Datum-managed DNS records for that name go with
it. Conflicts use the server's message.

When a hostname is `DNS not delegated` or unverified, next steps point at
`datumctl dns` rather than inventing a second delegation UI.

## Origins, headers, protection, auth

**Origin.** One public URL (`MaxItems=1`). `update --endpoint` rebuilds the
backend rule but **must** keep an existing connector, extra rules, and header
`set` entries the CLI does not own. Replacing `spec.rules` wholesale detaches
Connector-backed ALBs.

**Force HTTPS** is the portal's exact redirect rule (`x-forwarded-proto: http`,
301, no backends). Other redirects are left alone. Basic auth without Force
HTTPS warns: credentials would be plaintext.

**Headers.** `--host-header` / `header set Host=...` is the portal override
(literal hostname, no wildcard, no IP). Non-Host `header set` is allowed and
**warns** that the portal will lock the form (`advanced`). Unsetting the last
non-Host header must return the object to `host-only` so the form unlocks.
Refuse-all and silent-allow were both worse.

**WAF.** `set` creates a same-named TPP targeting `Gateway/{name}`. `disable`
deletes the policy (do not write `mode: Disabled`). Paranoia is blocking 1–4
(Relaxed / Balanced / Strict / Maximum). `describe` shows portal readiness:
Disabled, Pending, Monitoring, Protected, Error. Detection paranoia, sampling,
thresholds, and exclusions are **not in v1**.

**Auth.** `--password-stdin` required; `list` prints usernames never hashes.
`{SHA}` htpasswd only (Envoy). Portal validation: one user minimum, username
≤64 no spaces/colons, password ≥4, unique names. `set` replaces the whole
user list (dialog save). `unset` deletes policy + secret. `auth add`/`remove`
is **not in v1**.

## Complexity

Portal classes: `simple` (no backend-rule filters), `host-only` (exactly Host
set), `advanced` (anything else) → form read-only.

Create and portal-equivalent edits stay simple/host-only. `describe` prints
the class. Merge-unsafe rule rebuilds error unless we add `--force`
(**open**, leaning: refuse unsafe merges; `hostname add` still allowed).

Tests must include a Connector-backed proxy and an advanced proxy, not only
the simple case.

## Status and output

List columns: name, display name, hostname, origin, protection, status, age.
`Active` means `Programmed=True` (portal badge). Hostnames show claimed / in
use / unverified / DNS not delegated / external DNS / cert state. Protection
uses the portal readiness words, not CRD mode alone. Unknown reasons pass
through raw.

`describe` is the CLI overview: status, generated hostname, origin,
protection, auth, custom hostnames, and next steps (including a copyable
`curl` against the generated hostname).

Delete types the **object name**, refuses non-interactively without `--yes`,
and states the cascade (TPP, basic auth, Datum DNS for custom hostnames).
Hostname remove / waf disable / auth unset are `y/N` and proceed when
non-interactive. Every mutation has server-side `--dry-run`. Patches send
`resourceVersion` and retry once on conflict.

Exit codes, `Error:` / `Fix:` rendering, entitlement
(`networking.datumapis.com`), and completion of names match dns (`ALB_`
symbols). Catalog install is phase 2; until then
`datumctl plugin install datum-cloud/network-services-operator@<tag>`.

## Payload contract

- Namespace `default`; display name `app.kubernetes.io/name`
- Force HTTPS and Host override encoded as the portal adapter does
- TPP `targetRefs` Gateway `{proxy name}`, group `gateway.networking.k8s.io`
- Auth Secret `{name}-basic-auth`, `{SHA}` htpasswd
- Create WAF: Enforce, blocking paranoia 1; disable = delete the policy
- Merge updates preserve `connector` and unowned filters
- Client-side: URL scheme, FQDN-or-IP origin, TLS hostname for HTTPS IPs,
  hostname/header/paranoia/auth rules above

## Phasing

1. **Everyday loop** — CRUD + wait-on-create, hostname / waf / header / auth,
   version, safety, user guide.
2. **Catalog** — tagged plugin archives, `datumctl plugin install alb`.
3. **Configuration UX, as APIs and portal dialogs exist** — connectors,
   backend pools, WAF exclusions, multi-user auth, display-name lookup.

## Open questions

- **`hostname add --wait` in v1?** Leaning no; verification is human-paced.
- **Refuse advanced rule rebuilds without `--force`?** Leaning yes for
  `spec.rules`; keep hostname/auth/waf edits.
- **Quota pre-flight vs admission error?** Skip unless the admission message
  is opaque.
- **Password prompt on TTY?** `--password-stdin` is enough for v1 if tests
  prove the secret never hits argv.
)
